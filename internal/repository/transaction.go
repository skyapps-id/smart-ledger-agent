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
	LastExpenseByItem(ctx context.Context, chatID, itemName string, excludeID int64, onOrBefore time.Time) (*domain.Transaction, error)
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

// LastExpenseByItem mencari pengeluaran terakhir dengan nama barang sama
// pada chat sebelum (atau pada) tanggal tertentu. Dipakai untuk analisa
// beli ulang item non-stok (mis. token listrik): durasi antar pembelian
// dan rata-rata belanja per hari.
func (r *transactionRepo) LastExpenseByItem(ctx context.Context, chatID, itemName string, excludeID int64, onOrBefore time.Time) (*domain.Transaction, error) {
	var txn domain.Transaction
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND type = ? AND amount > 0 AND LOWER(item_name) = LOWER(?) AND id <> ? AND transaction_date <= ?",
			chatID, domain.TransactionExpense, itemName, excludeID, onOrBefore).
		Order("transaction_date DESC, created_at DESC, id DESC").
		First(&txn).Error
	if err != nil {
		return nil, err
	}
	return &txn, nil
}
