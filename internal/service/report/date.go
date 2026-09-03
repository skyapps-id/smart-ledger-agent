package report

import (
	"fmt"
	"time"
)

// parseLLMDate memparsing tanggal dari LLM dengan berbagai format
// Mendukung: "YYYY-MM-DD", "DD/MM/YYYY", "DD-MM-YYYY", "DD/MM", "DD-MM"
// Hasil selalu awal hari (00:00:00). Untuk batas atas rentang (to_date)
// gunakan ParseLLMDateEnd agar seluruh hari terakhir ikut terhitung.
func parseLLMDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		return time.Now(), nil
	}

	// Try format YYYY-MM-DD dulu (standard ISO)
	if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
		return parsed, nil
	}

	// Try format DD/MM/YYYY (format Indonesia)
	if parsed, err := time.Parse("02/01/2006", dateStr); err == nil {
		return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
	}

	// Try format DD-MM-YYYY
	if parsed, err := time.Parse("02-01-2006", dateStr); err == nil {
		return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
	}

	// Try format DD/MM (hanya hari dan bulan, tahun di-set ke tahun sekarang)
	if parsed, err := time.Parse("02/01", dateStr); err == nil {
		year := time.Now().Year()
		return time.Date(year, parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
	}

	// Try format DD-MM (hanya hari dan bulan dengan dash)
	if parsed, err := time.Parse("02-01", dateStr); err == nil {
		year := time.Now().Year()
		return time.Date(year, parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
	}

	return time.Now(), fmt.Errorf("format tanggal tidak dikenali: %s (gunakan YYYY-MM-DD, DD/MM/YYYY, atau DD/MM)", dateStr)
}

// ParseLLMDateEnd seperti parseLLMDate tapi mengembalikan akhir hari
// (23:59:59) sehingga dipakai sebagai batas atas rentang (to_date).
func ParseLLMDateEnd(dateStr string) (time.Time, error) {
	parsed, err := parseLLMDate(dateStr)
	if err != nil {
		return parsed, err
	}
	return parsed.Add(24*time.Hour - time.Second), nil
}
