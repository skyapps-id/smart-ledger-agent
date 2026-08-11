package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	repomodel "smart-ledger-agent/internal/repository/model"
)

// ── Intent & parsing ──

// isReportQuery mendeteksi apakah pesan adalah permintaan laporan
// (bukan instruksi pencatatan).
func isReportQuery(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if strings.HasSuffix(t, "?") || strings.HasPrefix(t, "?") {
		return true
	}
	markers := []string{
		"berapa", "ringkas", "laporan", "riwayat", "rincian", "mutasi",
		"sisa", "stok apa", "ada stok", "stok di", "stok dirumah",
		"per item", "per barang", "beli apa",
		"pemakaian", "yang dipakai", "pakai apa", "dipakai",
		"stok keluar", "stok masuk", "keluar apa",
		"barang apa", "punya barang", "daftar stok", "daftar barang",
	}
	for _, m := range markers {
		if strings.Contains(t, m) {
			return true
		}
	}
	switch t {
	case "stok", "pemasukan", "pengeluaran", "saldo", "mutasi", "ringkasan":
		return true
	}
	// Perintah tampil di awal kalimat.
	for _, p := range []string{"lihat", "tampilkan", "tunjukkan", "cek"} {
		if strings.HasPrefix(t, p+" ") {
			return true
		}
	}
	return false
}

type reportMetric int

const (
	metricSummary reportMetric = iota
	metricIncome
	metricExpense
	metricStock
	metricExpenseByItem
	metricConsumption
)

func parseMetric(text string) reportMetric {
	t := strings.ToLower(text)
	// Pemakaian dicek sebelum stok/expense karena "stok keluar" & "pakai"
	// dapat bentrok dengan kata lain.
	if anyContains(t, "pemakaian", "pemakai", "dipakai", "stok keluar",
		"riwayat pakai", "barang dipakai", "yang dipakai", "pakai barang",
		"pakai apa", "keluar apa", "stok yang keluar") {
		return metricConsumption
	}
	if anyContains(t, "per item", "per barang", "tiap item", "masing-masing",
		"rincian belanja", "beli apa") {
		return metricExpenseByItem
	}
	if anyContains(t, "stok", "sisa", "persediaan", "inventaris",
		"barang apa", "punya barang", "daftar barang", "ada barang") {
		return metricStock
	}
	if anyContains(t, "pemasukan", "pendapatan", "masuk") {
		return metricIncome
	}
	if anyContains(t, "pengeluaran", "biaya", "keluar") {
		return metricExpense
	}
	return metricSummary
}

func anyContains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

type period struct {
	from  time.Time
	to    time.Time
	label string
}

// parsePeriod menerjemahkan ekspresi waktu Indonesia menjadi rentang [from, to].
// Default: hari ini.
func parsePeriod(text string) period {
	now := time.Now()
	t := strings.ToLower(text)
	todayStart := startOfDay(now)

	switch {
	case strings.Contains(t, "bulan lalu"):
		firstThis := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		from := firstThis.AddDate(0, -1, 0)
		return period{from, firstThis.Add(-time.Second), formatMonth(from)}
	case strings.Contains(t, "minggu lalu"):
		thisWeek := startOfWeek(now)
		from := thisWeek.AddDate(0, 0, -7)
		return period{from, thisWeek.Add(-time.Second), "minggu lalu"}
	case strings.Contains(t, "kemarin"):
		from := todayStart.AddDate(0, 0, -1)
		return period{from, from.Add(24*time.Hour - time.Second), formatDay(from)}
	case strings.Contains(t, "minggu ini"):
		from := startOfWeek(now)
		return period{from, now, "minggu ini"}
	case strings.Contains(t, "bulan ini"):
		from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return period{from, now, "bulan ini"}
	case strings.Contains(t, "semua") || strings.Contains(t, "seluruh") || strings.Contains(t, "sejauh"):
		return period{time.Time{}, now, "sejauh ini"}
	default:
		return period{todayStart, now, "hari ini (" + formatDay(todayStart) + ")"}
	}
}

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

// ── Orchestration ──

// handleReport menjalankan path laporan: baca DB -> format -> balas.
func (a *Agent) handleReport(ctx context.Context, msg entity.IncomingMessage) error {
	metric := parseMetric(msg.Text)

	// Stok saat ini: tanpa konteks waktu.
	if metric == metricStock {
		items, err := a.invRepo.WithTx(a.db).ListByChat(ctx, msg.ChatID)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query stok", "err", err)
			return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil data stok.")
		}
		return a.reply(ctx, msg.ChatID, formatStock(items))
	}

	p := parsePeriod(msg.Text)

	switch metric {
	case metricExpenseByItem:
		items, err := a.txnRepo.WithTx(a.db).ExpenseByItem(ctx, msg.ChatID, p.from, p.to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query per item", "err", err)
			return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil laporan.")
		}
		return a.reply(ctx, msg.ChatID, formatExpenseByItem(p, items))
	case metricConsumption:
		moves, err := a.logRepo.WithTx(a.db).MovementsByChat(ctx, msg.ChatID, p.from, p.to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query pemakaian", "err", err)
			return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil laporan.")
		}
		return a.reply(ctx, msg.ChatID, formatConsumption(p, moves))
	default:
		summary, err := a.txnRepo.WithTx(a.db).Summary(ctx, msg.ChatID, p.from, p.to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query ringkasan", "err", err)
			return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil laporan.")
		}
		return a.reply(ctx, msg.ChatID, formatTxnReport(metric, p, summary))
	}
}

// ── Formatting ──

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

func formatStock(items []domain.Inventory) string {
	if len(items) == 0 {
		return "Belum ada barang di inventaris."
	}
	var b strings.Builder
	b.WriteString("Stok saat ini:\n")
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
