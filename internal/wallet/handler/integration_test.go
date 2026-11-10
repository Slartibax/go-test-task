//go:build integration

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"go-test-task/internal/migrate"
	db "go-test-task/internal/postgres"
	"go-test-task/internal/wallet"
	"go-test-task/internal/wallet/handler"
	walletpg "go-test-task/internal/wallet/postgres"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	code, err := runIntegration(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func runIntegration(m *testing.M) (code int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:17.5",
		postgres.WithDatabase("go_test_task"),
		postgres.WithUsername("go_test_task"),
		postgres.WithPassword("go_test_task"),
		postgres.BasicWaitStrategies(),
	)
	if container != nil {
		defer func() {
			termCtx, termCancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
			defer termCancel()
			if termErr := container.Terminate(termCtx); termErr != nil {
				err = errors.Join(err, fmt.Errorf("terminate postgres: %w", termErr))
				if code == 0 {
					code = 1
				}
			}
		}()
	}
	if err != nil {
		return 1, fmt.Errorf("start postgres: %w", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 1, fmt.Errorf("connection string: %w", err)
	}
	pool, err := db.Open(ctx, dsn, db.Config{MaxConns: 40, MinConns: 10})
	if err != nil {
		return 1, err
	}
	defer pool.Close()
	testPool = pool

	if err := migrate.Apply(ctx, pool); err != nil {
		return 1, err
	}
	if err := migrate.Apply(ctx, pool); err != nil {
		return 1, fmt.Errorf("repeat migration: %w", err)
	}

	code = m.Run()
	return code, nil
}

func TestWalletFlow(t *testing.T) {
	base, client := startWalletAPI(t)
	id := uuid.New()

	missing, err := postWalletOperation(t.Context(), client, base, id, "WITHDRAW", 10)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, missing.code, missing.raw)

	got, err := getWalletBalance(t.Context(), client, base, id)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, got.code, got.raw)

	deposited, err := postWalletOperation(t.Context(), client, base, id, "DEPOSIT", 100)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, deposited.code, deposited.raw)
	require.Equal(t, int64(100), deposited.balance)

	withdrawn, err := postWalletOperation(t.Context(), client, base, id, "WITHDRAW", 40)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, withdrawn.code, withdrawn.raw)
	require.Equal(t, int64(60), withdrawn.balance)

	over, err := postWalletOperation(t.Context(), client, base, id, "WITHDRAW", 100)
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, over.code, over.raw)
	require.Equal(t, "insufficient funds", over.message)

	final, err := getWalletBalance(t.Context(), client, base, id)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, final.code, final.raw)
	require.Equal(t, int64(60), final.balance)
}

func TestCreatedAtStaysAtCreation(t *testing.T) {
	repo := walletpg.NewRepository(testPool)
	id := uuid.New()

	_, err := repo.DepositOrCreate(t.Context(), id, 100)
	require.NoError(t, err)
	createdAt, updatedAt := readWalletTimestamps(t, id)
	require.False(t, createdAt.IsZero())
	require.True(t, createdAt.Equal(updatedAt))

	time.Sleep(5 * time.Millisecond)

	_, err = repo.DepositOrCreate(t.Context(), id, 25)
	require.NoError(t, err)
	_, err = repo.Withdraw(t.Context(), id, 10)
	require.NoError(t, err)

	createdAfter, updatedAfter := readWalletTimestamps(t, id)
	require.True(t, createdAt.Equal(createdAfter))
	require.True(t, updatedAfter.After(updatedAt))
}

func readWalletTimestamps(t *testing.T, id uuid.UUID) (time.Time, time.Time) {
	t.Helper()

	var createdAt, updatedAt time.Time
	err := testPool.QueryRow(
		t.Context(),
		`SELECT created_at, updated_at FROM wallets WHERE id = $1`,
		id,
	).Scan(&createdAt, &updatedAt)
	require.NoError(t, err)
	return createdAt, updatedAt
}

func TestConcurrentDeposits(t *testing.T) {
	base, client := startWalletAPI(t)
	id := uuid.New()
	const n = 1000

	results := postOperationsInParallel(t.Context(), client, base, id, repeatOperation("DEPOSIT", n), 1)
	seen := make(map[int64]int, n)
	for i, result := range results {
		require.NoErrorf(t, result.err, "deposit %d", i)
		require.Equalf(t, http.StatusOK, result.code, "deposit %d body %s", i, result.raw)
		require.GreaterOrEqualf(t, result.balance, int64(0), "deposit %d", i)
		seen[result.balance]++
	}
	require.Len(t, seen, n)
	for amount := int64(1); amount <= n; amount++ {
		require.Equal(t, 1, seen[amount], "balance %d", amount)
	}

	final, err := getWalletBalance(t.Context(), client, base, id)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, final.code, final.raw)
	require.Equal(t, int64(n), final.balance)
}

