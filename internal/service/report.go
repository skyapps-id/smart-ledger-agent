package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
	repomodel "smart-ledger-agent/internal/repository/model"
)

// ── Report Types & Helpers ──

type reportMetric int

const (
	metricSummary reportMetric = iota
	metricIncome
	metricExpense
	metricStock
	metricExpenseByItem
	metricConsumption
	metricConsumptionAnalysis // analisa rate konsumsi dari data pembelian + pemakaian
)

// period merepresentasikan rentang waktu untuk laporan
type period struct {
	from       time.Time
	to         time.Time
	label      string
	itemFilter string // filter analisa per item tertentu (opsional)
}

// Helper date functions - still needed by formatting functions
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// startOfWeek mengembalikan hari Senin pada minggu yang sama.
func startOfWeek(t time.Time) time.Time {
	w := int(t.Weekday()) // Minggu=0 ... Sabtu=6
	offset := 6
	if w != 0 {
		offset = w - 1
	}
	return startOfDay(t).AddDate(0, 0, -offset)
}

func formatDay(t time.Time) string   { return t.Format("02 Jan 2006") }
func formatMonth(t time.Time) string { return t.Format("Jan 2006") }

// ── Formatting ──

// Note: The old handleReport method has been removed as it is now replaced by 
// LLM-based intent classification and the new generateReport method in agent.go

func formatTxnReport(metric reportMetric, p period, s *repomodel.TxnSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ringkasan %s:\n", p.label)

	switch metric {
	case metricIncome:
		writeLine(&b, "Pemasukan", s.Income)
		if s.OpeningBalance > 0 {
			writeLine(&b, "Saldo awal", s.OpeningBalance)
		}
		if s.Count == 0 {
			b.WriteString("Belum ada transaksi pemasukan.")
		}
	case metricExpense:
		writeLine(&b, "Pengeluaran", s.Expense)
		if s.Expense > 0 {
			b.WriteString("\nRincian per kategori:\n")
			for _, c := range sortedCategories(s.ByCategory) {
				fmt.Fprintf(&b, "- %s: Rp%s\n", c, formatRupiah(s.ByCategory[c]))
			}
		} else {
			b.WriteString("Belum ada pengeluaran.")
		}
	default: // summary
		if s.OpeningBalance > 0 {
			writeLine(&b, "Saldo awal", s.OpeningBalance)
		}
		writeLine(&b, "Pemasukan", s.Income)
		writeLine(&b, "Pengeluaran", s.Expense)
		// Net termasuk saldo awal: mencerminkan running balance periode ini.
		net := s.OpeningBalance + s.Income - s.Expense
		writeLine(&b, "Selisih", net)
		fmt.Fprintf(&b, "Jumlah transaksi: %d", s.Count)
	}
	return b.String()
}

func writeLine(b *strings.Builder, label string, amount float64) {
	fmt.Fprintf(b, "%s: Rp%s\n", label, formatRupiah(amount))
}

func formatStock(items []domain.Inventory, itemFilter string) string {
	if len(items) == 0 {
		if itemFilter != "" {
			return fmt.Sprintf("Tidak ada stok untuk \"%s\".", itemFilter)
		}
		return "Belum ada barang di inventaris."
	}
	var b strings.Builder
	if itemFilter != "" {
		fmt.Fprintf(&b, "Stok saat ini (%s):\n", itemFilter)
	} else {
		b.WriteString("Stok saat ini:\n")
	}
	for _, it := range items {
		fmt.Fprintf(&b, "- %s: %g %s\n", it.ItemName, it.StockQty, it.Unit)
	}
	return b.String()
}

// sortedCategories mengembalikan key map diurutkan menurun berdasar nilai.
func sortedCategories(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
	return keys
}

// formatExpenseByItem merangkai rincian pengeluaran per nama barang.
func formatExpenseByItem(p period, items []repomodel.ItemBreakdown) string {
	if len(items) == 0 {
		return "Belum ada pengeluaran " + p.label + "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Pengeluaran per item (%s):\n", p.label)
	var total float64
	for _, it := range items {
		fmt.Fprintf(&b, "- %s: Rp%s (%dx)\n", it.ItemName, formatRupiah(it.Amount), it.Count)
		total += it.Amount
	}
	fmt.Fprintf(&b, "Total: Rp%s", formatRupiah(total))
	return b.String()
}

// formatConsumption menampilkan total pemakaian stok (OUT) per item pada periode.
func formatConsumption(p period, moves []repomodel.StockMovement) string {
	qty := map[string]float64{}
	unit := map[string]string{}
	var order []string
	for _, m := range moves {
		if m.ChangeType != domain.StockOut {
			continue
		}
		if _, ok := qty[m.ItemName]; !ok {
			order = append(order, m.ItemName)
		}
		qty[m.ItemName] += m.Quantity
		unit[m.ItemName] = m.Unit
	}
	if len(order) == 0 {
		return "Belum ada pemakaian " + p.label + "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Pemakaian barang (%s):\n", p.label)
	for _, name := range order {
		fmt.Fprintf(&b, "- %s: -%g %s\n", name, qty[name], unit[name])
	}
	return b.String()
}

