package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/google/uuid"

	"go-test-task/internal/wallet"
)

const maxRequestBodyBytes int64 = 4096

type Service interface {
	ApplyOperation(ctx context.Context, operation wallet.Operation) (wallet.Wallet, error)
	GetWallet(ctx context.Context, walletID uuid.UUID) (wallet.Wallet, error)
}

type Handler struct {
	walletService Service
}

func New(walletService Service) *Handler {
	return &Handler{walletService: walletService}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/wallets/{walletID}", h.handleGetBalance)
	mux.HandleFunc("POST /api/v1/wallet", h.handleDepositOrWithdraw)
}

type walletResponse struct {
	WalletID      uuid.UUID `json:"walletId"`
	OperationType string    `json:"operationType,omitempty"`
	Balance       int64     `json:"balance"`
}

func newWalletResponse(walletID uuid.UUID, operationType string, balance int64) walletResponse {
	return walletResponse{
		WalletID:      walletID,
		OperationType: operationType,
		Balance:       balance,
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) handleDepositOrWithdraw(w http.ResponseWriter, r *http.Request) {
	if !contentTypeAllowsJSON(r.Header.Get("Content-Type")) {
		h.writeErrorJSON(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}

	var operation wallet.Operation
	if err := decodeJSON(w, r, &operation); err != nil {
		message := "invalid json"
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			message = "request body is too large"
		}
		h.writeErrorJSON(w, http.StatusUnprocessableEntity, message)
		return
	}

	result, err := h.walletService.ApplyOperation(r.Context(), operation)
	if err != nil {
		h.writeWalletError(w, err)
		return
	}

	h.writeWalletJSON(w, newWalletResponse(result.ID, string(operation.OperationType), result.Balance))
}

func (h *Handler) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	walletID, err := uuid.Parse(r.PathValue("walletID"))
	if err != nil {
		h.writeErrorJSON(w, http.StatusUnprocessableEntity, wallet.ErrInvalidWalletID.Error())
		return
	}
	if err = wallet.ValidateWalletID(walletID); err != nil {
		h.writeWalletError(w, err)
		return
	}

	result, err := h.walletService.GetWallet(r.Context(), walletID)
	if err != nil {
		h.writeWalletError(w, err)
		return
	}

	h.writeWalletJSON(w, newWalletResponse(result.ID, "", result.Balance))
}

func (h *Handler) writeWalletError(w http.ResponseWriter, err error) {
	status, message := statusCodeForWalletError(err)
	if status >= http.StatusInternalServerError {
		slog.Error("wallet request failed", "status", status, "err", err)
	}
	h.writeErrorJSON(w, status, message)
}

func (h *Handler) writeWalletJSON(w http.ResponseWriter, payload walletResponse) {
	if err := writeJSON(w, http.StatusOK, payload); err != nil {
		slog.Error("write response", "err", err)
	}
}

func (h *Handler) writeErrorJSON(w http.ResponseWriter, status int, message string) {
	if err := writeJSON(w, status, errorResponse{Error: message}); err != nil {
		slog.Error("write error response", "err", err)
	}
}

func statusCodeForWalletError(err error) (int, string) {
	switch {
	case errors.Is(err, wallet.ErrInvalidAmount):
		return http.StatusUnprocessableEntity, wallet.ErrInvalidAmount.Error()
	case errors.Is(err, wallet.ErrInvalidOperation):
		return http.StatusUnprocessableEntity, wallet.ErrInvalidOperation.Error()
	case errors.Is(err, wallet.ErrInvalidWalletID):
		return http.StatusUnprocessableEntity, wallet.ErrInvalidWalletID.Error()
	case errors.Is(err, wallet.ErrNotFound):
		return http.StatusNotFound, wallet.ErrNotFound.Error()
	case errors.Is(err, wallet.ErrInsufficientFunds):
		return http.StatusConflict, wallet.ErrInsufficientFunds.Error()
	default:
		return http.StatusServiceUnavailable, "service unavailable"
	}
}

func contentTypeAllowsJSON(header string) bool {
	if strings.TrimSpace(header) == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := jsonv2.UnmarshalRead(r.Body, dest); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err = w.Write(body); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}
