package wallet

import (
	"errors"

	"github.com/google/uuid"
)

type OperationType string

const (
	Deposit  OperationType = "DEPOSIT"
	Withdraw OperationType = "WITHDRAW"
)

type Wallet struct {
	ID      uuid.UUID
	Balance int64
}

type Operation struct {
	WalletID      uuid.UUID     `json:"walletId" validate:"uuid_nz"`
	OperationType OperationType `json:"operationType" validate:"required,oneof=DEPOSIT WITHDRAW"`
	Amount        int64         `json:"amount" validate:"required,gt=0"`
}

var (
	ErrNotFound          = errors.New("wallet not found")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrInvalidAmount     = errors.New("invalid amount")
	ErrInvalidOperation  = errors.New("invalid operation type")
	ErrInvalidWalletID   = errors.New("invalid wallet id")
)