func TestConcurrentMixed(t *testing.T) {
	base, client := startWalletAPI(t)
	id := uuid.New()

	seed, err := postWalletOperation(t.Context(), client, base, id, "DEPOSIT", 1000)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, seed.code, seed.raw)
	require.Equal(t, int64(1000), seed.balance)

	const half = 400
	ops := append(repeatOperation("DEPOSIT", half), repeatOperation("WITHDRAW", half)...)
	results := postOperationsInParallel(t.Context(), client, base, id, ops, 1)
	for i, result := range results {
		require.NoErrorf(t, result.err, "op %d", i)
		require.Equalf(t, http.StatusOK, result.code, "op %d body %s", i, result.raw)
		require.GreaterOrEqualf(t, result.balance, int64(0), "op %d", i)
	}

	final, err := getWalletBalance(t.Context(), client, base, id)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, final.code, final.raw)
	require.Equal(t, int64(1000), final.balance)
}

func TestConcurrentOverdraft(t *testing.T) {
	base, client := startWalletAPI(t)
	id := uuid.New()

	seed, err := postWalletOperation(t.Context(), client, base, id, "DEPOSIT", 50)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, seed.code, seed.raw)

	results := postOperationsInParallel(t.Context(), client, base, id, repeatOperation("WITHDRAW", 100), 1)
	var okCount, conflictCount int
	for i, result := range results {
		require.NoErrorf(t, result.err, "withdraw %d", i)
		switch result.code {
		case http.StatusOK:
			okCount++
			require.GreaterOrEqualf(t, result.balance, int64(0), "withdraw %d", i)
		case http.StatusConflict:
			conflictCount++
			require.Equalf(t, "insufficient funds", result.message, "withdraw %d", i)
		default:
			require.Failf(t, "unexpected status", "withdraw %d: status %d body %s", i, result.code, result.raw)
		}
	}
	require.Equal(t, 50, okCount)
	require.Equal(t, 50, conflictCount)

	final, err := getWalletBalance(t.Context(), client, base, id)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, final.code, final.raw)
	require.Equal(t, int64(0), final.balance)
}

func startWalletAPI(t *testing.T) (string, *http.Client) {
	t.Helper()

	mux := http.NewServeMux()
	handler.New(wallet.NewService(walletpg.NewRepository(testPool))).Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        128,
			MaxIdleConnsPerHost: 128,
		},
	}
	t.Cleanup(client.CloseIdleConnections)
	return server.URL, client
}

type walletAPIResponse struct {
	code    int
	balance int64
	message string
	raw     string
	err     error
}

func postOperationsInParallel(ctx context.Context, client *http.Client, base string, id uuid.UUID, ops []string, amount int64) []walletAPIResponse {
	results := make([]walletAPIResponse, len(ops))
	var wg sync.WaitGroup
	wg.Add(len(ops))
	for i := range ops {
		go func(i int) {
			defer wg.Done()
			result, err := postWalletOperation(ctx, client, base, id, ops[i], amount)
			if err != nil {
				results[i] = walletAPIResponse{err: err}
				return
			}
			results[i] = result
		}(i)
	}
	wg.Wait()
	return results
}

func repeatOperation(operation string, n int) []string {
	ops := make([]string, n)
	for i := range ops {
		ops[i] = operation
	}
	return ops
}

func postWalletOperation(ctx context.Context, client *http.Client, base string, id uuid.UUID, operation string, amount int64) (walletAPIResponse, error) {
	body, err := json.Marshal(map[string]any{
		"walletId":      id,
		"operationType": operation,
		"amount":        amount,
	})
	if err != nil {
		return walletAPIResponse{}, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/wallet", bytes.NewReader(body))
	if err != nil {
		return walletAPIResponse{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return readWalletAPIResponse(client, req)
}

func getWalletBalance(ctx context.Context, client *http.Client, base string, id uuid.UUID) (walletAPIResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/wallets/"+id.String(), http.NoBody)
	if err != nil {
		return walletAPIResponse{}, fmt.Errorf("create request: %w", err)
	}
	return readWalletAPIResponse(client, req)
}

func readWalletAPIResponse(client *http.Client, req *http.Request) (walletAPIResponse, error) {
	resp, err := client.Do(req)
	if err != nil {
		return walletAPIResponse{}, fmt.Errorf("do request: %w", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return walletAPIResponse{}, errors.Join(fmt.Errorf("read response: %w", readErr), closeErr)
	}
	if closeErr != nil {
		return walletAPIResponse{}, fmt.Errorf("close response: %w", closeErr)
	}

	var payload struct {
		Balance int64  `json:"balance"`
		Error   string `json:"error"`
	}
	if unmarshalErr := json.Unmarshal(body, &payload); unmarshalErr != nil {
		return walletAPIResponse{code: resp.StatusCode, raw: string(body)}, fmt.Errorf("decode response: %w", unmarshalErr)
	}
	return walletAPIResponse{
		code:    resp.StatusCode,
		balance: payload.Balance,
		message: payload.Error,
		raw:     string(body),
	}, nil
}
