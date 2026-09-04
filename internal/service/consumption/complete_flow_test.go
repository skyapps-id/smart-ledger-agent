// Package service tests untuk flow lengkap beli → pakai → habis.
package consumption

import (
	"context"
	"errors"
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

func setupCompleteFlowTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(
		&domain.ConsumptionCycle{},
		&domain.Chat{},
		&domain.Transaction{},
		&domain.Inventory{},
		&domain.StockLog{},
	)
	require.NoError(t, err)

	return db
}

func TestCompleteConsumptionFlow(t *testing.T) {
	db := setupCompleteFlowTestDB(t)

	// Setup repositories
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	txnRepo := repository.NewTransactionRepository(db)
	invRepo := repository.NewInventoryRepository(db)
	logRepo := repository.NewStockLogRepository(db)

	logger := slog.Default()
	consumptionService := NewService(db, cycleRepo, logger)

	ctx := context.Background()
	chatID := "test-chat-flow"
	userPhone := "62812345678"

	// Setup chat
	chat := &domain.Chat{
		ChatID:      chatID,
		Name:        "Test Chat",
		Initialized: true,
	}
	require.NoError(t, db.Create(chat).Error)

	t.Run("Flow lengkap: Beli → Pakai → Habis", func(t *testing.T) {
		// STEP 1: BELI (masuk stock + keuangan)
		t.Run("Step 1 - Pembelian susu 5 kaleng", func(t *testing.T) {
			purchaseDate := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

			// Catat sebagai expense (RFC §5.1)
			txn := &domain.Transaction{
				ChatID:          chatID,
				SenderPhone:     userPhone,
				Type:            domain.TransactionExpense,
				Category:        "Belanja",
				ItemName:        "Susu 400gr",
				Amount:          75000, // Rp 75.000
				RawPayload:      "beli susu 400gr 5 kaleng",
				TransactionDate: purchaseDate,
			}
			require.NoError(t, txnRepo.Create(ctx, txn))

			// Tambah ke inventory via AddStock
			_, err := invRepo.AddStock(ctx, chatID, "Susu 400gr", 5, "kaleng")
			require.NoError(t, err)

			// Verifikasi
			stok, err := invRepo.GetByChatItem(ctx, chatID, "Susu 400gr")
			require.NoError(t, err)
			assert.Equal(t, 5.0, stok.StockQty)
		})

		// STEP 2: PAKAI (stock berkurang + consumption cycle OPEN)
		t.Run("Step 2 - Mulai pakai susu", func(t *testing.T) {
			// Simulasi 2 hari setelah beli
			time.Sleep(10 * time.Millisecond) // Small delay untuk StartDate yang berbeda

			// Start consumption cycle
			cycle, err := consumptionService.StartUsage(ctx, chatID, "Susu 400gr", 1, "kaleng", 400.0, "")
			require.NoError(t, err)
			assert.Equal(t, domain.ConsumptionCycleActive, cycle.Status)
			assert.Equal(t, "Susu 400gr", cycle.ItemName)
			_ = cycle // suppress unused warning

			// Kurangi stok (simulasi user mengambil 1 kaleng)
			inv, err := invRepo.GetByChatItem(ctx, chatID, "Susu 400gr")
			require.NoError(t, err)
			require.NoError(t, invRepo.DecreaseStock(ctx, inv.ID, 1))

			// Log stock OUT
			stockLog := &domain.StockLog{
				InventoryID: inv.ID,
				ChangeType:  domain.StockOut,
				Quantity:    1,
				Notes:       "Mulai pemakaian",
			}
			require.NoError(t, logRepo.Create(ctx, stockLog))

			// Verifikasi stok berkurang
			updatedInv, err := invRepo.GetByChatItem(ctx, chatID, "Susu 400gr")
			require.NoError(t, err)
			assert.Equal(t, 4.0, updatedInv.StockQty) // 5 - 1 = 4
			_ = updatedInv                            // suppress unused warning
		})

		// STEP 3: HABIS (consumption cycle COMPLETED + laporan)
		t.Run("Step 3 - Susu habis", func(t *testing.T) {
			// Simulasi 12 hari kemudian
			time.Sleep(10 * time.Millisecond)

			// Complete consumption cycle
			report, err := consumptionService.CompleteUsage(ctx, chatID, "Susu 400gr", "")
			require.NoError(t, err)

			// Verifikasi report format
			assert.Contains(t, report, "Susu 400gr")
			assert.Contains(t, report, "sudah habis!")
			assert.Contains(t, report, "⏰ Durasi:")
			assert.Contains(t, report, "📊 Total: 400 gr")
			assert.Contains(t, report, "📈 Rate:")

			// Verifikasi cycle status - tidak boleh ada active cycle
			_, err = cycleRepo.GetActiveByItem(ctx, chatID, "Susu 400gr")
			assert.Error(t, err) // Should not find active cycle
			assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))

			// Get completed cycle
			cycles, err := cycleRepo.ListByChat(ctx, chatID, 10)
			require.NoError(t, err)
			assert.Len(t, cycles, 1)

			completedCycle := cycles[0]
			assert.Equal(t, domain.ConsumptionCycleCompleted, completedCycle.Status)
			assert.NotNil(t, completedCycle.EndDate)
		})
	})

	t.Run("Flow dengan multiple items", func(t *testing.T) {
		// Setup multiple items
		items := []struct {
			name  string
			qty   float64
			unit  string
			conv  float64
			price float64
		}{
			{"Kopi 1kg", 1, "kg", 1000.0, 150000},
			{"Teh 250gr", 1, "bungkus", 250.0, 35000},
			{"Gula 1kg", 1, "kg", 1000.0, 20000},
		}

		// STEP 1: Beli semua items
		for _, item := range items {
			txn := &domain.Transaction{
				ChatID:          chatID,
				SenderPhone:     userPhone,
				Type:            domain.TransactionExpense,
				Category:        "Belanja",
				ItemName:        item.name,
				Amount:          item.price,
				RawPayload:      "beli " + item.name,
				TransactionDate: time.Now(),
			}
			require.NoError(t, txnRepo.Create(ctx, txn))

			// Tambah ke inventory via AddStock
			_, err := invRepo.AddStock(ctx, chatID, item.name, item.qty, item.unit)
			require.NoError(t, err)
		}

		// STEP 2: Pakai items
		for _, item := range items {
			_, err := consumptionService.StartUsage(ctx, chatID, item.name, item.qty, item.unit, item.conv, "")
			require.NoError(t, err)

			inv, err := invRepo.GetByChatItem(ctx, chatID, item.name)
			require.NoError(t, err)
			require.NoError(t, invRepo.DecreaseStock(ctx, inv.ID, item.qty))
		}

		// STEP 3: Complete semua
		for _, item := range items {
			report, err := consumptionService.CompleteUsage(ctx, chatID, item.name, "")
			require.NoError(t, err)
			assert.Contains(t, report, "sudah habis!")
			assert.Contains(t, report, "/hari")
		}

		// Verify semua cycles completed
		cycles, err := cycleRepo.ListByChat(ctx, chatID, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(cycles), 4) // 1 dari test sebelumnya + 3 baru
	})
}

func TestStartUsageErrors(t *testing.T) {
	db := setupCompleteFlowTestDB(t)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	logger := slog.Default()
	service := NewService(db, cycleRepo, logger)

	ctx := context.Background()
	chatID := "test-chat-errors"

	t.Run("CompleteUsage tanpa active cycle", func(t *testing.T) {
		_, err := service.CompleteUsage(ctx, chatID, "NonExistent Item", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "tidak ada siklus aktif")
	})
}
