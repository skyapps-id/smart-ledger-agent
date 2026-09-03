package consumption

import (
	"strings"

	"smart-ledger-agent/internal/domain"
)

// ConvertToInventoryUnit mengonversi jumlah pemakaian hasil LLM (mis. 200 g)
// ke satuan inventory (mis. 1 pcs) memakai ukuran yang tertera pada nama
// barang: inventory "susu bmt 200g" (satuan pcs) berarti 200 gr per pcs.
//
// Aturan:
//   - Satuan inventory sudah gr/ml (barang grosir) → cukup konversi skala
//     (gr↔kg, ml↔liter).
//   - Satuan inventory berupa kemasan (pcs/botol/...) dan nama barang memuat
//     isi per kemasan → qty kemasan = pemakaian(dalam gr/ml) ÷ isi per kemasan.
//   - Dimensi beda (gr vs ml) atau informasi kurang → dikembalikan apa adanya
//     (validasi stok akan menolak dengan pesan jelas).
func ConvertToInventoryUnit(inv *domain.Inventory, qty float64, unit string) (float64, string) {
	if inv == nil || qty <= 0 || unit == "" {
		return qty, unit
	}
	u := strings.ToLower(strings.TrimSpace(unit))
	invUnit := strings.ToLower(strings.TrimSpace(inv.Unit))

	// toBase menormalkan satuan berat/volume; ok=false bila bukan keduanya.
	toBase := func(u string, q float64) (base string, val float64, ok bool) {
		switch u {
		case "g", "gr", "gram":
			return "gr", q, true
		case "kg", "kilogram":
			return "gr", q * 1000, true
		case "ml", "mililiter":
			return "ml", q, true
		case "l", "liter":
			return "ml", q * 1000, true
		}
		return u, q, false
	}

	usageBase, usageVal, isUsageBase := toBase(u, qty)

	// Kasus 1: satuan inventory sendiri berbasis berat/volume → konversi skala.
	if invBase, _, isInvBase := toBase(invUnit, 1); isInvBase && isUsageBase {
		switch {
		case invBase == "gr" && usageBase == "gr":
			if invUnit == "kg" {
				return usageVal / 1000, inv.Unit // gr → kg
			}
			return usageVal, inv.Unit // gr → gr
		case invBase == "ml" && usageBase == "ml":
			if invUnit == "l" || invUnit == "liter" {
				return usageVal / 1000, inv.Unit // ml → liter
			}
			return usageVal, inv.Unit // ml → ml
		}
		return qty, unit // dimensi beda (gr vs ml) — jangan dikonversi
	}

	// Kasus 2: inventory ber-satuan kemasan; isi per kemasan ada di nama barang.
	perQty := ExtractQuantityFromItemName(inv.ItemName)      // mis. 200
	perUnit := ExtractOriginalUnitFromItemName(inv.ItemName) // "gr" / "ml"
	if isUsageBase && perQty > 0 && perUnit != "" && perUnit == usageBase {
		return usageVal / perQty, inv.Unit // 200g ÷ 200g/pcs = 1 pcs
	}

	return qty, unit
}
