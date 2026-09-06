// Package service tests untuk consumption service dengan tanggal pembelian dan habis.
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
	service := NewService(db, cycleRepo, logger)

	ctx := context.Background()
	chatID := "test-chat-123"
	susu := mustGood(t, db, chatID, "Susu")
	mie := mustGood(t, db, chatID, "Mie Instan")
	kopi := mustGood(t, db, chatID, "Kopi")

	t.Run("Start cycle dengan tanggal pembelian spesifik", func(t *testing.T) {
		purchaseDate, _ := time.Parse("2006-01-02", "2026-08-01")
		cycle, err := service.StartCycleWithDate(ctx, chatID, susu, 2.0, "kaleng", 400.0, purchaseDate)
		require.NoError(t, err)
		assert.Equal(t, susu.ID, cycle.GoodsID)
		assert.Equal(t, 2.0, cycle.PurchaseQty)
		assert.Equal(t, "kaleng", cycle.PurchaseUnit)
		assert.Equal(t, 400.0, cycle.ConversionFactor)
		assert.True(t, cycle.StartDate.Equal(purchaseDate))
	})

	t.Run("Complete cycle dengan tanggal habis dan hitung konsumsi harian", func(t *testing.T) {
		endDate, _ := time.Parse("2006-01-02", "2026-08-30")
		cycle, err := service.CompleteCycleWithEndDate(ctx, chatID, susu, endDate)
		require.NoError(t, err)
		assert.Equal(t, domain.ConsumptionCycleCompleted, cycle.Status)
		assert.True(t, cycle.EndDate != nil)
		assert.True(t, cycle.EndDate.Equal(endDate))

		// Verifikasi perhitungan konsumsi
		// 2 kaleng x 400gr = 800gr total
		// 29 hari (1-30 Aug) = 27.6 gr/hari
		expectedDays := 29.0
		actualDays := endDate.Sub(cycle.StartDate).Hours() / 24
		assert.InDelta(t, expectedDays, actualDays, 0.5)

		// ConsumedQty sudah dalam satuan dasar (gr) — bukan dikali factor lagi.
		assert.InDelta(t, 800.0, cycle.ConsumedQty, 1.0)
	})

	t.Run("Calculate daily consumption tanpa menyimpan cycle", func(t *testing.T) {
		purchaseDate, _ := time.Parse("2006-01-02", "2026-08-01")
		endDate, _ := time.Parse("2006-01-02", "2026-08-30")

		result, err := service.CalculateDailyConsumption(ctx, chatID, "Susu UHT", purchaseDate, endDate, 6.0, "kaleng", 1000.0)
		require.NoError(t, err)
		assert.Contains(t, result, "Susu UHT")
		assert.Contains(t, result, "6000 gr")       // tanpa satuan asli user → satuan dasar, TIDAK auto-upgrade ke kg
		assert.Contains(t, result, "206.9 gr/hari") // 6000 / 29 hari = 206.9
	})

	t.Run("Error handling - tanggal habis sebelum pembelian", func(t *testing.T) {
		// Buat cycle aktif baru untuk test ini
		purchaseDate, _ := time.Parse("2006-01-02", "2026-08-15")
		_, _ = service.StartCycleWithDate(ctx, chatID, mie, 10.0, "bungkus", 100.0, purchaseDate)

		endDate, _ := time.Parse("2006-01-02", "2026-08-01") // End date before purchase

		_, err := service.CompleteCycleWithEndDate(ctx, chatID, mie, endDate)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tanggal habis harus setelah tanggal pembelian")
	})

	t.Run("Get active cycle info dalam satuan terkecil", func(t *testing.T) {
		// Buat cycle aktif baru
		purchaseDate, _ := time.Parse("2006-01-02", "2026-08-05")
		_, err := service.StartCycleWithDate(ctx, chatID, kopi, 1.0, "kg", 1000.0, purchaseDate)
		require.NoError(t, err)

		info, err := service.GetActiveCycleInfo(ctx, chatID, kopi, "")
		require.NoError(t, err)
		assert.Contains(t, info, "Kopi")
		assert.Contains(t, info, "1 kg") // satuan beli user = kg → tampil dalam kg
		assert.Contains(t, info, "kg/hari")
	})
}

func TestConsumptionHistoryWithDailyRate(t *testing.T) {
	db := setupConsumptionTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	logger := slog.Default()
	service := NewService(db, cycleRepo, logger)

	ctx := context.Background()
	chatID := "test-chat-history"
	teh := mustGood(t, db, chatID, "Teh")

	// Buat beberapa cycle yang sudah selesai
	date1, _ := time.Parse("2006-01-02", "2026-07-01")
	date2, _ := time.Parse("2006-01-02", "2026-07-15")
	_, _ = service.StartCycleWithDate(ctx, chatID, teh, 10.0, "bungkus", 50.0, date1)
	service.CompleteCycleWithEndDate(ctx, chatID, teh, date2)

	date3, _ := time.Parse("2006-01-02", "2026-07-16")
	date4, _ := time.Parse("2006-01-02", "2026-07-25")
	service.StartCycleWithDate(ctx, chatID, teh, 8.0, "bungkus", 50.0, date3)
	service.CompleteCycleWithEndDate(ctx, chatID, teh, date4)

	t.Run("Get history menampilkan daily rate dalam gram", func(t *testing.T) {
		history, err := service.GetHistory(ctx, chatID, teh, 10)
		require.NoError(t, err)
		assert.Contains(t, history, "Teh")
		assert.Contains(t, history, "gr/hari") // Harus menampilkan rate dalam gram per hari

		// Cycle pertama: 10 bungkus x 50gr = 500gr / 14 hari = 35.7 gr/hari
		assert.Contains(t, history, "500 gr") // Total pembelian
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
				// Rate tampil dalam satuan user: "kaleng" bukan base → gr; "kg" → kg.
				wantUnit := "gr/hari"
				if tc.purchaseUnit == "kg" {
					wantUnit = "kg/hari"
				}
				assert.Contains(t, result, wantUnit)
			})
		}
	})
}
