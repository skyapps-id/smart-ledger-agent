package consumption

import (
	"testing"
	"time"

	"smart-ledger-agent/internal/domain"
)

// Test untuk analisa konsumsi dengan data yang realistis
func TestConsumptionAnalysisRealScenario(t *testing.T) {
	t.Log("🧪 Test Scenario: Analisa konsumsi popok dengan data real")

	// Scenario:
	// - 01/08: Beli 20 pcs popok
	// - 02/08: Beli 21 pcs popok
	// - 03/08: Pakai 5 pcs
	// - 05/08: Pakai 8 pcs
	// - 08/08: Pakai 3 pcs
	// - 11/08: Pakai 5 pcs
	// Total: 41 masuk, 21 keluar dalam periode 01/08-12/08

	// Mock data untuk simulation
	testMoves := []struct {
		date       time.Time
		changeType domain.ChangeType
		quantity   float64
		itemName   string
		unit       string
	}{
		{time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), domain.StockIn, 20, "popok", "pcs"},
		{time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), domain.StockIn, 21, "popok", "pcs"},
		{time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), domain.StockOut, 5, "popok", "pcs"},
		{time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC), domain.StockOut, 8, "popok", "pcs"},
		{time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC), domain.StockOut, 3, "popok", "pcs"},
		{time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC), domain.StockOut, 5, "popok", "pcs"},
	}

	t.Logf("📊 Test Data:")
	for _, move := range testMoves {
		action := "masuk"
		if move.changeType == domain.StockOut {
			action = "keluar"
		}
		t.Logf("   %s: %s %g %s", move.date.Format("02/01/2006"), action, move.quantity, move.unit)
	}

	// Hitung expected values
	expectedTotalIn := 41.0
	expectedTotalOut := 21.0
	expectedFirstIn := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	expectedLastOut := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	expectedAnalysisDuration := 11.0 // hari (01/08-12/08)
	expectedDailyRate := 21.0 / 11.0 // ~1.91 pcs/hari

	t.Logf("📈 Expected Results:")
	t.Logf("   Total masuk: %g pcs", expectedTotalIn)
	t.Logf("   Total keluar: %g pcs", expectedTotalOut)
	t.Logf("   First transaction: %s", expectedFirstIn.Format("02/01/2006"))
	t.Logf("   Last transaction: %s", expectedLastOut.Format("02/01/2006"))
	t.Logf("   Analysis duration: %.0f hari", expectedAnalysisDuration)
	t.Logf("   Daily rate: %.2f pcs/hari", expectedDailyRate)

	// Test actual logic
	type itemData struct {
		name        string
		unit        string
		totalIn     float64
		totalOut    float64
		firstInDate time.Time
		lastOutDate time.Time
		daysInUse   float64
	}

	data := &itemData{}
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	for _, move := range testMoves {
		switch move.changeType {
		case domain.StockIn:
			data.totalIn += move.quantity
			if data.firstInDate.IsZero() || move.date.Before(data.firstInDate) {
				data.firstInDate = move.date
			}
		case domain.StockOut:
			data.totalOut += move.quantity
			if data.lastOutDate.IsZero() || move.date.After(data.lastOutDate) {
				data.lastOutDate = move.date
			}
		}
	}

	// Calculate duration
	analysisDuration := to.Sub(from).Hours() / 24
	if analysisDuration > 0 && data.totalOut > 0 {
		data.daysInUse = analysisDuration
	}

	dailyRate := data.totalOut / data.daysInUse

	t.Logf("🔍 Actual Results:")
	t.Logf("   Total masuk: %g pcs", data.totalIn)
	t.Logf("   Total keluar: %g pcs", data.totalOut)
	t.Logf("   First transaction: %s", data.firstInDate.Format("02/01/2006"))
	t.Logf("   Last transaction: %s", data.lastOutDate.Format("02/01/2006"))
	t.Logf("   Analysis duration: %.0f hari", data.daysInUse)
	t.Logf("   Daily rate: %.2f pcs/hari", dailyRate)

	// Validations
	if data.totalIn != expectedTotalIn {
		t.Errorf("Total in expected %.0f, got %.0f", expectedTotalIn, data.totalIn)
	}
	if data.totalOut != expectedTotalOut {
		t.Errorf("Total out expected %.0f, got %.0f", expectedTotalOut, data.totalOut)
	}
	if !data.firstInDate.Equal(expectedFirstIn) {
		t.Errorf("First in expected %s, got %s", expectedFirstIn.Format("02/01/2006"), data.firstInDate.Format("02/01/2006"))
	}
	if !data.lastOutDate.Equal(expectedLastOut) {
		t.Errorf("Last out expected %s, got %s", expectedLastOut.Format("02/01/2006"), data.lastOutDate.Format("02/01/2006"))
	}
	if data.daysInUse != expectedAnalysisDuration {
		t.Errorf("Days in use expected %.0f, got %.0f", expectedAnalysisDuration, data.daysInUse)
	}

	// Test the specific issue mentioned by user
	t.Log("🐛 Issue: User reports 'Transaksi: 11/08/2026 → 11/08/2026' (only one day)")
	t.Log("   Expected: 'Transaksi: 01/08/2026 → 11/08/2026' (full range)")
	t.Logf("   Actual: Transaksi: %s → %s", data.firstInDate.Format("02/01/2006"), data.lastOutDate.Format("02/01/2006"))

	if !data.firstInDate.IsZero() && !data.lastOutDate.IsZero() {
		transactionRange := data.lastOutDate.Sub(data.firstInDate).Hours() / 24
		t.Logf("   Transaction range: %.0f hari", transactionRange)

		if transactionRange == 0 {
			t.Error("BUG: Transaction range is 0 days! This explains the user's issue.")
		}
	}
}
