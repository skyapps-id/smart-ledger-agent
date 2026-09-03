package consumption

import (
	"testing"

	"smart-ledger-agent/internal/domain"
)

func invOf(name, unit string, qty float64) *domain.Inventory {
	return &domain.Inventory{ItemName: name, Unit: unit, StockQty: qty}
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
