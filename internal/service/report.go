package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/repository"
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
		"persediaan", "persedian", "inventaris", "inventori", // typo & variasi
		"per item", "per barang", "beli apa",
		"pemakaian", "yang dipakai", "pakai apa", "dipakai",
		"stok keluar", "stok masuk", "keluar apa",
		"barang apa", "punya barang", "daftar stok", "daftar barang",
		"analisa", "analisis", "rate konsumsi", "rate pemakaian",
	}
	for _, m := range markers {
		if strings.Contains(t, m) {
			return true
		}
	}
	switch t {
	case "stok", "pemasukan", "pengeluaran", "saldo", "mutasi", "ringkasan",
	     "persediaan", "persedian", "inventaris", "inventori":
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
	metricConsumptionAnalysis // analisa rate konsumsi dari data pembelian + pemakaian
)

func parseMetric(text string) reportMetric {
	t := strings.ToLower(text)
	// Analisa konsumsi dicek paling awal
	if anyContains(t, "analisa", "analisis", "analisa pemakaian", "analisa konsumsi",
		"rate konsumsi", "rate pemakaian", "kecepatan habis", "perhitungan habis") {
		return metricConsumptionAnalysis
	}
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
	if anyContains(t, "stok", "sisa", "persediaan", "persedian", "inventaris", "inventori",
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

// parseItemFilter mencoba mengekstrak nama item dari query untuk filtering
// Contoh: "Analisa konsumsi popok 01/08 hingga 11/08" → "popok"
// Contoh: "stok kecap" → "kecap"
func parseItemFilter(text string) string {
	t := strings.ToLower(text)
	
	// Cek pattern analisa + item + tanggal
	re := `analisa(?:\s+konsumsi|\s+analisis)?\s+([a-zA-Z0-9\s]+?)(?:\s+\d{1,2}/\d{1,2})`
	matches := regexp.MustCompile(re).FindStringSubmatch(t)
	
	if len(matches) >= 2 {
		itemName := strings.TrimSpace(matches[1])
		// Filter out common words yang bukan nama item
		if !anyContains(itemName, "hari", "minggu", "bulan", "tahun", "lalu", "ini", "kemarin") {
			return itemName
		}
	}
	
	// Cek pattern stok + item atau barang + item
	re = `(?:stok|barang|persediaan|persedian|inventaris|inventori)\s+([a-zA-Z0-9\s]+?)(?:\s+|$| hari ini| kemarin| minggu ini| bulan ini| apa| apa aja| saja)$`
	matches = regexp.MustCompile(re).FindStringSubmatch(t)
	
	if len(matches) >= 2 {
		itemName := strings.TrimSpace(matches[1])
		// Filter out common words dan question patterns
		if anyContains(itemName, "hari", "minggu", "bulan", "tahun", "lalu", "ini", "kemarin", "apa", "aja", "saja", "saya", "punya", "kami", "kita") {
			return "" // Return empty untuk show all items
		}
		// Return item name untuk specific filter
		return itemName
	}
	
	return ""
}

func anyContains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// parseCustomDateRange mencoba parse custom date range seperti "01/08 hingga 11/08"
// Mengembalikan nil bila tidak ada pattern match
func parseCustomDateRange(text string) *period {
	// Pattern: DD/MM hingga DD/MM atau DD/MM sampai DD/MM atau DD/MM - DD/MM
	// Use regular dash instead of em dash to avoid regex escape issues
	re := `(\d{1,2})/(\d{1,2})\s*(?:hingga|sampai|-)\s*(\d{1,2})/(\d{1,2})`
	matches := regexp.MustCompile(re).FindStringSubmatch(text)
	
	if len(matches) < 5 {
		return nil
	}
	
	// matches[1] = hari from, matches[2] = bulan from
	// matches[3] = hari to, matches[4] = bulan to
	fromDay, _ := strconv.Atoi(matches[1])
	fromMonth, _ := strconv.Atoi(matches[2])
	toDay, _ := strconv.Atoi(matches[3])
	toMonth, _ := strconv.Atoi(matches[4])
	
	year := time.Now().Year()
	
	fromDate := time.Date(year, time.Month(fromMonth), fromDay, 0, 0, 0, 0, time.Now().Location())
	toDate := time.Date(year, time.Month(toMonth), toDay, 23, 59, 59, 0, time.Now().Location())
	
	label := fmt.Sprintf("%s - %s", fromDate.Format("02/01/2006"), toDate.Format("02/01/2006"))
	
	return &period{
		from:       fromDate,
		to:         toDate,
		label:      label,
		itemFilter: "",
	}
}

type period struct {
	from       time.Time
	to         time.Time
	label      string
	itemFilter string // filter analisa per item tertentu (opsional)
}

// parsePeriod menerjemahkan ekspresi waktu Indonesia menjadi rentang [from, to].
// Default: hari ini.
func parsePeriod(text string) period {
	now := time.Now()
	t := strings.ToLower(text)
	todayStart := startOfDay(now)

	// Cek custom date range dulu (format: "01/08 hingga 11/08" atau "01/08 sampai 11/08")
	if customRange := parseCustomDateRange(t); customRange != nil {
		return *customRange
	}

	switch {
	case strings.Contains(t, "bulan lalu"):
		firstThis := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		from := firstThis.AddDate(0, -1, 0)
		return period{from: from, to: firstThis.Add(-time.Second), label: formatMonth(from), itemFilter: ""}
	case strings.Contains(t, "minggu lalu"):
		thisWeek := startOfWeek(now)
		from := thisWeek.AddDate(0, 0, -7)
		return period{from: from, to: thisWeek.Add(-time.Second), label: "minggu lalu", itemFilter: ""}
	case strings.Contains(t, "kemarin"):
		from := todayStart.AddDate(0, 0, -1)
		return period{from: from, to: from.Add(24*time.Hour - time.Second), label: formatDay(from), itemFilter: ""}
	case strings.Contains(t, "minggu ini"):
		from := startOfWeek(now)
		return period{from: from, to: now, label: "minggu ini", itemFilter: ""}
	case strings.Contains(t, "bulan ini"):
		from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return period{from: from, to: now, label: "bulan ini", itemFilter: ""}
	case strings.Contains(t, "semua") || strings.Contains(t, "seluruh") || strings.Contains(t, "sejauh"):
		return period{from: time.Time{}, to: now, label: "sejauh ini", itemFilter: ""}
	default:
		return period{from: todayStart, to: now, label: "hari ini (" + formatDay(todayStart) + ")", itemFilter: ""}
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
		// Extract item filter untuk query stok spesifik per item
		itemFilter := parseItemFilter(msg.Text)
		
		var items []domain.Inventory
		var err error
		
		if itemFilter != "" {
			// Query spesifik item yang diminta
			items, err = a.invRepo.WithTx(a.db).ListByChat(ctx, msg.ChatID)
			if err != nil {
				a.log.ErrorContext(ctx, "gagal query stok", "err", err)
				return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil data stok.")
			}
			// Filter items yang match dengan itemFilter
			filteredItems := make([]domain.Inventory, 0)
			for _, item := range items {
				if strings.Contains(strings.ToLower(item.ItemName), strings.ToLower(itemFilter)) {
					filteredItems = append(filteredItems, item)
				}
			}
			items = filteredItems
		} else {
			// Query semua stok
			items, err = a.invRepo.WithTx(a.db).ListByChat(ctx, msg.ChatID)
			if err != nil {
				a.log.ErrorContext(ctx, "gagal query stok", "err", err)
				return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil data stok.")
			}
		}
		
		return a.reply(ctx, msg.ChatID, formatStock(items, itemFilter))
	}

	p := parsePeriod(msg.Text)

	// Extract item filter untuk analisa spesifik per item
	if metric == metricConsumptionAnalysis {
		itemFilter := parseItemFilter(msg.Text)
		if itemFilter != "" {
			p.itemFilter = itemFilter
		}
	}

	switch metric {
	case metricConsumptionAnalysis:
		analysis, err := generateConsumptionAnalysis(ctx, a.db, a.logRepo, a.invRepo, msg.ChatID, p.from, p.to, p.itemFilter)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal generate analisa konsumsi", "err", err)
			return a.reply(ctx, msg.ChatID, "Maaf, gagal membuat analisa konsumsi.")
		}
		return a.reply(ctx, msg.ChatID, analysis)
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
