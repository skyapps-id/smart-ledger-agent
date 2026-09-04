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
	LastExpenseByGoods(ctx context.Context, chatID string, goodsID int64, excludeID int64, onOrBefore time.Time) (*domain.Transaction, error)
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

// LastExpenseByGoods mencari pengeluaran terakhir untuk barang yang sama
// (relasi goods_id) pada chat sebelum (atau pada) tanggal tertentu. Dipakai
// untuk analisa beli ulang item non-stok (mis. token listrik): durasi antar
// pembelian dan rata-rata belanja per hari.
func (r *transactionRepo) LastExpenseByGoods(ctx context.Context, chatID string, goodsID int64, excludeID int64, onOrBefore time.Time) (*domain.Transaction, error) {
	var txn domain.Transaction
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND type = ? AND amount > 0 AND goods_id = ? AND goods_id <> 0 AND id <> ? AND transaction_date <= ?",
			chatID, domain.TransactionExpense, goodsID, excludeID, onOrBefore).
		Order("transaction_date DESC, created_at DESC, id DESC").
		First(&txn).Error
	if err != nil {
		return nil, err
	}
	return &txn, nil
}
