package wallet

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	DepositOrCreate(ctx context.Context, walletID uuid.UUID, amount int64) (int64, error)
	Withdraw(ctx context.Context, walletID uuid.UUID, amount int64) (int64, error)
	GetBalance(ctx context.Context, walletID uuid.UUID) (int64, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) ApplyOperation(ctx context.Context, operation Operation) (Wallet, error) {
	if err := validateOperation(operation); err != nil {
		return Wallet{}, err
	}

	var (
		balance int64
		err     error
	)
	switch operation.OperationType {
	case Deposit:
		balance, err = s.repo.DepositOrCreate(ctx, operation.WalletID, operation.Amount)
	case Withdraw:
		balance, err = s.repo.Withdraw(ctx, operation.WalletID, operation.Amount)
	default:
		return Wallet{}, ErrInvalidOperation
	}
	if err != nil {
		return Wallet{}, err
	}
	return Wallet{ID: operation.WalletID, Balance: balance}, nil
}

func (s *Service) GetWallet(ctx context.Context, walletID uuid.UUID) (Wallet, error) {
	if err := ValidateWalletID(walletID); err != nil {
		return Wallet{}, err
	}
	balance, err := s.repo.GetBalance(ctx, walletID)
	if err != nil {
		return Wallet{}, err
	}
	return Wallet{ID: walletID, Balance: balance}, nil
}
