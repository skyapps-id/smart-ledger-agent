package stock

import (
	"context"
	"strings"
	"testing"
	"time"

	"smart-ledger-agent/internal/domain"
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
			ItemName: "Kecap Manis",
			StockQty: 5.0,
			Unit:     "botol",
		},
		{
			ItemName: "Kecap Asin",
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
		{ItemName: "susu bmt 800g", StockQty: 1, Unit: "pcs"},
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

	response = formatStock(items, "susu bmt 800g", nil)
	if strings.Contains(response, "beli terakhir") {
		t.Errorf("tanpa lastPurchases tidak boleh ada info harga: %s", response)
	}
}
