package transaction

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/service/agent"
)

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

// parseConversionInfo mengambil informasi konversi dari notes
// Contoh: "100g per pcs" → (100, "g")
// Contoh: "200ml per botol" → (200, "ml")
// Contoh: "" → (0, "")
func parseConversionInfo(notes string) (float64, string) {
	if notes == "" {
		return 0, ""
	}

	// Cari pattern angka + unit + "per" + unit packaging
	// Menggunakan regex sederhana
	lowerNotes := strings.ToLower(notes)

	// Pattern 1: "100g per pcs", "200ml per botol", "1kg per pack"
	re := `(\d+(?:\.\d+)?)\s*([a-zA-Z]+)\s*per\s*([a-zA-Z]+)`
	matches := regexp.MustCompile(re).FindStringSubmatch(lowerNotes)

	if len(matches) >= 3 {
		// matches[0] = full match
		// matches[1] = number (100, 200, etc)
		// matches[2] = unit (g, ml, kg, etc)
		// matches[3] = packaging unit (pcs, botol, pack, etc)
		quantity, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			unit := matches[2]
			// Normalize unit
			switch unit {
			case "gram", "g":
				unit = "g"
			case "mililiter", "mililitre", "ml":
				unit = "ml"
			case "kilogram", "kg":
				unit = "kg"
			}
			return quantity, unit
		}
	}

	return 0, ""
}

// getConversionFactor menghitung conversion factor dari satuan asli ke satuan terkecil
// Contoh: "500ml" → 500, "1kg" → 1000, "250gr" → 250
func getConversionFactor(originalUnit string) float64 {
	lowerUnit := strings.ToLower(originalUnit)

	switch {
	case strings.Contains(lowerUnit, "ml"):
		// ml adalah satuan terkecil untuk liquid
		if qty := extractQuantityFromUnit(originalUnit); qty > 0 {
			return qty
		}
		return 1.0
	case strings.Contains(lowerUnit, "gr"):
		// gr adalah satuan terkecil untuk solid
		if qty := extractQuantityFromUnit(originalUnit); qty > 0 {
			return qty
		}
		return 1.0
	case strings.Contains(lowerUnit, "kg"):
		return 1000.0 // kg ke gr
	case strings.Contains(lowerUnit, "l"), strings.Contains(lowerUnit, "liter"):
		return 1000.0 // liter ke ml
	default:
		return 1.0
	}
}

// extractQuantityFromUnit mengekstrak quantity dari string unit
// Contoh: "500ml" → 500, "1.5kg" → 1.5
func extractQuantityFromUnit(unitStr string) float64 {
	re := regexp.MustCompile(`(\d+(?:\.\d+)?)`)
	matches := re.FindStringSubmatch(unitStr)

	if len(matches) >= 2 {
		if qty, err := strconv.ParseFloat(matches[1], 64); err == nil {
			return qty
		}
	}

	return 0
}
