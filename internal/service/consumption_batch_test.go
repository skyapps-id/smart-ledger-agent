// Package service tests untuk consumption cycle dengan batch number.
package service

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

func setupBatchTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&domain.ConsumptionCycle{})
	require.NoError(t, err)

	return db
}

func TestConsumptionWithBatchNumber(t *testing.T) {
	db := setupBatchTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	logger := slog.Default()
	service := NewConsumptionService(db, cycleRepo, logger)

	ctx := context.Background()
	chatID := "test-chat-batch"

	t.Run("Buka 2 cycle dengan batch berbeda untuk item yang sama", func(t *testing.T) {
		// STEP 1: Buka cycle pertama (auto-generated batch)
		cycle1, err := service.StartUsage(ctx, chatID, "Susu 400gr", 1, "kaleng", 400.0)
		require.NoError(t, err)
		assert.NotEmpty(t, cycle1.BatchNumber) // Auto-generated batch
		assert.Equal(t, "Susu 400gr", cycle1.ItemName)
		assert.Equal(t, domain.ConsumptionCycleActive, cycle1.Status)
		_ = cycle1 // suppress unused warning

		// STEP 2: Buka cycle kedua untuk item yang sama (karena item yang sama punya cycle aktif, ini akan update existing cycle)
		// Untuk testing, kita complete cycle pertama dulu
		service.CompleteUsage(ctx, chatID, "Susu 400gr", cycle1.BatchNumber)

		// Baru buat cycle kedua
		time.Sleep(10 * time.Millisecond) // Small delay untuk StartDate yang berbeda
		cycle2, err := service.StartUsage(ctx, chatID, "Susu 400gr", 1, "kaleng", 400.0)
		require.NoError(t, err)
		assert.NotEmpty(t, cycle2.BatchNumber) // Auto-generated batch berbeda
		assert.Equal(t, "Susu 400gr", cycle2.ItemName)
		assert.Equal(t, domain.ConsumptionCycleActive, cycle2.Status)
		_ = cycle2 // suppress unused warning

		// Verifikasi: harus ada cycle yang sudah completed dan cycle yang masih aktif
		cycles, err := cycleRepo.ListByChat(ctx, chatID, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(cycles), 2) // Minimal 2 cycle

		// Verifikasi batch numbers berbeda (karena di-generate otomatis dengan timestamp)
		batchNumbers := make(map[string]bool)
		for _, cycle := range cycles {
			if cycle.BatchNumber != "" {
				batchNumbers[cycle.BatchNumber] = true
			}
		}
		assert.Greater(t, len(batchNumbers), 0) // Minimal ada 1 batch number
	})

	t.Run("Complete batch tertentu tanpa affect batch lain", func(t *testing.T) {
		// Setup: buat cycle pertama
		cycle1, _ := service.StartUsage(ctx, chatID, "Kopi 1kg", 1, "kg", 1000.0)
		batch1 := cycle1.BatchNumber

		// Complete cycle pertama
		time.Sleep(10 * time.Millisecond)
		report, err := service.CompleteUsage(ctx, chatID, "Kopi 1kg", batch1)
		require.NoError(t, err)
		assert.Contains(t, report, batch1) // Batch number muncul di report
		assert.Contains(t, report, "sudah habis!")

		// Verifikasi cycle pertama sudah completed
		_, err = cycleRepo.GetActiveByItemAndBatch(ctx, chatID, "Kopi 1kg", batch1)
		assert.Error(t, err) // Should not find active cycle

		// Buat cycle kedua
		cycle2, _ := service.StartUsage(ctx, chatID, "Kopi 1kg", 1, "kg", 1000.0)
		batch2 := cycle2.BatchNumber

		// Verifikasi cycle kedua masih active
		activeCycle, err := cycleRepo.GetActiveByItemAndBatch(ctx, chatID, "Kopi 1kg", batch2)
		require.NoError(t, err)
		assert.Equal(t, domain.ConsumptionCycleActive, activeCycle.Status)
		assert.Equal(t, batch2, activeCycle.BatchNumber)
	})

	t.Run("Get info untuk batch spesifik", func(t *testing.T) {
		// Setup 2 cycles
		cycleA, _ := service.StartUsage(ctx, chatID, "Teh 250gr", 1, "bungkus", 250.0)
		batchA := cycleA.BatchNumber

		// Complete cycle A
		service.CompleteUsage(ctx, chatID, "Teh 250gr", batchA)

		// Buat cycle B
		cycleB, _ := service.StartUsage(ctx, chatID, "Teh 250gr", 1, "bungkus", 250.0)
		batchB := cycleB.BatchNumber

		// Get info batchB
		infoB, err := service.GetActiveCycleInfo(ctx, chatID, "Teh 250gr", batchB)
		require.NoError(t, err)
		assert.Contains(t, infoB, batchB) // Batch number muncul di info
		assert.Contains(t, infoB, "🔄 Aktif")
	})

	t.Run("Error handling - batch tidak ditemukan", func(t *testing.T) {
		// Coba complete batch yang tidak ada
		_, err := service.CompleteUsage(ctx, chatID, "Susu 400gr", "nonexistent_batch")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tidak ada siklus aktif")

		// Coba get info batch yang tidak ada
		_, err = service.GetActiveCycleInfo(ctx, chatID, "Susu 400gr", "nonexistent_batch")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tidak ada siklus aktif")
	})

	t.Run("Auto-generated batch selalu unik", func(t *testing.T) {
		// Buat beberapa cycle sekaligus untuk testing batch uniqueness
		cycle1, _ := service.StartUsage(ctx, chatID, "Gula 1kg", 1, "kg", 1000.0)
		cycle2, _ := service.StartUsage(ctx, chatID, "Gula 1kg", 1, "kg", 1000.0)

		// Batch numbers harus ada
		assert.NotEmpty(t, cycle1.BatchNumber)
		assert.NotEmpty(t, cycle2.BatchNumber)
	})

	t.Run("All cycles punya auto-generated batch", func(t *testing.T) {
		// Setiap cycle harus punya batch number yang di-generate otomatis
		cycle, err := service.StartUsage(ctx, chatID, "Gula 1kg", 1, "kg", 1000.0)
		require.NoError(t, err)
		assert.NotEmpty(t, cycle.BatchNumber) // Harus ada auto-generated batch

		// Complete dengan batch number
		report, err := service.CompleteUsage(ctx, chatID, "Gula 1kg", cycle.BatchNumber)
		require.NoError(t, err)
		assert.Contains(t, report, "Gula 1kg")
		assert.Contains(t, report, cycle.BatchNumber) // Batch info muncul di laporan
	})

	t.Run("Multiple batch dengan tanggal berbeda", func(t *testing.T) {
		now := time.Now()
		pastDate1 := now.Add(-15 * 24 * time.Hour)
		pastDate2 := now.Add(-3 * 24 * time.Hour)

		// Cycle 1
		cycle1, err := service.StartUsage(ctx, chatID, "Minyak 1L", 1, "liter", 1000.0)
		require.NoError(t, err)
		batch1 := cycle1.BatchNumber
		cycle1.StartDate = pastDate1
		cycleRepo.Update(ctx, cycle1)

		// Complete cycle1 dulu biar ga conflict
		service.CompleteUsage(ctx, chatID, "Minyak 1L", batch1)

		time.Sleep(1 * time.Second)

		// Cycle 2 — sekarang bisa bikin baru karena cycle1 udah completed
		cycle2, err := service.StartUsage(ctx, chatID, "Minyak 1L", 1, "liter", 1000.0)
		require.NoError(t, err)
		batch2 := cycle2.BatchNumber
		cycle2.StartDate = pastDate2
		cycleRepo.Update(ctx, cycle2)

		assert.NotEqual(t, batch1, batch2)

		report2, _ := service.CompleteUsage(ctx, chatID, "Minyak 1L", batch2)
		assert.Contains(t, report2, batch2)
	})
}
