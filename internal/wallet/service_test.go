package wallet_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"go-test-task/internal/wallet"
)

type fakeRepo struct {
	balance      int64
	err          error
	deposits     int
	withdrawals  int
	balanceReads int
}

func (f *fakeRepo) DepositOrCreate(_ context.Context, _ uuid.UUID, amount int64) (int64, error) {
	f.deposits++
	if f.err != nil {
		return 0, f.err
	}
	f.balance += amount
	return f.balance, nil
}

func (f *fakeRepo) Withdraw(_ context.Context, _ uuid.UUID, amount int64) (int64, error) {
	f.withdrawals++
	if f.err != nil {
		return 0, f.err
	}
	f.balance -= amount
	return f.balance, nil
}

func (f *fakeRepo) GetBalance(context.Context, uuid.UUID) (int64, error) {
	f.balanceReads++
	if f.err != nil {
		return 0, f.err
	}
	return f.balance, nil
}

func TestApply(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

	tests := []struct {
		name      string
		op        wallet.Operation
		repoErr   error
		wantErr   error
		wantBal   int64
		wantCalls int
		startBal  int64
	}{
		{
			name:      "deposit",
			op:        wallet.Operation{WalletID: id, OperationType: wallet.Deposit, Amount: 40},
			wantBal:   40,
			wantCalls: 1,
		},
		{
			name:      "withdraw",
			op:        wallet.Operation{WalletID: id, OperationType: wallet.Withdraw, Amount: 15},
			startBal:  20,
			wantBal:   5,
			wantCalls: 1,
		},
		{
			name:    "nil wallet",
			op:      wallet.Operation{OperationType: wallet.Deposit, Amount: 1},
			wantErr: wallet.ErrInvalidWalletID,
		},
		{
			name:    "zero amount",
			op:      wallet.Operation{WalletID: id, OperationType: wallet.Deposit, Amount: 0},
			wantErr: wallet.ErrInvalidAmount,
		},
		{
			name:    "negative amount",
			op:      wallet.Operation{WalletID: id, OperationType: wallet.Withdraw, Amount: -5},
			wantErr: wallet.ErrInvalidAmount,
		},
		{
			name:    "unknown operation",
			op:      wallet.Operation{WalletID: id, OperationType: "TRANSFER", Amount: 1},
			wantErr: wallet.ErrInvalidOperation,
		},
		{
			name:    "insufficient funds",
			op:      wallet.Operation{WalletID: id, OperationType: wallet.Withdraw, Amount: 1},
			repoErr: wallet.ErrInsufficientFunds,
			wantErr: wallet.ErrInsufficientFunds,
		},
		{
			name:    "missing wallet",
			op:      wallet.Operation{WalletID: id, OperationType: wallet.Withdraw, Amount: 1},
			repoErr: wallet.ErrNotFound,
			wantErr: wallet.ErrNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := &fakeRepo{balance: tc.startBal, err: tc.repoErr}
			svc := wallet.NewService(repo)
			got, err := svc.ApplyOperation(t.Context(), tc.op)
			require.ErrorIs(t, err, tc.wantErr)
			calls := repo.deposits + repo.withdrawals
			if tc.wantErr != nil {
				if tc.repoErr == nil {
					require.Zero(t, calls)
				} else {
					require.Equal(t, 1, calls)
				}
				return
			}
			require.Equal(t, id, got.ID)
			require.Equal(t, tc.wantBal, got.Balance)
			require.Equal(t, tc.wantCalls, calls)
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	repo := &fakeRepo{balance: 12}
	svc := wallet.NewService(repo)

	got, err := svc.GetWallet(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)
	require.Equal(t, int64(12), got.Balance)
	require.Equal(t, 1, repo.balanceReads)

	_, err = svc.GetWallet(t.Context(), uuid.Nil)
	require.ErrorIs(t, err, wallet.ErrInvalidWalletID)
	require.Equal(t, 1, repo.balanceReads)

	repo.err = wallet.ErrNotFound
	_, err = svc.GetWallet(t.Context(), id)
	require.ErrorIs(t, err, wallet.ErrNotFound)
}
