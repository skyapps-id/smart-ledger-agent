package transaction

import (
	"context"
	"log/slog"
	"testing"

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
		consumptionService: consumption.NewService(db, cycleRepo, slog.Default()),
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
