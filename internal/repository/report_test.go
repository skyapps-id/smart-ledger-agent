package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"smart-ledger-agent/internal/domain"
)

// TestReportUsesTransactionDate memastikan laporan difilter berdasarkan
// transaction_date (tanggal transaksi), bukan created_at (waktu pencatatan)
// — transaksi backdate "31 agustus" harus muncul di laporan bulan Agustus
// meski dicatat bulan berikutnya.
func TestReportUsesTransactionDate(t *testing.T) {
	db := setupTxnTestDB(t)
	repo := NewTransactionRepository(db)
	ctx := context.Background()

	g := seedTxnGood(t, db, "PEPMPES", "pepmpes isi 48")

	// Dicatat 7 Sep, tapi transaksinya 31 Agustus.
	backdated := &domain.Transaction{
		ChatID:          "c1",
		Type:            domain.TransactionExpense,
		Category:        "PERLENGKAPAN BAYI",
		GoodsID:         g.ID,
		ItemName:        g.Name,
		Amount:          192000,
		Quantity:        3,
		Unit:            "ball",
		UnitPrice:       64000,
		TransactionDate: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
		CreatedAt:       time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC),
	}
	require.NoError(t, db.Create(backdated).Error)

	augFrom := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	augTo := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)

	summary, err := repo.Summary(ctx, "c1", augFrom, augTo)
	require.NoError(t, err)
	assert.Equal(t, float64(192000), summary.Expense)
	assert.Equal(t, int64(1), summary.Count)

	items, err := repo.ExpenseByItem(ctx, "c1", augFrom, augTo)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "pepmpes isi 48", items[0].ItemName)
	assert.Equal(t, float64(192000), items[0].Amount)

	// Bulan September (rentang created_at) tidak boleh memuat transaksi ini.
	sepFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sepTo := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	summary, err = repo.Summary(ctx, "c1", sepFrom, sepTo)
	require.NoError(t, err)
	assert.Equal(t, float64(0), summary.Expense)
	assert.Equal(t, int64(0), summary.Count)
}
