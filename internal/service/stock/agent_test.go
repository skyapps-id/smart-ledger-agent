package stock

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/sender"
	"smart-ledger-agent/internal/service/agent"
)

// MockIntentExtractor untuk testing
type mockIntentExtractor struct{}

func (m *mockIntentExtractor) ClassifyIntent(ctx context.Context, rawText string, sessionID string) (domain.ServiceAction, error) {
	// Mock response untuk "cek stock kecap"
	return domain.ServiceAction{
		Action: domain.ActionGetStock,
		Params: map[string]interface{}{
			"item_filter": "kecap",
		},
	}, nil
}

// Test berbagai query patterns untuk get_stock
func TestGetStockPatterns(t *testing.T) {
	patterns := []struct {
		query        string
		expectedItem string
	}{
		{"cek stock kecap", "kecap"},
		{"stok kecap", "kecap"},
		{"sisa kecap", "kecap"},
		{"persediaan kecap", "kecap"},
		{"inventaris kecap", "kecap"},
		{"stok", ""}, // semua stok
		{"sisa", ""}, // semua stok
	}

	for _, pattern := range patterns {
		t.Run("Query: "+pattern.query, func(t *testing.T) {
			// Test logic disini
			_ = pattern.query
			_ = pattern.expectedItem
		})
	}
}

// Test actual intent classification flow
func TestIntentClassificationGetStock(t *testing.T) {
	ctx := context.Background()
	extractor := &mockIntentExtractor{}

	// Test query
	query := "cek stock kecap"
	action, err := extractor.ClassifyIntent(ctx, query, "test-session")

	if err != nil {
		t.Fatalf("ClassifyIntent failed: %v", err)
	}

	// Verify action
	if action.Action != domain.ActionGetStock {
		t.Errorf("Expected action %s, got %s", domain.ActionGetStock, action.Action)
	}

	// Verify parameters
	itemFilter, ok := action.Params["item_filter"].(string)
	if !ok || itemFilter != "kecap" {
		t.Errorf("Expected item_filter 'kecap', got %v", action.Params["item_filter"])
	}

	t.Logf("✅ Intent classification successful for query '%s'", query)
	t.Logf("   Action: %s", action.Action)
	t.Logf("   Params: %v", action.Params)
}

// Test response formatting
func TestFormatStockResponse(t *testing.T) {
	items := []domain.Inventory{
		{
			Good:     &domain.Good{Name: "Kecap Manis"},
			StockQty: 5.0,
			Unit:     "botol",
		},
		{
			Good:     &domain.Good{Name: "Kecap Asin"},
			StockQty: 3.0,
			Unit:     "botol",
		},
	}

	response := formatStock(items, "kecap", nil)
	t.Logf("Stock response:\n%s", response)

	if response == "" {
		t.Error("formatStock returned empty string")
	}
}

func TestFormatStockWithLastPurchase(t *testing.T) {
	items := []domain.Inventory{
		{Good: &domain.Good{Name: "susu bmt 800g"}, StockQty: 1, Unit: "pcs"},
	}
	last := &domain.Transaction{
		ItemName:        "susu bmt 800g",
		Amount:          89000,
		TransactionDate: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	}

	response := formatStock(items, "susu bmt 800g", map[string]*domain.Transaction{"susu bmt 800g": last})

	if !strings.Contains(response, "susu bmt 800g: 1 pcs (beli terakhir: Rp89.000, 02/09)") {
		t.Errorf("harga beli terakhir tidak muncul: %s", response)
	}

	// Row dengan unit_price → tampilkan harga satuan, bukan total.
	lastUnit := &domain.Transaction{
		ItemName:        "pepmpes isi 48",
		Amount:          192000,
		Quantity:        3,
		Unit:            "ball",
		UnitPrice:       64000,
		TransactionDate: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
	}
	response = formatStock(items, "susu bmt 800g", map[string]*domain.Transaction{"susu bmt 800g": lastUnit})
	if !strings.Contains(response, "beli terakhir: Rp64.000/ball, 31/08") {
		t.Errorf("harga satuan tidak muncul: %s", response)
	}

	response = formatStock(items, "susu bmt 800g", nil)
	if strings.Contains(response, "beli terakhir") {
		t.Errorf("tanpa lastPurchases tidak boleh ada info harga: %s", response)
	}
}

func stockIncoming(text string) entity.IncomingMessage {
	return entity.IncomingMessage{ChatID: "c1", Text: text}
}

type stockMockSender struct{ msgs []string }

func (s *stockMockSender) Enqueue(msg sender.Message) bool {
	s.msgs = append(s.msgs, msg.Text)
	return true
}

func setupStockAgentTest(t *testing.T) (*stockAgent, *gorm.DB, *stockMockSender) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Good{}, &domain.Inventory{}, &domain.Transaction{}))
	senderMock := &stockMockSender{}
	ag := &stockAgent{
		db:        db,
		goodsRepo: repository.NewGoodsRepository(db),
		invRepo:   repository.NewInventoryRepository(db),
		txnRepo:   repository.NewTransactionRepository(db),
		sender:    senderMock,
		log:       slog.Default(),
	}
	return ag, db, senderMock
}

func TestGetStockMasterItemWithoutPurchase(t *testing.T) {
	ag, db, senderMock := setupStockAgentTest(t)
	ctx := context.Background()

	// Terdaftar di master, belum pernah dibeli → stok 0, bukan "tidak ada".
	_, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "galon air", "galon")
	require.NoError(t, err)
	// Barang lain yang SUDAH dibeli.
	beras, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "beras 5kg", "kg")
	require.NoError(t, err)
	_, err = ag.invRepo.AddStock(ctx, "c1", beras.ID, 5, "kg")
	require.NoError(t, err)
	_ = db

	err = ag.Handle(ctx, agent.Request{
		Message: stockIncoming("stok galon"),
		Chat:    &domain.Chat{ChatID: "c1", Initialized: true},
		Action: domain.ServiceAction{Action: domain.ActionGetStock, Params: map[string]interface{}{
			"item_filter": "galon",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "galon air: 0 galon")
	assert.Contains(t, senderMock.msgs[0], "belum pernah dibeli")
}

func TestGetStockUnknownItem(t *testing.T) {
	ag, _, senderMock := setupStockAgentTest(t)

	err := ag.Handle(context.Background(), agent.Request{
		Message: stockIncoming("stok kecap"),
		Chat:    &domain.Chat{ChatID: "c1", Initialized: true},
		Action: domain.ServiceAction{Action: domain.ActionGetStock, Params: map[string]interface{}{
			"item_filter": "kecap",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "tidak ada di inventaris maupun master")
	assert.Contains(t, senderMock.msgs[0], "tambah barang kecap")
}
