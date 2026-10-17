package transaction

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/sender"
	"smart-ledger-agent/internal/service/agent"
	"smart-ledger-agent/internal/service/consumption"
)

// mockExtractor mengembalikan Extraction tetap (meniru hop LLM).
type mockExtractor struct {
	ext domain.Extraction
}

func (m *mockExtractor) Extract(ctx context.Context, systemPrompt, rawText, sessionID string) (domain.Extraction, llm.Usage, error) {
	return m.ext, llm.Usage{}, nil
}

type txnMockSender struct{ msgs []string }

func (s *txnMockSender) Enqueue(msg sender.Message) bool {
	s.msgs = append(s.msgs, msg.Text)
	return true
}

func setupTxnAgentTest(t *testing.T, ext domain.Extraction) (*transactionAgent, *gorm.DB, *txnMockSender) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&domain.Chat{}, &domain.Good{}, &domain.Transaction{},
		&domain.Inventory{}, &domain.StockLog{}, &domain.ConsumptionCycle{},
	))

	cycleRepo := repository.NewConsumptionCycleRepository(db)
	ag := &transactionAgent{
		db:                 db,
		txnRepo:            repository.NewTransactionRepository(db),
		goodsRepo:          repository.NewGoodsRepository(db),
		invRepo:            repository.NewInventoryRepository(db),
		logRepo:            repository.NewStockLogRepository(db),
		consumptionService: consumption.NewService(cycleRepo, slog.Default()),
		llm:                &mockExtractor{ext: ext},
		extractionPrompt:   transactionSystemPrompt,
		pending:            agent.NewPendingConfirms(),
		sender:             &txnMockSender{},
		log:                slog.Default(),
	}
	return ag, db, ag.sender.(*txnMockSender)
}

// TestExpenseStockUnitFromMaster: satuan inventory HARUS dari master goods
// (galon), bukan dari ekstraksi LLM (default "pcs") — master-first.
func TestExpenseStockUnitFromMaster(t *testing.T) {
	ag, _, senderMock := setupTxnAgentTest(t, domain.Extraction{
		Type:     domain.ExtractionExpense,
		Category: "MINUMAN",
		ItemName: "air aqua galon",
		Quantity: 1,
		Unit:     "pcs", // LLM default — HARUS kalah dari master
		Amount:   0,
	})
	ctx := context.Background()

	galon, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "air aqua galon", "galon")
	require.NoError(t, err)
	require.NoError(t, ag.goodsRepo.UpdateConversion(ctx, galon.ID, "lt", 15))

	err = ag.Handle(ctx, agent.Request{
		Message: entity.IncomingMessage{ChatID: "c1", Text: "beli air aqua galon"},
		Chat:    &domain.Chat{ChatID: "c1", Initialized: true},
		Action:  domain.ServiceAction{Action: domain.ActionRecordTransaction},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "+1 galon")

	inv, err := ag.invRepo.GetByChatGoods(ctx, "c1", galon.ID)
	require.NoError(t, err)
	assert.Equal(t, "galon", inv.Unit, "unit inventory harus dari master, bukan ekstraksi LLM")
	assert.Equal(t, float64(1), inv.StockQty)
}

// TestExpenseNonStockMasterSkipsInventory: flag affects_stock=false di
// master goods (jasa/BBM) → pembelian hanya tercatat keuangan, TANPA row
// inventory — keputusan dari master, bukan dari ekstraksi LLM.
func TestExpenseNonStockMasterSkipsInventory(t *testing.T) {
	ag, _, senderMock := setupTxnAgentTest(t, domain.Extraction{
		Type:     domain.ExtractionExpense,
		Category: "TRANSPORT",
		ItemName: "bensin mobil",
		Quantity: 1,
		Unit:     "ltr",
		Amount:   50000,
	})
	ctx := context.Background()

	g, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "bensin mobil", "ltr")
	require.NoError(t, err)
	require.NoError(t, ag.goodsRepo.UpdateAffectsStock(ctx, g.ID, false))

	err = ag.Handle(ctx, agent.Request{
		Message: entity.IncomingMessage{ChatID: "c1", Text: "beli bensin 50rb"},
		Chat:    &domain.Chat{ChatID: "c1", Initialized: true},
		Action:  domain.ServiceAction{Action: domain.ActionRecordTransaction},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "Pengeluaran tercatat")
	assert.NotContains(t, senderMock.msgs[0], "Stok saat ini")

	_, err = ag.invRepo.GetByChatGoods(ctx, "c1", g.ID)
	assert.Error(t, err, "barang non-stok tidak boleh punya row inventory")
}

