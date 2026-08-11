package repository

import (
	"context"
	"time"

	"smart-ledger-agent/internal/domain"
	repomodel "smart-ledger-agent/internal/repository/model"
)

// Summary mengagregasi transaksi pada chat (ledger) dalam rentang [from, to].
// Mengembalikan total income, total expense, jumlah transaksi, dan
// rincian pengeluaran per kategori.
func (r *transactionRepo) Summary(ctx context.Context, chatID string, from, to time.Time) (*repomodel.TxnSummary, error) {
	s := &repomodel.TxnSummary{ByCategory: map[string]float64{}}

	type row struct {
		Type     domain.TransactionType
		Category string
		Total    float64
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&domain.Transaction{}).
		Select("type, category, COALESCE(SUM(amount), 0) as total").
		Where("chat_id = ? AND created_at >= ? AND created_at <= ?", chatID, from, to).
		Group("type, category").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, rw := range rows {
		switch rw.Type {
		case domain.TransactionIncome:
			// Saldo awal dipisahkan dari pemasukan riil; keduanya tetap
			// berkontribusi ke Selisih (lihat formatTxnReport).
			if rw.Category == domain.CategoryOpeningBalance {
				s.OpeningBalance += rw.Total
				continue
			}
			s.Income += rw.Total
		case domain.TransactionExpense:
			s.Expense += rw.Total
			if rw.Category != "" {
				s.ByCategory[rw.Category] += rw.Total
			}
		}
	}

	// Hitung total baris transaksi (bukan jumlah group kategori).
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&domain.Transaction{}).
		Where("chat_id = ? AND created_at >= ? AND created_at <= ?", chatID, from, to).
		Count(&count).Error; err != nil {
		return nil, err
	}
	s.Count = count

	return s, nil
}

// ExpenseByItem mengagregasi pengeluaran per nama barang pada chat (ledger)
// dalam rentang [from, to], diurutkan dari nominal terbesar.
func (r *transactionRepo) ExpenseByItem(ctx context.Context, chatID string, from, to time.Time) ([]repomodel.ItemBreakdown, error) {
	var rows []repomodel.ItemBreakdown
	err := r.db.WithContext(ctx).
		Model(&domain.Transaction{}).
		Select("item_name, SUM(amount) as amount, COUNT(*) as count").
		Where("chat_id = ? AND type = ? AND created_at >= ? AND created_at <= ?",
			chatID, domain.TransactionExpense, from, to).
		Group("item_name").
		Order("amount DESC").
		Scan(&rows).Error
	return rows, err
}

// CountByChat menghitung total baris transaksi (semua tipe) pada sebuah chat.
// Dipakai command `info` untuk diagnostic.
func (r *transactionRepo) CountByChat(ctx context.Context, chatID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.Transaction{}).
		Where("chat_id = ?", chatID).
		Count(&count).Error
	return count, err
}
