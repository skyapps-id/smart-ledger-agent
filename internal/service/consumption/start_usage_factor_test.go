package consumption

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"smart-ledger-agent/internal/repository"
)

func TestStartUsageCountBasedFactor(t *testing.T) {
	db := setupCompleteFlowTestDB(t)
	svc := NewService(db, repository.NewConsumptionCycleRepository(db), slog.Default())
	ctx := context.Background()

	t.Run("pampers 1 ball = 48 pcs", func(t *testing.T) {
		cycle, err := svc.StartUsage(ctx, "c1", "pampers mamypoko 48", 1, "ball", 1, "2026-05-01")
		require.NoError(t, err)
		assert.Equal(t, float64(48), cycle.ConversionFactor)
		assert.Equal(t, float64(48), cycle.ConsumedQty)
		assert.Equal(t, "pcs", cycle.ConsumedUnit)
		assert.Equal(t, float64(1), cycle.PurchaseQty)
		assert.Equal(t, "ball", cycle.PurchaseUnit)
	})

	t.Run("galon 15lt = 15000 ml", func(t *testing.T) {
		cycle, err := svc.StartUsage(ctx, "c1", "le minerale galon 15lt", 1, "galon", 1, "2026-05-01")
		require.NoError(t, err)
		assert.Equal(t, float64(15000), cycle.ConversionFactor)
		assert.Equal(t, float64(15000), cycle.ConsumedQty)
		assert.Equal(t, "ml", cycle.ConsumedUnit)
	})

	t.Run("susu 200g tetap 200 gr", func(t *testing.T) {
		cycle, err := svc.StartUsage(ctx, "c1", "susu bmt 200g", 1, "pcs", 1, "2026-05-01")
		require.NoError(t, err)
		assert.Equal(t, float64(200), cycle.ConversionFactor)
		assert.Equal(t, float64(200), cycle.ConsumedQty)
		assert.Equal(t, "gr", cycle.ConsumedUnit)
	})
}
