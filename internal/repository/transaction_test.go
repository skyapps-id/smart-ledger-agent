package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
)

func setupTxnTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Transaction{}))
	return db
}

func TestLastExpenseByItem(t *testing.T) {
	db := setupTxnTestDB(t)
	repo := NewTransactionRepository(db)
	ctx := context.Background()

	older := &domain.Transaction{
		ChatID: "c1", Type: domain.TransactionExpense, ItemName: "token listrik",
		Amount: 100000, TransactionDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
	prev := &domain.Transaction{
		ChatID: "c1", Type: domain.TransactionExpense, ItemName: "Token Listrik",
		Amount: 200000, TransactionDate: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
	}
	otherItem := &domain.Transaction{
		ChatID: "c1", Type: domain.TransactionExpense, ItemName: "pulsa",
		Amount: 50000, TransactionDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	otherChat := &domain.Transaction{
		ChatID: "c2", Type: domain.TransactionExpense, ItemName: "token listrik",
		Amount: 300000, TransactionDate: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
	}
	for _, txn := range []*domain.Transaction{older, prev, otherItem, otherChat} {
		require.NoError(t, repo.Create(ctx, txn))
	}

	current := &domain.Transaction{
		ChatID: "c1", Type: domain.TransactionExpense, ItemName: "token listrik",
		Amount: 200000, TransactionDate: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, repo.Create(ctx, current))

	got, err := repo.LastExpenseByItem(ctx, "c1", "token listrik", current.ID, current.TransactionDate)
	require.NoError(t, err)
	assert.Equal(t, prev.ID, got.ID)
	assert.Equal(t, float64(200000), got.Amount)
	assert.WithinDuration(t, prev.TransactionDate, got.TransactionDate, 0)

	_, err = repo.LastExpenseByItem(ctx, "c1", "wifi", current.ID, current.TransactionDate)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	t.Run("tidak mengambil transaksi setelah tanggal acuan", func(t *testing.T) {
		got, err := repo.LastExpenseByItem(ctx, "c1", "token listrik", current.ID, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
		require.NoError(t, err)
		assert.Equal(t, older.ID, got.ID)
	})
}
