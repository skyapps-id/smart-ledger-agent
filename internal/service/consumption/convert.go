package consumption

import (
	"math"
	"strconv"
	"strings"

	"smart-ledger-agent/internal/domain"
)

// unitAliases menyamaratakan ejaan satuan umum (murni alias teks, tanpa
// matematika konversi apa pun).
var unitAliases = map[string]string{
	"l": "lt", "ltr": "lt", "liter": "lt",
	"mililiter": "ml",
	"gram":      "gr",
	"kilogram":  "kg",
	"pc":        "pcs", "buah": "pcs", "keping": "pcs",
}

// NormalizeUnit menyamakan ejaan satuan (lowercase + alias).
func NormalizeUnit(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	if alias, ok := unitAliases[u]; ok {
		return alias
	}
	return u
}

// ConvertUsage mengonversi jumlah pemakaian ke SATUAN STOK, HANYA dari
// faktor resmi master goods — tanpa heuristik/nama barang/penalaran:
//  1. satuan pemakaian == satuan stok  → jumlah apa adanya;
//  2. satuan pemakaian == ConversionUom → jumlah / FactorUom (mis. 3 lt
//     pada galon 15 lt → 0.2 galon);
//  3. selain itu → ok=false: satuan tidak dikenal, caller memberi tahu
//     user satuan yang diterima (lihat UsageUnitHint).
func ConvertUsage(inv *domain.Inventory, qty float64, unit string) (float64, string, bool) {
	if inv == nil || qty <= 0 || unit == "" {
		return qty, unit, false
	}
	u := NormalizeUnit(unit)
	if u == NormalizeUnit(inv.Unit) {
		return qty, inv.Unit, true
	}
	if inv.Good != nil && inv.Good.FactorUom > 0 && inv.Good.ConversionUom != "" &&
		u == NormalizeUnit(inv.Good.ConversionUom) {
		return qty / inv.Good.FactorUom, inv.Unit, true
	}
	return qty, unit, false
}

// UsageUnitHint merangkai satuan yang diterima untuk pemakaian barang ini,
// mis. "galon atau lt" — dipakai pesan bimbingan saat satuan tak dikenal.
func UsageUnitHint(inv *domain.Inventory) string {
	if inv == nil {
		return ""
	}
	if inv.Good != nil && inv.Good.ConversionUom != "" && inv.Good.FactorUom > 0 {
		return NormalizeUnit(inv.Unit) + " atau " + NormalizeUnit(inv.Good.ConversionUom)
	}
	return NormalizeUnit(inv.Unit)
}

// normalizeDefaultUnit mengganti satuan "pcs" bawaan LLM dengan satuan
// stok (dari master goods) BILA pesan user tidak benar-benar menyebut
// "pcs" — "pakai galon" berarti 1 galon, bukan 1 pcs. Sebutan eksplisit
// ("popok 48pcs") tetap pcs literal (dikonversi via faktor master).
func normalizeDefaultUnit(msgText, usageUnit, invUnit string) string {
	if usageUnit == "pcs" && invUnit != "" && invUnit != "pcs" &&
		!strings.Contains(strings.ToLower(msgText), "pcs") {
		return invUnit
	}
	return usageUnit
}

// formatQty memformat angka + satuan untuk display — nilai apa adanya
// (tanpa konversi), dibulatkan 2 desimal.
func formatQty(qty float64, unit string) (string, string) {
	v := strconv.FormatFloat(math.Round(qty*100)/100, 'f', -1, 64)
	return v, unit
}
