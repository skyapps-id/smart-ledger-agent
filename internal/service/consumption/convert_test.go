package consumption

import (
	"testing"

	"smart-ledger-agent/internal/domain"
)

func invOf(name, unit string, qty float64) *domain.Inventory {
	return &domain.Inventory{Good: &domain.Good{Name: name}, Unit: unit, StockQty: qty}
}

func TestConvertGramToPcs(t *testing.T) {
	inv := invOf("susu bmt 200g", "pcs", 1)
	qty, unit := ConvertToInventoryUnit(inv, 200, "g")
	if qty != 1 || unit != "pcs" {
		t.Errorf("expected 1 pcs, got %g %s", qty, unit)
	}
}

func TestConvertPartialGramToPcs(t *testing.T) {
	inv := invOf("susu bmt 200g", "pcs", 1)
	qty, unit := ConvertToInventoryUnit(inv, 100, "g")
	if qty != 0.5 || unit != "pcs" {
		t.Errorf("expected 0.5 pcs, got %g %s", qty, unit)
	}
}

func TestConvertGramVariantToPcs(t *testing.T) {
	inv := invOf("susu bmt 200gr", "pcs", 2)
	for _, u := range []string{"g", "gr", "gram"} {
		if qty, unit := ConvertToInventoryUnit(inv, 400, u); qty != 2 || unit != "pcs" {
			t.Errorf("unit %s: expected 2 pcs, got %g %s", u, qty, unit)
		}
	}
}

func TestConvertMlToBottle(t *testing.T) {
	inv := invOf("minyak 500ml", "botol", 3)
	qty, unit := ConvertToInventoryUnit(inv, 250, "ml")
	if qty != 0.5 || unit != "botol" {
		t.Errorf("expected 0.5 botol, got %g %s", qty, unit)
	}
}

func TestConvertLiterToMlInventory(t *testing.T) {
	inv := invOf("air", "ml", 1000)
	qty, unit := ConvertToInventoryUnit(inv, 2, "liter")
	if qty != 2000 || unit != "ml" {
		t.Errorf("expected 2000 ml, got %g %s", qty, unit)
	}
}

func TestConvertGramToKgInventory(t *testing.T) {
	inv := invOf("beras", "kg", 5)
	qty, unit := ConvertToInventoryUnit(inv, 500, "gr")
	if qty != 0.5 || unit != "kg" {
		t.Errorf("expected 0.5 kg, got %g %s", qty, unit)
	}
}

func TestConvertSameUnitUnchanged(t *testing.T) {
	inv := invOf("popok", "pcs", 10)
	if qty, unit := ConvertToInventoryUnit(inv, 3, "pcs"); qty != 3 || unit != "pcs" {
		t.Errorf("expected unchanged 3 pcs, got %g %s", qty, unit)
	}
}

func TestConvertCrossDimensionUnchanged(t *testing.T) {
	// gr vs ml: jangan nebak — biarkan validasi stok yang menolak jelas.
	inv := invOf("susu bmt 200g", "pcs", 1)
	if qty, unit := ConvertToInventoryUnit(inv, 5, "ml"); qty != 5 || unit != "ml" {
		t.Errorf("expected unchanged 5 ml, got %g %s", qty, unit)
	}
}

func TestConvertNoSizeInNameUnchanged(t *testing.T) {
	inv := invOf("susu", "pcs", 2) // nama tanpa ukuran → tak bisa konversi
	if qty, unit := ConvertToInventoryUnit(inv, 200, "g"); qty != 200 || unit != "g" {
		t.Errorf("expected unchanged 200 g, got %g %s", qty, unit)
	}
}

func TestConvertLiterNameToPcs(t *testing.T) {
	// Bug lama: "1liter" dihitung 1 (bukan 1000 ml) → 500 ml harusnya 0.5 pcs.
	inv := invOf("susu 1liter", "pcs", 1)
	if qty, unit := ConvertToInventoryUnit(inv, 500, "ml"); qty != 0.5 || unit != "pcs" {
		t.Errorf("expected 0.5 pcs, got %g %s", qty, unit)
	}
}

func TestConvertLtUsageWithSizeInName(t *testing.T) {
	inv := invOf("le minerale galon 15lt", "galon", 2)
	if qty, unit := ConvertToInventoryUnit(inv, 15, "lt"); qty != 1 || unit != "galon" {
		t.Errorf("expected 1 galon, got %g %s", qty, unit)
	}
	if qty, unit := ConvertToInventoryUnit(inv, 7.5, "lt"); qty != 0.5 || unit != "galon" {
		t.Errorf("expected 0.5 galon, got %g %s", qty, unit)
	}
}

func TestConvertFromMessageFullContainer(t *testing.T) {
	inv := invOf("le minerale galon", "galon", 1)
	qty, unit := ConvertToInventoryUnitFromMessage(inv, 15, "lt", "Pakai le minerale galon 15lt")
	if qty != 1 || unit != "galon" {
		t.Errorf("expected 1 galon, got %g %s", qty, unit)
	}
}

func TestConvertFromMessagePartialContainer(t *testing.T) {
	inv := invOf("le minerale galon", "galon", 1)
	qty, unit := ConvertToInventoryUnitFromMessage(inv, 7.5, "lt", "Pakai le minerale galon 15lt sisa 7.5lt")
	if qty != 0.5 || unit != "galon" {
		t.Errorf("expected 0.5 galon, got %g %s", qty, unit)
	}
}

