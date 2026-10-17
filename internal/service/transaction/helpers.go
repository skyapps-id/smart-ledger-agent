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

// unitPrice menghitung harga beli satuan (amount/qty); 0 bila tidak bisa
// dihitung (amount 0 atau qty 0) — caller fallback ke tampilan total.
func unitPrice(amount, qty float64) float64 {
	if amount > 0 && qty > 0 {
		return amount / qty
	}
	return 0
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

	// Try format DD/MM (hanya hari dan bulan → tahun berjalan, sama dengan
	// aturan prompt ekstraksi)
	if parsed, err := time.Parse("02/01", dateStr); err == nil {
		return time.Date(time.Now().Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
	}

	// Try format DD-MM (hanya hari dan bulan dengan dash → tahun berjalan)
	if parsed, err := time.Parse("02-01", dateStr); err == nil {
		return time.Date(time.Now().Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
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

// categoryLine menyusun baris kategori balasan ("\n🏷️ SEMBAKO"); kosong
// bila kategori belum ada agar tidak muncul baris label tanpa isi.
func categoryLine(category string) string {
	if category == "" {
		return ""
	}
	return "\n🏷️ " + category
}

// previousPriceComparison menyusun baris pembanding harga beli sebelumnya
// untuk barang stok, mis. "\n📈 Harga sebelumnya: Rp95.000/karung (02/09) →
// naik Rp5.000". Selisih hanya dihitung bila kedua harga satuan tersedia
// dengan satuan sama; selain itu tampilkan harga sebelumnya apa adanya.
// Kosong bila belum ada pembelian sebelumnya.
func previousPriceComparison(newUnitPrice float64, unit string, last *domain.Transaction) string {
	if last == nil || last.Amount <= 0 {
		return ""
	}
	date := last.TransactionDate.Format("02/01")
	if last.UnitPrice <= 0 || last.Unit == "" {
		return fmt.Sprintf("\n💲 Harga sebelumnya: Rp%s (%s)", agent.FormatRupiah(last.Amount), date)
	}
	prev := fmt.Sprintf("Harga sebelumnya: Rp%s/%s (%s)", agent.FormatRupiah(last.UnitPrice), last.Unit, date)
	if newUnitPrice <= 0 || last.Unit != unit {
		return "\n💲 " + prev
	}
	diff := math.Round(newUnitPrice - last.UnitPrice)
	switch {
	case diff > 0:
		return fmt.Sprintf("\n📈 %s → naik Rp%s", prev, agent.FormatRupiah(diff))
	case diff < 0:
		return fmt.Sprintf("\n📉 %s → turun Rp%s", prev, agent.FormatRupiah(-diff))
	default:
		return "\n💲 " + prev + " → harga sama"
	}
}

// repurchaseAnalysis menyusun kalimat analisa beli ulang untuk item non-stok
// (barang stok dianalisa modul consumption, bukan di sini):
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
	detail := fmt.Sprintf("Rp%s (%s)", agent.FormatRupiah(last.Amount), last.TransactionDate.Format("02/01"))
	if last.UnitPrice > 0 && last.Quantity > 0 && last.Unit != "" {
		detail = fmt.Sprintf("Rp%s/%s x%g = Rp%s (%s)",
			agent.FormatRupiah(last.UnitPrice), last.Unit, last.Quantity,
			agent.FormatRupiah(last.Amount), last.TransactionDate.Format("02/01"))
	}
	return fmt.Sprintf(
		"\n🔁 *Analisa beli ulang*\nSebelumnya: %s\nBertahan %s → rata-rata *Rp%s/hari*",
		detail, formatDuration(days), agent.FormatRupiah(math.Round(avgDaily)),
	)
}

// ── Unit parsing helpers ──
