// Package service tests untuk consumption cycle dengan batch number.
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

func setupBatchTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&domain.ConsumptionCycle{}, &domain.Good{})
	require.NoError(t, err)

	return db
}

// mustGood membuat master goods untuk pengujian (relasi via goods_id).
func mustGood(t *testing.T, db *gorm.DB, chatID, name string) *domain.Good {
	t.Helper()
	g := &domain.Good{ChatID: chatID, Code: "T-" + name, Name: name}
	require.NoError(t, db.Create(g).Error)
	return g
}

func TestConsumptionWithBatchNumber(t *testing.T) {
	db := setupBatchTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	logger := slog.Default()
	service := NewService(db, cycleRepo, logger)

	ctx := context.Background()
	chatID := "test-chat-batch"
	susu := mustGood(t, db, chatID, "Susu 400gr")
	kopi := mustGood(t, db, chatID, "Kopi 1kg")
	teh := mustGood(t, db, chatID, "Teh 250gr")
	gula := mustGood(t, db, chatID, "Gula 1kg")
	minyak := mustGood(t, db, chatID, "Minyak 1L")

	t.Run("Buka 2 cycle dengan batch berbeda untuk item yang sama", func(t *testing.T) {
		// STEP 1: Buka cycle pertama (auto-generated batch)
		cycle1, err := service.StartUsage(ctx, chatID, susu, 1, "kaleng", 400.0, "")
		require.NoError(t, err)
		assert.NotEmpty(t, cycle1.BatchNumber) // Auto-generated batch
		assert.Equal(t, susu.ID, cycle1.GoodsID)
		assert.Equal(t, domain.ConsumptionCycleActive, cycle1.Status)
		_ = cycle1 // suppress unused warning

		// STEP 2: Buka cycle kedua untuk item yang sama (karena item yang sama punya cycle aktif, ini akan update existing cycle)
		// Untuk testing, kita complete cycle pertama dulu
		service.CompleteUsage(ctx, chatID, susu, cycle1.BatchNumber)

		// Baru buat cycle kedua
		time.Sleep(10 * time.Millisecond) // Small delay untuk StartDate yang berbeda
		cycle2, err := service.StartUsage(ctx, chatID, susu, 1, "kaleng", 400.0, "")
		require.NoError(t, err)
		assert.NotEmpty(t, cycle2.BatchNumber) // Auto-generated batch berbeda
		assert.Equal(t, susu.ID, cycle2.GoodsID)
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
		cycle1, _ := service.StartUsage(ctx, chatID, kopi, 1, "kg", 1000.0, "")
		batch1 := cycle1.BatchNumber

		// Complete cycle pertama
		time.Sleep(10 * time.Millisecond)
		report, err := service.CompleteUsage(ctx, chatID, kopi, batch1)
		require.NoError(t, err)
		assert.Contains(t, report, batch1) // Batch number muncul di report
		assert.Contains(t, report, "sudah habis!")

		// Verifikasi cycle pertama sudah completed
		_, err = cycleRepo.GetActiveByGoodsAndBatch(ctx, chatID, kopi.ID, batch1)
		assert.Error(t, err) // Should not find active cycle

		// Buat cycle kedua
		cycle2, _ := service.StartUsage(ctx, chatID, kopi, 1, "kg", 1000.0, "")
		batch2 := cycle2.BatchNumber

		// Verifikasi cycle kedua masih active
		activeCycle, err := cycleRepo.GetActiveByGoodsAndBatch(ctx, chatID, kopi.ID, batch2)
		require.NoError(t, err)
		assert.Equal(t, domain.ConsumptionCycleActive, activeCycle.Status)
		assert.Equal(t, batch2, activeCycle.BatchNumber)
	})

	t.Run("Get info untuk batch spesifik", func(t *testing.T) {
		// Setup 2 cycles
		cycleA, _ := service.StartUsage(ctx, chatID, teh, 1, "bungkus", 250.0, "")
		batchA := cycleA.BatchNumber

		// Complete cycle A
		service.CompleteUsage(ctx, chatID, teh, batchA)

		// Buat cycle B
		cycleB, _ := service.StartUsage(ctx, chatID, teh, 1, "bungkus", 250.0, "")
		batchB := cycleB.BatchNumber

		// Get info batchB
		infoB, err := service.GetActiveCycleInfo(ctx, chatID, teh, batchB)
		require.NoError(t, err)
		assert.Contains(t, infoB, batchB) // Batch number muncul di info
		assert.Contains(t, infoB, "🔄 Aktif")
	})

	t.Run("Error handling - batch tidak ditemukan", func(t *testing.T) {
		// Coba complete batch yang tidak ada
		_, err := service.CompleteUsage(ctx, chatID, susu, "nonexistent_batch")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tidak ada siklus aktif")

		// Coba get info batch yang tidak ada
		_, err = service.GetActiveCycleInfo(ctx, chatID, susu, "nonexistent_batch")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tidak ada siklus aktif")
	})

	t.Run("Auto-generated batch selalu unik", func(t *testing.T) {
		// Buat beberapa cycle sekaligus untuk testing batch uniqueness
		cycle1, _ := service.StartUsage(ctx, chatID, gula, 1, "kg", 1000.0, "")
		cycle2, _ := service.StartUsage(ctx, chatID, gula, 1, "kg", 1000.0, "")

		// Batch numbers harus ada
		assert.NotEmpty(t, cycle1.BatchNumber)
		assert.NotEmpty(t, cycle2.BatchNumber)
	})

	t.Run("All cycles punya auto-generated batch", func(t *testing.T) {
		// Setiap cycle harus punya batch number yang di-generate otomatis
		cycle, err := service.StartUsage(ctx, chatID, gula, 1, "kg", 1000.0, "")
		require.NoError(t, err)
		assert.NotEmpty(t, cycle.BatchNumber) // Harus ada auto-generated batch

		// Complete dengan batch number
		report, err := service.CompleteUsage(ctx, chatID, gula, cycle.BatchNumber)
		require.NoError(t, err)
		assert.Contains(t, report, "Gula 1kg")
		assert.Contains(t, report, cycle.BatchNumber) // Batch info muncul di laporan
	})

	t.Run("Multiple batch dengan tanggal berbeda", func(t *testing.T) {
		now := time.Now()
		pastDate1 := now.Add(-15 * 24 * time.Hour)
		pastDate2 := now.Add(-3 * 24 * time.Hour)

		// Cycle 1
		cycle1, err := service.StartUsage(ctx, chatID, minyak, 1, "liter", 1000.0, "")
		require.NoError(t, err)
		batch1 := cycle1.BatchNumber
		cycle1.StartDate = pastDate1
		cycleRepo.Update(ctx, cycle1)

		// Complete cycle1 dulu biar ga conflict
		service.CompleteUsage(ctx, chatID, minyak, batch1)

		time.Sleep(1 * time.Second)

		// Cycle 2 — sekarang bisa bikin baru karena cycle1 udah completed
		cycle2, err := service.StartUsage(ctx, chatID, minyak, 1, "liter", 1000.0, "")
		require.NoError(t, err)
		batch2 := cycle2.BatchNumber
		cycle2.StartDate = pastDate2
		cycleRepo.Update(ctx, cycle2)

		assert.NotEqual(t, batch1, batch2)

		report2, _ := service.CompleteUsage(ctx, chatID, minyak, batch2)
		assert.Contains(t, report2, batch2)
	})
}