// seedPreviousExpense mencatat pembelian sebelumnya (10 hari lalu) agar
// analisa beli ulang punya pembanding.
func seedPreviousExpense(t *testing.T, db *gorm.DB, goodsID int64, name string) {
	t.Helper()
	require.NoError(t, db.Create(&domain.Transaction{
		ChatID: "c1", Type: domain.TransactionExpense, GoodsID: goodsID, ItemName: name,
		Amount: 100000, Quantity: 1, Unit: "pcs", UnitPrice: 100000,
		TransactionDate: time.Now().AddDate(0, 0, -10),
	}).Error)
}

// TestRepurchaseAnalysisOnlyForNonStock: analisa beli ulang hanya untuk
// barang non-stok; barang stok punya modul consumption sendiri.
func TestRepurchaseAnalysisOnlyForNonStock(t *testing.T) {
	t.Run("non-stok tampil analisa", func(t *testing.T) {
		ag, db, senderMock := setupTxnAgentTest(t, domain.Extraction{
			Type: domain.ExtractionExpense, ItemName: "bensin", Quantity: 5, Unit: "liter", Amount: 50000,
		})
		ctx := context.Background()
		g, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "bensin", "liter")
		require.NoError(t, err)
		require.NoError(t, ag.goodsRepo.UpdateAffectsStock(ctx, g.ID, false))
		seedPreviousExpense(t, db, g.ID, "bensin")

		require.NoError(t, ag.Handle(ctx, agent.Request{
			Message: entity.IncomingMessage{ChatID: "c1", Text: "beli bensin 5 liter 50rb"},
			Chat:    &domain.Chat{ChatID: "c1", Initialized: true},
			Action:  domain.ServiceAction{Action: domain.ActionRecordTransaction},
		}))
		require.Len(t, senderMock.msgs, 1)
		assert.Contains(t, senderMock.msgs[0], "Analisa beli ulang")
	})

	t.Run("stok tanpa analisa", func(t *testing.T) {
		ag, db, senderMock := setupTxnAgentTest(t, domain.Extraction{
			Type: domain.ExtractionExpense, ItemName: "beras", Quantity: 1, Unit: "karung", Amount: 100000,
		})
		ctx := context.Background()
		g, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "beras", "karung")
		require.NoError(t, err)
		seedPreviousExpense(t, db, g.ID, "beras")

		require.NoError(t, ag.Handle(ctx, agent.Request{
			Message: entity.IncomingMessage{ChatID: "c1", Text: "beli beras 1 karung 100rb"},
			Chat:    &domain.Chat{ChatID: "c1", Initialized: true},
			Action:  domain.ServiceAction{Action: domain.ActionRecordTransaction},
		}))
		require.Len(t, senderMock.msgs, 1)
		assert.Contains(t, senderMock.msgs[0], "📦 Stok: 1 karung")
		assert.NotContains(t, senderMock.msgs[0], "Analisa beli ulang")
		assert.Contains(t, senderMock.msgs[0], "Harga sebelumnya")
	})
}

func TestPreviousPriceComparison(t *testing.T) {
	last := &domain.Transaction{
		Amount: 95000, Quantity: 1, Unit: "karung", UnitPrice: 95000,
		TransactionDate: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	}
	assert.Empty(t, previousPriceComparison(100000, "karung", nil))
	assert.Equal(t, "\n📈 Harga sebelumnya: Rp95.000/karung (02/09) → naik Rp5.000",
		previousPriceComparison(100000, "karung", last))
	assert.Equal(t, "\n📉 Harga sebelumnya: Rp95.000/karung (02/09) → turun Rp5.000",
		previousPriceComparison(90000, "karung", last))
	assert.Equal(t, "\n💲 Harga sebelumnya: Rp95.000/karung (02/09) → harga sama",
		previousPriceComparison(95000, "karung", last))
	// Satuan beda → tanpa selisih (tidak bisa dibandingkan langsung).
	assert.Equal(t, "\n💲 Harga sebelumnya: Rp95.000/karung (02/09)",
		previousPriceComparison(20000, "kg", last))
	// Row lama tanpa unit_price → tampilkan total.
	assert.Equal(t, "\n💲 Harga sebelumnya: Rp95.000 (02/09)",
		previousPriceComparison(100000, "karung", &domain.Transaction{Amount: 95000, TransactionDate: last.TransactionDate}))
}
