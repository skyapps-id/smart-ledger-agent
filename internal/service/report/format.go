package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"smart-ledger-agent/internal/domain"
	repomodel "smart-ledger-agent/internal/repository/model"
	"smart-ledger-agent/internal/service/agent"
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
				fmt.Fprintf(&b, "- %s: Rp%s\n", c, agent.FormatRupiah(s.ByCategory[c]))
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
	fmt.Fprintf(b, "%s: Rp%s\n", label, agent.FormatRupiah(amount))
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
		fmt.Fprintf(&b, "- %s: Rp%s (%dx)\n", it.ItemName, agent.FormatRupiah(it.Amount), it.Count)
		total += it.Amount
	}
	fmt.Fprintf(&b, "Total: Rp%s", agent.FormatRupiah(total))
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

// formatPeriodRange format periode untuk display
func formatPeriodRange(from, to time.Time) string {
	if from.IsZero() {
		return "sejauh ini"
	}
	return fmt.Sprintf("%s - %s", from.Format("02/01/2006"), to.Format("02/01/2006"))
}