// generateConsumptionAnalysis membuat analisa konsumsi dari data pembelian + pemakaian
func generateConsumptionAnalysis(ctx context.Context, db *gorm.DB, logRepo repository.StockLogRepository, invRepo repository.InventoryRepository, chatID string, from, to time.Time, itemFilter string) (string, error) {
	// Query semua stock movements dalam periode
	moves, err := logRepo.WithTx(db).MovementsByChat(ctx, chatID, from, to)
	if err != nil {
		return "", err
	}

	// Filter by item name jika specified
	if itemFilter != "" {
		filteredMoves := make([]repomodel.StockMovement, 0)
		for _, m := range moves {
			if strings.Contains(strings.ToLower(m.ItemName), strings.ToLower(itemFilter)) {
				filteredMoves = append(filteredMoves, m)
			}
		}
		moves = filteredMoves
	}

	// Group data per item
	type itemData struct {
		name        string
		unit        string
		totalIn     float64
		totalOut    float64
		firstInDate time.Time
		lastOutDate time.Time
		daysInUse   float64
	}

	itemsMap := make(map[string]*itemData)
	var order []string

	for _, m := range moves {
		if _, exists := itemsMap[m.ItemName]; !exists {
			itemsMap[m.ItemName] = &itemData{
				name: m.ItemName,
				unit: m.Unit,
			}
			order = append(order, m.ItemName)
		}
		data := itemsMap[m.ItemName]

		if m.ChangeType == domain.StockIn {
			data.totalIn += m.Quantity
			if data.firstInDate.IsZero() || m.CreatedAt.Before(data.firstInDate) {
				data.firstInDate = m.CreatedAt
			}
		} else if m.ChangeType == domain.StockOut {
			data.totalOut += m.Quantity
			if data.lastOutDate.IsZero() || m.CreatedAt.After(data.lastOutDate) {
				data.lastOutDate = m.CreatedAt
			}
		}
	}

	// Calculate consumption rate dan periode pemakaian
	// Gunakan durasi dari periode analisa (to - from) untuk daily rate yang lebih akurat
	for _, data := range itemsMap {
		// Durasi periode analisa dalam hari
		analysisDuration := to.Sub(from).Hours() / 24
		
		if analysisDuration > 0 && data.totalOut > 0 {
			data.daysInUse = analysisDuration
		} else if !data.firstInDate.IsZero() && !data.lastOutDate.IsZero() && data.totalOut > 0 {
			// Fallback ke durasi actual transaksi bila periode analisa tidak valid
			duration := data.lastOutDate.Sub(data.firstInDate).Hours() / 24
			if duration > 0 {
				data.daysInUse = duration
			}
		}
	}

	if len(order) == 0 {
		if itemFilter != "" {
			return fmt.Sprintf("Belum ada data konsumsi untuk \"%s\" %s.", itemFilter, formatPeriodRange(from, to)), nil
		}
		return "Belum ada data konsumsi " + formatPeriodRange(from, to) + ".", nil
	}

	// Format output analisa
	var b strings.Builder
	if itemFilter != "" {
		fmt.Fprintf(&b, "📊 Analisa Konsumsi: %s (%s):\n\n", itemFilter, formatPeriodRange(from, to))
	} else {
		fmt.Fprintf(&b, "📊 Analisa Konsumsi (%s):\n\n", formatPeriodRange(from, to))
	}

	for _, name := range order {
		data := itemsMap[name]

		fmt.Fprintf(&b, "📦 %s:\n", name)
		fmt.Fprintf(&b, "   Stok masuk: %g %s\n", data.totalIn, data.unit)
		
		if data.totalOut > 0 {
			fmt.Fprintf(&b, "   Stok keluar: %g %s (%.0f%% dari masuk)\n", 
				data.totalOut, data.unit, (data.totalOut/data.totalIn)*100)
			
			if data.daysInUse > 0 {
				dailyRate := data.totalOut / data.daysInUse
				fmt.Fprintf(&b, "   Periode analisa: %.0f hari (%s → %s)\n", 
					data.daysInUse,
					from.Format("02/01/2006"),
					to.Format("02/01/2006"))
				fmt.Fprintf(&b, "   Rate konsumsi: %.2f %s/hari\n", dailyRate, data.unit)
				
				// Tampilkan range tanggal transaksi actual bila berbeda dari periode analisa
				if !data.firstInDate.IsZero() && !data.lastOutDate.IsZero() {
					fmt.Fprintf(&b, "   Transaksi: %s → %s\n",
						data.firstInDate.Format("02/01/2006"),
						data.lastOutDate.Format("02/01/2006"))
				}
				
				// Estimasi kapan stok habis (bila ada sisa)
				remaining := data.totalIn - data.totalOut
				if remaining > 0 && dailyRate > 0 {
					daysUntilEmpty := remaining / dailyRate
					estimatedEmpty := to.AddDate(0, 0, int(daysUntilEmpty))
					timeToEmptyStr := formatDuration(daysUntilEmpty)
					if daysUntilEmpty < 1 {
						timeToEmptyStr = fmt.Sprintf("%.0f jam", daysUntilEmpty*24)
					}
					fmt.Fprintf(&b, "   Sisa stok: %g %s (estimasi habis: %s, %s dari sekarang)\n", 
						remaining, data.unit, estimatedEmpty.Format("02/01/2006"), timeToEmptyStr)
				} else if remaining > 0 {
					fmt.Fprintf(&b, "   Sisa stok: %g %s\n", remaining, data.unit)
				}
			}
		} else {
			fmt.Fprintf(&b, "   Belum ada pemakaian\n")
			
			remaining := data.totalIn - data.totalOut
			if remaining > 0 {
				fmt.Fprintf(&b, "   Sisa stok: %g %s\n", remaining, data.unit)
			}
		}
		fmt.Fprintf(&b, "\n")
	}

	return b.String(), nil
}

// formatPeriodRange format periode untuk display
func formatPeriodRange(from, to time.Time) string {
	if from.IsZero() {
		return "sejauh ini"
	}
	return fmt.Sprintf("%s - %s", from.Format("02/01/2006"), to.Format("02/01/2006"))
}
