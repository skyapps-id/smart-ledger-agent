package consumption

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"smart-ledger-agent/internal/domain"
)

// unitToBase menormalkan satuan ke satuan dasar: berat/volume → gr/ml,
// satuan hitung (pcs/buah/...) → ct. ok=false bila satuan tak dikenal.
func unitToBase(u string, q float64) (base string, val float64, ok bool) {
	switch u {
	case "g", "gr", "gram":
		return "gr", q, true
	case "kg", "kilogram":
		return "gr", q * 1000, true
	case "ml", "mililiter":
		return "ml", q, true
	case "l", "lt", "ltr", "liter":
		return "ml", q * 1000, true
	case "pcs", "pc", "buah", "keping", "ct":
		return "ct", q, true
	}
	return u, q, false
}

// sizeInNameRe mencari pola angka + satuan isi berat/volume pada nama barang.
// Alternatif yang lebih panjang (lt/ltr/liter) harus mendahului "l"
// agar tidak terpotong ("15lt" jangan terbaca "15 l").
var sizeInNameRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(ml|mililiter|lt|ltr|l|liter|gr|gram|g|kg|kilogram)`)

// countInNameRe mencari pola angka + satuan hitung (mis. "48pcs").
var countInNameRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(pcs|pc|buah|keping)`)

