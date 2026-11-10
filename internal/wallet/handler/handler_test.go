package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"go-test-task/internal/wallet"
	"go-test-task/internal/wallet/handler"
)

type fakeService struct {
	applyOperation func(context.Context, wallet.Operation) (wallet.Wallet, error)
	getWallet      func(context.Context, uuid.UUID) (wallet.Wallet, error)
}

func (f fakeService) ApplyOperation(ctx context.Context, operation wallet.Operation) (wallet.Wallet, error) {
	return f.applyOperation(ctx, operation)
}

func (f fakeService) GetWallet(ctx context.Context, walletID uuid.UUID) (wallet.Wallet, error) {
	return f.getWallet(ctx, walletID)
}

func TestApply(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	tests := []struct {
		name        string
		body        string
		contentType string
		status      int
		message     string
		balance     int64
		applyErr    error
		wantCalled  bool
	}{
		{
			name:        "deposit",
			body:        `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":1000}`,
			contentType: "application/json",
			status:      http.StatusOK,
			balance:     1000,
			wantCalled:  true,
		},
		{
			name:        "charset",
			body:        `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"WITHDRAW","amount":1}`,
			contentType: "application/json; charset=utf-8",
			status:      http.StatusOK,
			balance:     9,
			wantCalled:  true,
		},
		{
			name:       "missing content type",
			body:       `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":1}`,
			status:     http.StatusOK,
			balance:    1,
			wantCalled: true,
		},
		{
			name:        "wrong content type",
			body:        `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":1}`,
			contentType: "text/plain",
			status:      http.StatusUnsupportedMediaType,
			message:     "content type must be application/json",
		},
		{
			name:        "ignored extra field",
			body:        `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":1,"note":"x"}`,
			contentType: "application/json",
			status:      http.StatusOK,
			balance:     1,
			wantCalled:  true,
		},
		{
			name:        "broken json",
			body:        `{`,
			contentType: "application/json",
			status:      http.StatusUnprocessableEntity,
			message:     "invalid json",
		},
		{
			name:        "fractional amount",
			body:        `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":1000.5}`,
			contentType: "application/json",
			status:      http.StatusUnprocessableEntity,
			message:     "invalid json",
		},
		{
			name:        "not found",
			body:        `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"WITHDRAW","amount":1}`,
			contentType: "application/json",
			status:      http.StatusNotFound,
			message:     "wallet not found",
			applyErr:    wallet.ErrNotFound,
			wantCalled:  true,
		},
		{
			name:        "insufficient funds",
			body:        `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"WITHDRAW","amount":5}`,
			contentType: "application/json",
			status:      http.StatusConflict,
			message:     "insufficient funds",
			applyErr:    wallet.ErrInsufficientFunds,
			wantCalled:  true,
		},
		{
			name:        "database down",
			body:        `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":1}`,
			contentType: "application/json",
			status:      http.StatusServiceUnavailable,
			message:     "service unavailable",
			applyErr:    errors.New("connection refused"),
			wantCalled:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			called := false
			svc := fakeService{
				applyOperation: func(_ context.Context, operation wallet.Operation) (wallet.Wallet, error) {
					called = true
					if tc.applyErr != nil {
						return wallet.Wallet{}, tc.applyErr
					}
					return wallet.Wallet{ID: operation.WalletID, Balance: tc.balance}, nil
				},
				getWallet: func(context.Context, uuid.UUID) (wallet.Wallet, error) {
					require.FailNow(t, "get was called")
					return wallet.Wallet{}, nil
				},
			}

			rec := sendHandlerRequest(t, svc, http.MethodPost, "/api/v1/wallet", tc.contentType, tc.body)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Equal(t, tc.wantCalled, called)

			var payload struct {
				WalletID      string `json:"walletId"`
				OperationType string `json:"operationType"`
				Balance       int64  `json:"balance"`
				Error         string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
			if tc.message != "" {
				require.Equal(t, tc.message, payload.Error)
			}
			if tc.status == http.StatusOK {
				require.Equal(t, id.String(), payload.WalletID)
				require.Equal(t, tc.balance, payload.Balance)
				require.NotEmpty(t, payload.OperationType)
				require.NotContains(t, rec.Body.String(), "valletId")
			}
		})
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()

	svc := wallet.NewService(rejectingRepo{})
	tests := []struct {
		name    string
		body    string
		message string
	}{
		{
			name:    "nil wallet",
			body:    `{"walletId":"00000000-0000-0000-0000-000000000000","operationType":"DEPOSIT","amount":1}`,
			message: "invalid wallet id",
		},
		{
			name:    "valletId is not accepted",
			body:    `{"valletId":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":1}`,
			message: "invalid wallet id",
		},
		{
			name:    "wallet id case is exact",
			body:    `{"walletid":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":1}`,
			message: "invalid wallet id",
		},
		{
			name:    "operation type key is exact",
			body:    `{"walletId":"11111111-1111-1111-1111-111111111111","operation Type":"DEPOSIT","amount":1}`,
			message: "invalid operation type",
		},
		{
			name:    "bad operation",
			body:    `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"TRANSFER","amount":1}`,
			message: "invalid operation type",
		},
		{
			name:    "zero amount",
			body:    `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"DEPOSIT","amount":0}`,
			message: "invalid amount",
		},
		{
			name:    "negative amount",
			body:    `{"walletId":"11111111-1111-1111-1111-111111111111","operationType":"WITHDRAW","amount":-5}`,
			message: "invalid amount",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := sendHandlerRequest(t, svc, http.MethodPost, "/api/v1/wallet", "application/json", tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.message)
		})
	}
}

