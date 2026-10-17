package transaction

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"smart-ledger-agent/internal/domain"
)

func TestRepurchaseAnalysis(t *testing.T) {
	base := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	t.Run("tanpa pembelian sebelumnya", func(t *testing.T) {
		assert.Empty(t, repurchaseAnalysis(base, nil))
	})

	t.Run("jarak kurang dari 1 hari", func(t *testing.T) {
		last := &domain.Transaction{
			ItemName:        "token listrik",
			Amount:          200000,
			TransactionDate: base.Add(-12 * time.Hour),
		}
		assert.Empty(t, repurchaseAnalysis(base, last))
	})

	t.Run("jarak 30 hari", func(t *testing.T) {
		last := &domain.Transaction{
			ItemName:        "token listrik",
			Amount:          200000,
			TransactionDate: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		}
		got := repurchaseAnalysis(base, last)
		assert.Contains(t, got, "token listrik sebelumnya Rp200.000 (11/08)")
		assert.Contains(t, got, "bertahan 1 bulan")
		assert.Contains(t, got, "rata-rata Rp6.667/hari")
	})

	t.Run("jarak 6 hari", func(t *testing.T) {
		last := &domain.Transaction{
			ItemName:        "pulsa",
			Amount:          50000,
			TransactionDate: base.AddDate(0, 0, -6),
		}
		got := repurchaseAnalysis(base, last)
		assert.True(t, strings.Contains(got, "bertahan 6 hari"))
		assert.Contains(t, got, "rata-rata Rp8.333/hari")
	})
}

// TestParseTransactionDateShortFormatUsesCurrentYear: tanggal tanpa tahun
// ("25/08", "25-08") wajib jatuh di tahun berjalan, bukan tahun hardcode.
func TestParseTransactionDateShortFormatUsesCurrentYear(t *testing.T) {
	year := time.Now().Year()
	for _, in := range []string{"25/08", "25-08"} {
		got, err := parseTransactionDate(in)
		assert.NoError(t, err, in)
		assert.Equal(t, time.Date(year, 8, 25, 0, 0, 0, 0, time.UTC), got, in)
	}

	got, err := parseTransactionDate("2024-08-25")
	assert.NoError(t, err)
	assert.Equal(t, 2024, got.Year(), "tahun eksplisit tetap dipakai")
}