// trailingNumRe mencari angka telanjang di akhir nama barang ("popok 48").
var trailingNumRe = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*$`)

// extractSizeFromItemName mengambil ukuran isi mentah dari nama barang.
// "le minerale galon 15lt" → (15, "lt"); "susu uht 500ml" → (500, "ml");
// "pampers mamypoko 48pcs" → (48, "pcs"); "pampers mamypoko 48" → (48, "pcs").
func extractSizeFromItemName(itemName string) (float64, string) {
	lower := strings.ToLower(itemName)
	if m := sizeInNameRe.FindStringSubmatch(lower); len(m) >= 3 {
		if qty, err := strconv.ParseFloat(m[1], 64); err == nil {
			return qty, m[2]
		}
	}
	if m := countInNameRe.FindStringSubmatch(lower); len(m) >= 3 {
		if qty, err := strconv.ParseFloat(m[1], 64); err == nil {
			return qty, m[2]
		}
	}
	if m := trailingNumRe.FindStringSubmatch(lower); len(m) >= 2 {
		if qty, err := strconv.ParseFloat(m[1], 64); err == nil {
			return qty, "pcs"
		}
	}
	return 0, ""
}

// convertScale mengonversi pemakaian ke satuan inventory bila keduanya
// se-dimensi (gr↔gr/ml↔ml/ct↔ct) — konversi skala murni (gr→kg, ml→liter).
func convertScale(inv *domain.Inventory, qty float64, unit string) (float64, string, bool) {
	if inv == nil || qty <= 0 || unit == "" {
		return qty, unit, false
	}
	u := strings.ToLower(strings.TrimSpace(unit))
	invUnit := strings.ToLower(strings.TrimSpace(inv.Unit))
	usageBase, usageVal, isUsageBase := unitToBase(u, qty)
	invBase, _, isInvBase := unitToBase(invUnit, 1)
	if !isInvBase || !isUsageBase {
		return qty, unit, false
	}
	switch {
	case invBase == "gr" && usageBase == "gr":
		if invUnit == "kg" {
			return usageVal / 1000, inv.Unit, true
		}
		return usageVal, inv.Unit, true
	case invBase == "ml" && usageBase == "ml":
		if invUnit == "l" || invUnit == "lt" || invUnit == "ltr" || invUnit == "liter" {
			return usageVal / 1000, inv.Unit, true
		}
		return usageVal, inv.Unit, true
	case invBase == "ct" && usageBase == "ct":
		return usageVal, inv.Unit, true
	}
	return qty, unit, false
}

// convertByContent mengonversi pemakaian ke satuan kemasan inventory
// memakai isi per kemasan (contentQty contentUnit, mis. 15 lt per galon).
// ok=false bila dimensi pemakaian dan isi tidak kompatibel.
func convertByContent(inv *domain.Inventory, contentQty float64, contentUnit string, qty float64, unit string) (float64, string, bool) {
	if inv == nil || contentQty <= 0 || contentUnit == "" {
		return qty, unit, false
	}
	u := strings.ToLower(strings.TrimSpace(unit))
	cu := strings.ToLower(strings.TrimSpace(contentUnit))
	usageBase, usageVal, okU := unitToBase(u, qty)
	cBase, cVal, okC := unitToBase(cu, contentQty)
	if !okU || !okC || cVal <= 0 || usageBase != cBase {
		return qty, unit, false
	}
	return usageVal / cVal, inv.Unit, true
}

// contentFromMessage mencari pola "<kemasan> <isi>" pada pesan user
// (mis. "pakai le minerale galon 15lt" atau "1 ball isi 48").
func contentFromMessage(inv *domain.Inventory, message string) (float64, string, bool) {
	if inv == nil || message == "" {
		return 0, "", false
	}
	invUnit := strings.ToLower(strings.TrimSpace(inv.Unit))
	if _, _, isInvBase := unitToBase(invUnit, 1); isInvBase {
		return 0, "", false
	}
	re := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(invUnit) + `(?:\s*isi)?\s*(\d+(?:\.\d+)?)\s*(ml|mililiter|lt|ltr|l|liter|gr|gram|g|kg|kilogram|pcs|pc|buah|keping)?\b`)
	m := re.FindStringSubmatch(strings.ToLower(message))
	if len(m) < 2 {
		return 0, "", false
	}
	contentQty, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, "", false
	}
	contentUnit := m[2]
	if contentUnit == "" {
		contentUnit = "pcs" // angka telanjang setelah kemasan → isi hitung
	}
	return contentQty, contentUnit, true
}

// ResolveUsageConversion mengonversi pemakaian ke satuan inventory dengan
// urutan prioritas: (1) konversi skala se-dimensi, (2) isi tersimpan di
// inventory (dipelajari dari jawaban user), (3) isi dari nama barang,
// (4) pola "<kemasan> <isi>" pada pesan. Mengembalikan learnedQty/learnedUnit
// = isi yang BARU diketahui dari nama/pesan agar caller menyimpannya ke
// inventory (pemakaian berikutnya stabil, tidak bergantung pola pesan).
func ResolveUsageConversion(inv *domain.Inventory, qty float64, unit, message string) (convQty float64, convUnit string, learnedQty float64, learnedUnit string, ok bool) {
	if inv == nil {
		return qty, unit, 0, "", false
	}
	if q, u, okC := convertScale(inv, qty, unit); okC {
		return q, u, 0, "", true
	}
	if q, u, okC := convertByContent(inv, inv.ContentSize, inv.ContentUnit, qty, unit); okC {
		return q, u, 0, "", true
	}
	if perQty, perUnit := extractSizeFromItemName(inv.ItemName); perQty > 0 {
		if q, u, okC := convertByContent(inv, perQty, perUnit, qty, unit); okC {
			return q, u, perQty, perUnit, true
		}
	}
	if contentQty, contentUnit, found := contentFromMessage(inv, message); found {
		if q, u, okC := convertByContent(inv, contentQty, contentUnit, qty, unit); okC {
			return q, u, contentQty, contentUnit, true
		}
	}
	return qty, unit, 0, "", false
}

// ConversionQuestion menyusun pertanyaan faktor konversi bila pemakaian
// (satuan dasar gr/ml/ct) tidak bisa dikonversi ke satuan kemasan inventory
// dan isi per kemasan belum diketahui. String kosong bila tidak relevan.
func ConversionQuestion(inv *domain.Inventory, usageUnit string) string {
	if inv == nil || usageUnit == "" {
		return ""
	}
	invUnit := strings.ToLower(strings.TrimSpace(inv.Unit))
	u := strings.ToLower(strings.TrimSpace(usageUnit))
	if strings.EqualFold(invUnit, u) {
		return ""
	}
	if _, _, isInvBase := unitToBase(invUnit, 1); isInvBase {
		return ""
	}
	if _, _, isUsageBase := unitToBase(u, 1); !isUsageBase {
		return ""
	}
	if inv.ContentSize > 0 && inv.ContentUnit != "" {
		return ""
	}
	return fmt.Sprintf(
		"Stok %s tercatat dalam satuan %s. 1 %s setara berapa %s? Balas dengan angka+satuan (contoh: 15%s)",
		inv.ItemName, invUnit, invUnit, u, u,
	)
}

// FormatQtyForDisplay memformat nilai satuan dasar memakai satuan ASLI yang
// user pakai (dari ukuran di nama barang, atau satuan beli) — bot TIDAK
// mengambil keputusan konversi sepihak. Contoh: item "galon 15lt" → 15000 ml
// ditampilkan "15 lt"; dibeli per "kg" → 1000 gr ditampilkan "1 kg"; tanpa
// info satuan user → satuan dasar apa adanya ("6000 gr"), tidak di-upgrade.
func FormatQtyForDisplay(qtyBase float64, baseUnit, itemName, purchaseUnit string) (string, string) {
	format := func(v float64) string {
		return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
	}
	if _, raw := extractSizeFromItemName(itemName); raw != "" {
		if b, val, ok := unitToBase(raw, 1); ok && b == baseUnit && val > 0 {
			return format(qtyBase/val), raw
		}
	}
	if b, val, ok := unitToBase(strings.ToLower(strings.TrimSpace(purchaseUnit)), 1); ok && b == baseUnit && val > 0 {
		return format(qtyBase/val), strings.TrimSpace(purchaseUnit)
	}
	return format(qtyBase), baseUnit
}

// ConvertToInventoryUnit mengonversi jumlah pemakaian hasil LLM (mis. 200 g
// atau 5 pcs) ke satuan inventory (mis. 1 pcs / 0.1 ball) memakai ukuran isi
// yang tertera pada nama barang.
func ConvertToInventoryUnit(inv *domain.Inventory, qty float64, unit string) (float64, string) {
	if q, u, ok := convertScale(inv, qty, unit); ok {
		return q, u
	}
	if inv != nil {
		if perQty, perUnit := extractSizeFromItemName(inv.ItemName); perQty > 0 {
			if q, u, ok := convertByContent(inv, perQty, perUnit, qty, unit); ok {
				return q, u
			}
		}
	}
	return qty, unit
}

// ConvertToInventoryUnitFromMessage seperti ResolveUsageConversion tetapi
// hanya mengembalikan hasil konversinya (tanpa info learning).
func ConvertToInventoryUnitFromMessage(inv *domain.Inventory, qty float64, unit, message string) (float64, string) {
	q, u, _, _, _ := ResolveUsageConversion(inv, qty, unit, message)
	return q, u
}
