// Package service tests untuk consumption service dengan tanggal mulai pakai
// dan habis (jalur production: StartUsage + CompleteUsageWithDate).
package consumption

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"log/slog"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

func setupConsumptionTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&domain.ConsumptionCycle{}, &domain.Good{})
	require.NoError(t, err)

	return db
}

func TestConsumptionWithDateRange(t *testing.T) {
	db := setupConsumptionTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	logger := slog.Default()
	service := NewService(cycleRepo, logger)

	ctx := context.Background()
	chatID := "test-chat-123"
	susu := mustGood(t, db, chatID, "Susu")
	mie := mustGood(t, db, chatID, "Mie Instan")
	kopi := mustGood(t, db, chatID, "Kopi")

	t.Run("Start usage dengan tanggal spesifik", func(t *testing.T) {
		cycle, err := service.StartUsage(ctx, chatID, susu, 2.0, "kaleng", 400.0, "2026-08-01")
		require.NoError(t, err)
		assert.Equal(t, susu.ID, cycle.GoodsID)
		assert.Equal(t, 2.0, cycle.InventoryQty)
		assert.Equal(t, "kaleng", cycle.InventoryUnit)
		assert.Equal(t, 400.0, cycle.ConversionFactor)
		start, _ := time.Parse("2006-01-02", "2026-08-01")
		assert.True(t, cycle.StartDate.Equal(start))
	})

	t.Run("Complete dengan tanggal habis dan hitung konsumsi harian", func(t *testing.T) {
		endDate, _ := time.Parse("2006-01-02", "2026-08-30")
		active, err := cycleRepo.GetActiveByGoods(ctx, chatID, susu.ID)
		require.NoError(t, err)

		report, err := service.CompleteUsageWithDate(ctx, chatID, susu, active.BatchNumber, endDate)
		require.NoError(t, err)

		// 2 kaleng x 400gr = 800gr total, 29 hari (1-30 Aug) = 27.6 gr/hari
		assert.Contains(t, report, "29 hari")
		assert.Contains(t, report, "sudah habis!")

		// ConsumedQty dalam satuan konversi (qty × faktor) — tidak dikali faktor lagi saat display.
		cycles, err := cycleRepo.ListByChat(ctx, chatID, 10)
		require.NoError(t, err)
		for _, c := range cycles {
			if c.BatchNumber == active.BatchNumber {
				assert.Equal(t, domain.ConsumptionCycleCompleted, c.Status)
				require.NotNil(t, c.EndDate)
				assert.True(t, c.EndDate.Equal(endDate))
				assert.InDelta(t, 800.0, c.ConsumedQty, 1.0)
			}
		}
	})

	t.Run("Calculate daily consumption tanpa menyimpan cycle", func(t *testing.T) {
		purchaseDate, _ := time.Parse("2006-01-02", "2026-08-01")
		endDate, _ := time.Parse("2006-01-02", "2026-08-30")

		result, err := service.CalculateDailyConsumption(ctx, chatID, "Susu UHT", purchaseDate, endDate, 6.0, "kaleng", 1000.0)
		require.NoError(t, err)
		assert.Contains(t, result, "Susu UHT")
		assert.Contains(t, result, "6000 kaleng")       // ditampilkan dalam satuan beli (verbatim), TIDAK auto-upgrade ke kg
		assert.Contains(t, result, "206.9 kaleng/hari") // 6000 / 29 hari = 206.9
	})

	t.Run("Error handling - tanggal habis sebelum mulai pakai", func(t *testing.T) {
		// Buat cycle aktif baru untuk test ini
		cycle, err := service.StartUsage(ctx, chatID, mie, 10.0, "bungkus", 100.0, "2026-08-15")
		require.NoError(t, err)

		endDate, _ := time.Parse("2006-01-02", "2026-08-01") // End date before start

		_, err = service.CompleteUsageWithDate(ctx, chatID, mie, cycle.BatchNumber, endDate)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "durasi penggunaan tidak valid")
	})

	t.Run("Get active cycle info dalam satuan konversi master", func(t *testing.T) {
		// Buat cycle aktif baru
		_, err := service.StartUsage(ctx, chatID, kopi, 1.0, "kg", 1000.0, "2026-08-05")
		require.NoError(t, err)

		info, err := service.GetActiveCycleInfo(ctx, chatID, kopi, "")
		require.NoError(t, err)
		assert.Contains(t, info, "Kopi")
		assert.Contains(t, info, "1 kg")    // satuan stok user
		assert.Contains(t, info, "kg/hari") // rate dalam satuan tersimpan (verbatim)
	})
}

func TestConsumptionHistoryWithDailyRate(t *testing.T) {
	db := setupConsumptionTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	logger := slog.Default()
	service := NewService(cycleRepo, logger)

	ctx := context.Background()
	chatID := "test-chat-history"
	teh := mustGood(t, db, chatID, "Teh")

	// Buat beberapa cycle yang sudah selesai
	c1, err := service.StartUsage(ctx, chatID, teh, 10.0, "bungkus", 50.0, "2026-07-01")
	require.NoError(t, err)
	date2, _ := time.Parse("2006-01-02", "2026-07-15")
	_, err = service.CompleteUsageWithDate(ctx, chatID, teh, c1.BatchNumber, date2)
	require.NoError(t, err)

	c2, err := service.StartUsage(ctx, chatID, teh, 8.0, "bungkus", 50.0, "2026-07-16")
	require.NoError(t, err)
	date4, _ := time.Parse("2006-01-02", "2026-07-25")
	_, err = service.CompleteUsageWithDate(ctx, chatID, teh, c2.BatchNumber, date4)
	require.NoError(t, err)

	t.Run("Get history menampilkan daily rate", func(t *testing.T) {
		history, err := service.GetHistory(ctx, chatID, teh)
		require.NoError(t, err)
		assert.Contains(t, history, "Teh")
		assert.Contains(t, history, "bungkus/hari") // rate dalam satuan tersimpan (verbatim)

		// Cycle pertama: 10 bungkus x 50gr = 500gr / 14 hari = 35.7 gr/hari
		assert.Contains(t, history, "500 bungkus") // Total pengambilan stok
	})

	t.Run("Calculate daily consumption untuk berbagai scenario", func(t *testing.T) {
		testCases := []struct {
			name          string
			purchaseQty   float64
			purchaseUnit  string
			convFactor    float64
			startDate     string
			endDate       string
			expectedDaily float64
		}{
			{
				name:          "Susu 2 kaleng (800gr) selama 30 hari",
				purchaseQty:   2.0,
				purchaseUnit:  "kaleng",
				convFactor:    400.0,
				startDate:     "2026-08-01",
				endDate:       "2026-08-30",
				expectedDaily: 800.0 / 29.0, // 29 hari
			},
			{
				name:          "Berat 5kg selama 90 hari",
				purchaseQty:   5.0,
				purchaseUnit:  "kg",
				convFactor:    1000.0,
				startDate:     "2026-06-01",
				endDate:       "2026-08-30",
				expectedDaily: 5000.0 / 90.0,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				start, _ := time.Parse("2006-01-02", tc.startDate)
				end, _ := time.Parse("2006-01-02", tc.endDate)

				result, err := service.CalculateDailyConsumption(ctx, chatID, "Test Item", start, end, tc.purchaseQty, tc.purchaseUnit, tc.convFactor)
				require.NoError(t, err)
				// Rate tampil dalam satuan user apa adanya (tanpa konversi).
				assert.Contains(t, result, tc.purchaseUnit+"/hari")
			})
		}
	})
}