type rejectingRepo struct{}

func (rejectingRepo) DepositOrCreate(context.Context, uuid.UUID, int64) (int64, error) {
	return 0, errors.New("repository should not be called")
}

func (rejectingRepo) Withdraw(context.Context, uuid.UUID, int64) (int64, error) {
	return 0, errors.New("repository should not be called")
}

func (rejectingRepo) GetBalance(context.Context, uuid.UUID) (int64, error) {
	return 0, errors.New("repository should not be called")
}

func TestGet(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	svc := fakeService{
		applyOperation: func(context.Context, wallet.Operation) (wallet.Wallet, error) {
			require.FailNow(t, "apply was called")
			return wallet.Wallet{}, nil
		},
		getWallet: func(_ context.Context, got uuid.UUID) (wallet.Wallet, error) {
			require.Equal(t, id, got)
			return wallet.Wallet{ID: got, Balance: 0}, nil
		},
	}

	rec := sendHandlerRequest(t, svc, http.MethodGet, "/api/v1/wallets/"+id.String(), "", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"balance":0`)

	missing := fakeService{
		getWallet: func(context.Context, uuid.UUID) (wallet.Wallet, error) {
			return wallet.Wallet{}, wallet.ErrNotFound
		},
	}
	rec = sendHandlerRequest(t, missing, http.MethodGet, "/api/v1/wallets/"+id.String(), "", "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = sendHandlerRequest(t, svc, http.MethodGet, "/api/v1/wallets/not-a-uuid", "", "")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = sendHandlerRequest(t, svc, http.MethodGet, "/api/v1/wallets/"+uuid.Nil.String(), "", "")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestBodyTooLarge(t *testing.T) {
	t.Parallel()

	svc := fakeService{
		applyOperation: func(context.Context, wallet.Operation) (wallet.Wallet, error) {
			require.FailNow(t, "apply was called")
			return wallet.Wallet{}, nil
		},
	}
	body := `{"operationType":"` + strings.Repeat("A", 5000) + `","walletId":"11111111-1111-1111-1111-111111111111","amount":1}`
	rec := sendHandlerRequest(t, svc, http.MethodPost, "/api/v1/wallet", "application/json", body)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "request body is too large")
}

func sendHandlerRequest(t *testing.T, svc handler.Service, method, path, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	handler.New(svc).Register(mux)
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}
