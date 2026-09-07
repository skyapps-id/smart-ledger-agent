package consumption

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"smart-ledger-agent/internal/domain"
)

func TestNormalizeUnit(t *testing.T) {
	assert.Equal(t, "lt", NormalizeUnit("Liter"))
	assert.Equal(t, "lt", NormalizeUnit(" ltr "))
	assert.Equal(t, "gr", NormalizeUnit("gram"))
	assert.Equal(t, "kg", NormalizeUnit("Kilogram"))
	assert.Equal(t, "ml", NormalizeUnit("mililiter"))
	assert.Equal(t, "pcs", NormalizeUnit("Buah"))
	assert.Equal(t, "galon", NormalizeUnit("galon"))
}

// TestConvertUsage: konversi pemakaian HANYA dari faktor master goods.
func TestConvertUsage(t *testing.T) {
	galon := &domain.Good{Name: "air aqua galon", Uom: "galon", ConversionUom: "lt", FactorUom: 15}
	inv := &domain.Inventory{GoodsID: 1, Good: galon, StockQty: 1, Unit: "galon"}

	t.Run("satuan sama apa adanya", func(t *testing.T) {
		q, u, ok := ConvertUsage(inv, 1, "galon")
		assert.True(t, ok)
		assert.Equal(t, float64(1), q)
		assert.Equal(t, "galon", u)
	})

	t.Run("satuan konversi master: 3 lt / 15 = 0.2 galon", func(t *testing.T) {
		q, u, ok := ConvertUsage(inv, 3, "lt")
		assert.True(t, ok)
		assert.InDelta(t, 0.2, q, 0.0001)
		assert.Equal(t, "galon", u)
	})

	t.Run("alias liter diterima", func(t *testing.T) {
		_, _, ok := ConvertUsage(inv, 3, "liter")
		assert.True(t, ok)
	})

	t.Run("satuan tak dikenal ditolak", func(t *testing.T) {
		_, _, ok := ConvertUsage(inv, 2, "kg")
		assert.False(t, ok)
	})

	t.Run("tanpa faktor master: hanya satuan stok", func(t *testing.T) {
		plain := &domain.Inventory{Good: &domain.Good{Name: "beras"}, StockQty: 5, Unit: "kg"}
		q, u, ok := ConvertUsage(plain, 2, "kg")
		assert.True(t, ok)
		assert.Equal(t, float64(2), q)
		assert.Equal(t, "kg", u)
		_, _, ok = ConvertUsage(plain, 2, "gr")
		assert.False(t, ok)
	})

	t.Run("hint satuan", func(t *testing.T) {
		assert.Equal(t, "galon atau lt", UsageUnitHint(inv))
	})
}

// TestNormalizeDefaultUnit: "pcs" bawaan LLM diganti satuan stok hanya bila
// pesan tidak menyebut pcs literal.
func TestNormalizeDefaultUnit(t *testing.T) {
	assert.Equal(t, "galon", normalizeDefaultUnit("pakai air aqua galon", "pcs", "galon"))
	assert.Equal(t, "pcs", normalizeDefaultUnit("pakai popok 5pcs", "pcs", "ball"), "pcs eksplisit tetap")
	assert.Equal(t, "ml", normalizeDefaultUnit("pakai susu 100 ml", "ml", "pcs"), "satuan user tetap")
	assert.Equal(t, "pcs", normalizeDefaultUnit("pakai popok", "pcs", "pcs"))
}

func TestFormatQty(t *testing.T) {
	q, u := formatQty(15, "lt")
	assert.Equal(t, "15", q)
	assert.Equal(t, "lt", u)
	q, _ = formatQty(0.2, "galon")
	assert.Equal(t, "0.2", q)
}
