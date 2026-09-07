package transaction

import (
	"context"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/service/agent"
)

// resolveCategory menentukan kategori final transaksi: kategori kanonik di
// master goods MENANG (stabil, tidak bisa digeser LLM per transaksi).
// Bila master belum punya kategori dan LLM mengekstrak satu, kategori itu
// di-seed ke master agar transaksi berikutnya konsisten.
func resolveCategory(ctx context.Context, goodsRepo repository.GoodsRepository, db *gorm.DB, goods *domain.Good, extracted string) string {
	if goods == nil {
		return extracted
	}
	if goods.Category != "" {
		return goods.Category
	}
	if extracted != "" {
		if err := goodsRepo.WithTx(db).UpdateCategory(ctx, goods.ID, extracted); err == nil {
			goods.Category = extracted
		}
	}
	return extracted
}

// ── Date parsing helpers ──

// parseTransactionDate mengubah string tanggal dari ekstraksi LLM ke time.Time.
// Jika string kosong, gunakan waktu saat ini (hari ini).
// Format yang didukung: "YYYY-MM-DD", "DD/MM/YYYY", "DD/MM/YY", "DD/MM", "DD-MM".
func parseTransactionDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		// Jika tidak ada tanggal yang disebutkan, gunakan tanggal hari ini
		return time.Now(), nil
	}

	// Try format YYYY-MM-DD dulu (standard ISO)
	if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
		return parsed, nil
	}

	// Try format DD/MM/YYYY (format Indonesia)
	if parsed, err := time.Parse("02/01/2006", dateStr); err == nil {
		return parsed, nil
	}

	// Try format DD/MM/YY (format pendek dengan 2 digit tahun)
	if parsed, err := time.Parse("02/01/06", dateStr); err == nil {
		// Tambahkan 2000 untuk tahun 2 digit (contoh: 25 -> 2025)
		year := parsed.Year()
		if year < 100 {
			parsed = parsed.AddDate(2000-year, 0, 0)
		}
		return parsed, nil
	}

	// Try format DD/MM (hanya hari dan bulan, tahun di-set ke 2025)
	if parsed, err := time.Parse("02/01", dateStr); err == nil {
		// Set tahun ke 2025
		parsed = time.Date(2025, parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
		return parsed, nil
	}

	// Try format DD-MM (hanya hari dan bulan dengan dash)
	if parsed, err := time.Parse("02-01", dateStr); err == nil {
		// Set tahun ke 2025
		parsed = time.Date(2025, parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
		return parsed, nil
	}

	// Try format DD-MM-YY (dengan 2 digit tahun)
	if parsed, err := time.Parse("02-01-06", dateStr); err == nil {
		year := parsed.Year()
		if year < 100 {
			parsed = parsed.AddDate(2000-year, 0, 0)
		}
		return parsed, nil
	}

	return time.Now(), fmt.Errorf("format tanggal tidak dikenali: %s (gunakan DD/MM atau DD/MM/YYYY)", dateStr)
}

// formatDuration mengubah durasi dalam hari menjadi format yang mudah dibaca
func formatDuration(days float64) string {
	if days < 1 {
		return "< 1 hari"
	}
	if days == 1 {
		return "1 hari"
	}
	if days < 7 {
		return fmt.Sprintf("%.0f hari", days)
	}
	if days < 30 {
		weeks := days / 7
		if weeks == 1 {
			return "1 minggu"
		}
		return fmt.Sprintf("%.0f minggu", weeks)
	}
	months := days / 30
	if months == 1 {
		return "1 bulan"
	}
	return fmt.Sprintf("%.0f bulan", months)
}

// repurchaseAnalysis menyusun kalimat analisa beli ulang untuk item non-stok:
// berapa lama pembelian sebelumnya "bertahan" (jarak ke pembelian baru) dan
// rata-rata belanja per hari. Mengembalikan string kosong bila tidak layak
// ditampilkan (tidak ada pembelian sebelumnya atau jarak < 1 hari).
func repurchaseAnalysis(newTxnDate time.Time, last *domain.Transaction) string {
	if last == nil || last.Amount <= 0 {
		return ""
	}
	days := newTxnDate.Sub(last.TransactionDate).Hours() / 24
	if days < 1 {
		return ""
	}
	avgDaily := last.Amount / days
	return fmt.Sprintf(
		" Analisa beli ulang: %s sebelumnya Rp%s (%s) bertahan %s → rata-rata Rp%s/hari.",
		last.ItemName, agent.FormatRupiah(last.Amount),
		last.TransactionDate.Format("02/01"),
		formatDuration(days), agent.FormatRupiah(math.Round(avgDaily)),
	)
}

// ── Unit parsing helpers ──