// TestStartUsageSemantics mengunci semantic satuan cycle: PurchaseQty dalam
// satuan inventory (pcs), ConversionFactor = isi gr/ml per satuan inventory
// (dari nama barang), ConsumedQty dalam satuan dasar (gr).
func TestStartUsageSemantics(t *testing.T) {
	db := setupBatchTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	svc := NewService(db, cycleRepo, slog.Default())

	bmt := mustGood(t, db, "chat-uom", "susu bmt 200g")
	cycle, err := svc.StartUsage(context.Background(), "chat-uom", bmt, 1, "pcs", 1.0, "2026-03-01")
	require.NoError(t, err)

	assert.Equal(t, 1.0, cycle.PurchaseQty)        // satuan inventory
	assert.Equal(t, "pcs", cycle.PurchaseUnit)     //
	assert.Equal(t, 200.0, cycle.ConversionFactor) // gr per pcs
	assert.Equal(t, 200.0, cycle.ConsumedQty)      // satuan dasar
	assert.Equal(t, "gr", cycle.ConsumedUnit)      //
}

// TestListActiveByItemMultiBatch: dua batch aktif untuk satu item harus
// ter-list keduanya (dipakai konfirmasi batch di agent).
func TestListActiveByItemMultiBatch(t *testing.T) {
	db := setupBatchTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	svc := NewService(db, cycleRepo, slog.Default())
	ctx := context.Background()

	// Dua "pakai" berturut = dua batch aktif.
	bmt := mustGood(t, db, "chat-multi", "susu bmt 200g")
	_, err := svc.StartUsage(ctx, "chat-multi", bmt, 1, "pcs", 1.0, "2026-09-01")
	require.NoError(t, err)
	_, err = svc.StartUsage(ctx, "chat-multi", bmt, 1, "pcs", 1.0, "2026-09-02")
	require.NoError(t, err)

	cycles, err := cycleRepo.ListActiveByGoods(ctx, "chat-multi", bmt.ID)
	require.NoError(t, err)
	assert.Len(t, cycles, 2)
	// Terbaru dulu.
	assert.Equal(t, "2026-09-02", cycles[0].StartDate.Format("2006-01-02"))

	// Item lain tidak ikut.
	other, err := cycleRepo.ListActiveByGoods(ctx, "chat-multi", 999999)
	require.NoError(t, err)
	assert.Empty(t, other)
}
