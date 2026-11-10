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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"go-test-task/internal/wallet"
	"go-test-task/internal/wallet/handler"
)

type memRepo struct {
	mu       sync.Mutex
	balances map[uuid.UUID]int64
}

func newMemRepo() *memRepo {
	return &memRepo{balances: make(map[uuid.UUID]int64)}
}

func (m *memRepo) DepositOrCreate(_ context.Context, walletID uuid.UUID, amount int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balances[walletID] += amount
	return m.balances[walletID], nil
}

func (m *memRepo) Withdraw(_ context.Context, walletID uuid.UUID, amount int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	balance, ok := m.balances[walletID]
	if !ok {
		return 0, wallet.ErrNotFound
	}
	if balance < amount {
		return 0, wallet.ErrInsufficientFunds
	}
	m.balances[walletID] = balance - amount
	return m.balances[walletID], nil
}

func (m *memRepo) GetBalance(_ context.Context, walletID uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	balance, ok := m.balances[walletID]
	if !ok {
		return 0, wallet.ErrNotFound
	}
	return balance, nil
}

func TestConcurrentRequests(t *testing.T) {
	repo := newMemRepo()
	mux := http.NewServeMux()
	handler.New(wallet.NewService(repo)).Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        128,
			MaxIdleConnsPerHost: 128,
		},
	}
	t.Cleanup(client.CloseIdleConnections)

	id := uuid.New()
	const (
		n         = 1000
		seed      = 500_000
		operation = 100
	)
	_, err := repo.DepositOrCreate(t.Context(), id, seed)
	require.NoError(t, err)

	codes := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			op := "DEPOSIT"
			if i%2 == 0 {
				op = "WITHDRAW"
			}
			code, postErr := postOperation(t.Context(), client, server.URL, id, op, operation)
			codes[i] = code
			errs[i] = postErr
		}(i)
	}
	wg.Wait()

	for i := range n {
		require.NoError(t, errs[i])
		require.Equal(t, http.StatusOK, codes[i])
	}

	final, err := getBalance(t.Context(), client, server.URL, id)
	require.NoError(t, err)
	require.Equal(t, int64(seed), final)
}

func postOperation(ctx context.Context, client *http.Client, base string, id uuid.UUID, operation string, amount int64) (int, error) {
	body, err := json.Marshal(map[string]any{
		"walletId":      id.String(),
		"operationType": operation,
		"amount":        amount,
	})
	if err != nil {
		return 0, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/wallet", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("do request: %w", err)
	}
	_, copyErr := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()
	if copyErr != nil || closeErr != nil {
		return resp.StatusCode, errors.Join(copyErr, closeErr)
	}
	return resp.StatusCode, nil
}

func getBalance(ctx context.Context, client *http.Client, base string, id uuid.UUID) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/wallets/"+id.String(), http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("do request: %w", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return 0, fmt.Errorf("read response: %w", readErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close response: %w", closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		Balance int64 `json:"balance"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	return payload.Balance, nil
}
