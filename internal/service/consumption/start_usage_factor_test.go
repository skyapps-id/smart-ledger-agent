package consumption

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

func TestStartUsageCountBasedFactor(t *testing.T) {
	db := setupCompleteFlowTestDB(t)
	svc := NewService(repository.NewConsumptionCycleRepository(db), slog.Default())
	ctx := context.Background()

	t.Run("master: pampers 1 ball = 48 pcs", func(t *testing.T) {
		g := &domain.Good{Name: "pampers mamypoko", Uom: "ball", ConversionUom: "pcs", FactorUom: 48}
		cycle, err := svc.StartUsage(ctx, "c1", g, 1, "ball", 1, "2026-05-01")
		require.NoError(t, err)
		assert.Equal(t, float64(48), cycle.ConversionFactor)
		assert.Equal(t, float64(48), cycle.ConsumedQty)
		assert.Equal(t, "pcs", cycle.ConsumedUnit)
		assert.Equal(t, float64(1), cycle.InventoryQty)
		assert.Equal(t, "ball", cycle.InventoryUnit)
	})

	t.Run("master: galon 15 lt tersimpan apa adanya (tanpa normalisasi ml)", func(t *testing.T) {
		g := &domain.Good{Name: "le minerale galon", Uom: "galon", ConversionUom: "lt", FactorUom: 15}
		cycle, err := svc.StartUsage(ctx, "c1", g, 1, "galon", 1, "2026-05-01")
		require.NoError(t, err)
		assert.Equal(t, float64(15), cycle.ConversionFactor)
		assert.Equal(t, float64(15), cycle.ConsumedQty)
		assert.Equal(t, "lt", cycle.ConsumedUnit)
	})

	t.Run("tanpa master: pakai param caller apa adanya", func(t *testing.T) {
		cycle, err := svc.StartUsage(ctx, "c1", &domain.Good{Name: "susu bmt"}, 1, "pcs", 200, "2026-05-01")
		require.NoError(t, err)
		assert.Equal(t, float64(200), cycle.ConversionFactor)
		assert.Equal(t, float64(200), cycle.ConsumedQty)
	})
	if false {
		_ = ctx
	}
}

// TestStartUsageMasterFactor: faktor resmi di master goods MENANG atas nama
// barang & fallback param, dan tersimpan APA ADANYA — "air aqua galon" +
// master 1 galon = 15 lt → factor 15, unit "lt" (tanpa normalisasi ml).
func TestStartUsageMasterFactor(t *testing.T) {
	db := setupCompleteFlowTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	svc := NewService(cycleRepo, slog.Default())

	goods := &domain.Good{
		Name: "air aqua galon", Uom: "galon",
		ConversionUom: "lt", FactorUom: 15,
	}
	cycle, err := svc.StartUsage(context.Background(), "c1", goods, 1, "galon", 1.0, "2026-05-01")
	require.NoError(t, err)

	assert.Equal(t, 1.0, cycle.InventoryQty)
	assert.Equal(t, "galon", cycle.InventoryUnit)
	assert.Equal(t, 15.0, cycle.ConversionFactor, "1 galon = 15 lt, apa adanya dari master")
	assert.Equal(t, 15.0, cycle.ConsumedQty)
	assert.Equal(t, "lt", cycle.ConsumedUnit)
}