func TestConvertFromMessageNoPatternUnchanged(t *testing.T) {
	inv := invOf("le minerale galon", "galon", 1)
	if qty, unit := ConvertToInventoryUnitFromMessage(inv, 7.5, "lt", "Pakai le minerale 7.5lt"); qty != 7.5 || unit != "lt" {
		t.Errorf("expected unchanged 7.5 lt, got %g %s", qty, unit)
	}
}

func TestConvertFromMessageSizeInNameStillWins(t *testing.T) {
	inv := invOf("minyak 500ml", "botol", 3)
	qty, unit := ConvertToInventoryUnitFromMessage(inv, 250, "ml", "pakai minyak botol 250ml")
	if qty != 0.5 || unit != "botol" {
		t.Errorf("expected 0.5 botol (konversi dari nama), got %g %s", qty, unit)
	}
}

func TestConvertPcsToBallWithCountName(t *testing.T) {
	inv := invOf("pampers mamypoko 48", "ball", 1)
	qty, unit := ConvertToInventoryUnit(inv, 5, "pcs")
	if qty != 5.0/48 || unit != "ball" {
		t.Errorf("expected %.4f ball, got %g %s", 5.0/48, qty, unit)
	}
	if qty, unit := ConvertToInventoryUnit(inv, 48, "pcs"); qty != 1 || unit != "ball" {
		t.Errorf("expected 1 ball, got %g %s", qty, unit)
	}
}

func TestConvertPcsToBallWithPcsSuffixName(t *testing.T) {
	inv := invOf("pampers mamypoko 48pcs", "ball", 2)
	if qty, unit := ConvertToInventoryUnit(inv, 12, "pcs"); qty != 0.25 || unit != "ball" {
		t.Errorf("expected 0.25 ball, got %g %s", qty, unit)
	}
}

func TestConvertBuahAliasToBall(t *testing.T) {
	inv := invOf("pampers mamypoko 48", "ball", 1)
	if qty, unit := ConvertToInventoryUnit(inv, 24, "buah"); qty != 0.5 || unit != "ball" {
		t.Errorf("expected 0.5 ball, got %g %s", qty, unit)
	}
}

func TestConvertCountNameDoesNotBreakVolumeItems(t *testing.T) {
	// Nama volume harus tetap dikonversi via satuan, bukan angka telanjang.
	inv := invOf("susu bmt 200g", "pcs", 1)
	if qty, unit := ConvertToInventoryUnit(inv, 100, "g"); qty != 0.5 || unit != "pcs" {
		t.Errorf("expected 0.5 pcs, got %g %s", qty, unit)
	}
}

func TestConvertFromMessageBallCount(t *testing.T) {
	inv := invOf("pampers", "ball", 1) // nama tanpa ukuran sama sekali
	qty, unit := ConvertToInventoryUnitFromMessage(inv, 48, "pcs", "pakai pampers 1 ball isi 48")
	if qty != 1 || unit != "ball" {
		t.Errorf("expected 1 ball, got %g %s", qty, unit)
	}
}

func TestResolveStoredContentWins(t *testing.T) {
	// Isi dipelajari dari user: 1 galon = 19lt; nama & pesan tanpa ukuran.
	inv := invOf("le minerale galon", "galon", 1)
	inv.Good.FactorUom = 19
	inv.Good.ConversionUom = "lt"

	qty, unit, learnedQty, _, ok := ResolveUsageConversion(inv, 19, "lt", "pakai air 19lt")
	if !ok || qty != 1 || unit != "galon" || learnedQty != 0 {
		t.Errorf("expected 1 galon via stored content, got %g %s ok=%v learned=%g", qty, unit, ok, learnedQty)
	}
	if qty, _, _, _, ok := ResolveUsageConversion(inv, 9.5, "lt", "pakai air 9.5lt"); !ok || qty != 0.5 {
		t.Errorf("expected 0.5 galon, got %g ok=%v", qty, ok)
	}
}

func TestResolveLearnsFromName(t *testing.T) {
	inv := invOf("minyak 500ml", "botol", 3)
	_, _, learnedQty, learnedUnit, ok := ResolveUsageConversion(inv, 250, "ml", "pakai minyak 250ml")
	if !ok || learnedQty != 500 || learnedUnit != "ml" {
		t.Errorf("expected learned 500 ml, got %g %s ok=%v", learnedQty, learnedUnit, ok)
	}
}

func TestResolveUnknownContentFails(t *testing.T) {
	inv := invOf("le minerale galon", "galon", 1)
	qty, _, _, _, ok := ResolveUsageConversion(inv, 3, "lt", "pakai air 3lt")
	if ok || qty != 3 {
		t.Errorf("expected no conversion, got %g ok=%v", qty, ok)
	}
}

func TestConversionQuestion(t *testing.T) {
	inv := invOf("le minerale galon", "galon", 1)

	if q := ConversionQuestion(inv, "lt"); q == "" {
		t.Error("expected question for unknown kemasan + base usage unit")
	}
	if q := ConversionQuestion(inv, "galon"); q != "" {
		t.Error("same unit as inventory should not ask")
	}
	inv.Good.FactorUom, inv.Good.ConversionUom = 15, "lt"
	if q := ConversionQuestion(inv, "lt"); q != "" {
		t.Error("stored content should not ask")
	}
	baseInv := invOf("beras", "kg", 5)
	if q := ConversionQuestion(baseInv, "gr"); q != "" {
		t.Error("base-unit inventory should not ask")
	}
}
