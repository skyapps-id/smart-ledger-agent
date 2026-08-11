// Package repository mengisolasi akses data (persistence layer).
// Setiap repository menerima *gorm.DB (bisa berupa transaksi) via WithTx
// sehingga beberapa operasi dapat digabung dalam satu DB transaction (ACID).
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	repomodel "smart-ledger-agent/internal/repository/model"
)

// TransactionRepository abstraksi penyimpanan transaksi keuangan.
type TransactionRepository interface {
	WithTx(tx *gorm.DB) TransactionRepository
	Create(ctx context.Context, t *domain.Transaction) error
	Summary(ctx context.Context, chatID string, from, to time.Time) (*repomodel.TxnSummary, error)
	ExpenseByItem(ctx context.Context, chatID string, from, to time.Time) ([]repomodel.ItemBreakdown, error)
	CountByChat(ctx context.Context, chatID string) (int64, error)
}

type transactionRepo struct{ db *gorm.DB }

func NewTransactionRepository(db *gorm.DB) TransactionRepository {
	return &transactionRepo{db: db}
}

func (r *transactionRepo) WithTx(tx *gorm.DB) TransactionRepository {
	return &transactionRepo{db: tx}
}

func (r *transactionRepo) Create(ctx context.Context, t *domain.Transaction) error {
	return r.db.WithContext(ctx).Create(t).Error
}
