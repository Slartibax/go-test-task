package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-test-task/internal/wallet"
)

const (
	pgNumericOverflow = "22003"
	pgCheckViolation  = "23514"
)

const (
	depositSQL = `
INSERT INTO wallets (id, balance, created_at)
VALUES ($1, $2, now())
ON CONFLICT (id) DO UPDATE
SET balance = wallets.balance + EXCLUDED.balance,
    updated_at = now()
RETURNING balance`

	withdrawSQL = `
WITH updated AS (
    UPDATE wallets
    SET balance = balance - $2,
        updated_at = now()
    WHERE id = $1 AND balance >= $2
    RETURNING balance
)
SELECT
    EXISTS (SELECT 1 FROM wallets WHERE id = $1),
    (SELECT balance FROM updated)`

	balanceSQL = `SELECT balance FROM wallets WHERE id = $1`
)

type Repository struct {
	pool *pgxpool.Pool
}

var _ wallet.Repository = (*Repository)(nil)

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) DepositOrCreate(ctx context.Context, walletID uuid.UUID, amount int64) (int64, error) {
	return r.scanUpdatedBalance(ctx, depositSQL, walletID, amount, "deposit")
}

func (r *Repository) Withdraw(ctx context.Context, walletID uuid.UUID, amount int64) (int64, error) {
	var walletRowExists bool
	var updatedBalance *int64
	err := r.pool.QueryRow(ctx, withdrawSQL, walletID, amount).Scan(&walletRowExists, &updatedBalance)
	if err != nil {
		return 0, wrapPostgresWriteError("withdraw", err)
	}
	if updatedBalance != nil {
		return *updatedBalance, nil
	}
	if !walletRowExists {
		return 0, wallet.ErrNotFound
	}
	return 0, wallet.ErrInsufficientFunds
}

func (r *Repository) GetBalance(ctx context.Context, walletID uuid.UUID) (int64, error) {
	var balance int64
	err := r.pool.QueryRow(ctx, balanceSQL, walletID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, wallet.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("select balance: %w", err)
	}
	return balance, nil
}

func (r *Repository) scanUpdatedBalance(ctx context.Context, query string, walletID uuid.UUID, amount int64, operationName string) (int64, error) {
	var balance int64
	err := r.pool.QueryRow(ctx, query, walletID, amount).Scan(&balance)
	if err != nil {
		return 0, wrapPostgresWriteError(operationName, err)
	}
	return balance, nil
}

func wrapPostgresWriteError(operationName string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if ok {
		switch pgErr.Code {
		case pgNumericOverflow:
			return fmt.Errorf("%w: %s", wallet.ErrInvalidAmount, pgErr.Message)
		case pgCheckViolation:
			return fmt.Errorf("%w: %s", wallet.ErrInsufficientFunds, pgErr.Message)
		}
	}
	return fmt.Errorf("%s balance: %w", operationName, err)
}
