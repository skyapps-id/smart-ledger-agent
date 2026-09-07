// Package consumption menyediakan business logic untuk consumption cycles
package consumption

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

// Service menangani pembuatan dan analisa consumption cycles.
type Service struct {
	cycleRepo repository.ConsumptionCycleRepository
	log       *slog.Logger
}

func NewService(cycleRepo repository.ConsumptionCycleRepository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cycleRepo: cycleRepo,
		log:       logger,
	}
}

// generateBatchNumber membuat batch number otomatis dengan format: MMM-DD-HHmmss
func generateBatchNumber() string {
	now := time.Now()
	months := []string{"JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"}
	month := months[int(now.Month())-1]
	return fmt.Sprintf("%s-%02d-%02d%02d%02d", month, now.Day(), now.Hour(), now.Minute(), now.Second())
}

// parseUsageDate parse tanggal dari berbagai format (YYYY-MM-DD, DD/MM, DD/MM/YYYY)
func parseUsageDate(dateStr string) (time.Time, error) {
	// YYYY-MM-DD
	if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
		return parsed, nil
	}
	// DD/MM/YYYY
	if parsed, err := time.Parse("02/01/2006", dateStr); err == nil {
		return parsed, nil
	}
	// DD/MM (tahun sekarang)
	if parsed, err := time.Parse("02/01", dateStr); err == nil {
		return time.Date(time.Now().Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.Local), nil
	}
	return time.Time{}, fmt.Errorf("format tanggal tidak dikenali: %s", dateStr)
}

// cycleDisplayUnit menentukan satuan tampilan cycle: satuan konversi yang
// tersimpan di cycle (dari master goods, mis. "lt") apa adanya; fallback ke
// satuan beli bila kosong.
func cycleDisplayUnit(cycle *domain.ConsumptionCycle) string {
	if cycle == nil {
		return ""
	}
	if cycle.ConsumedUnit != "" {
		return cycle.ConsumedUnit
	}
	return cycle.InventoryUnit
}

// StartUsage memulai pemakaian item (saat user bilang "pakai susu 400gr").
// Ini akan membuat consumption cycle baru dengan auto-generated batch number.
func (s *Service) StartUsage(ctx context.Context, chatID string, goods *domain.Good, usageQty float64, usageUnit string, conversionFactor float64, usageDate string) (*domain.ConsumptionCycle, error) {
	itemName := goods.Name
	// Auto-generate batch number
	batchNumber := generateBatchNumber()

	// Parse usage date, default ke time.Now()
	startDate := time.Now()
	if usageDate != "" {
		if parsed, err := parseUsageDate(usageDate); err == nil {
			startDate = parsed
		}
	}

	// Conversion factor = isi per satuan stok, APA ADANYA dari master goods
	// (1 galon = 15 lt → factor 15, unit "lt"). Bila master belum punya
	// faktor: samakan dengan satuan stok (galon → galon, factor 1) — tanpa
	// heuristik satuan dasar (gr/ml).
	smallestUnit := usageUnit
	finalConsumptionQty := usageQty
	finalConversionFactor := conversionFactor

	if goods.ConversionUom != "" && goods.FactorUom > 0 {
		finalConversionFactor = goods.FactorUom
		smallestUnit = goods.ConversionUom
	}

	// SELALU buat cycle baru setiap kali pemakaian (setiap pakai = batch baru)
	// Ini memungkinkan tracking per batch dengan start date yang berbeda

	// Buat cycle baru dengan auto-generated batch
	// Gunakan data inventory untuk InventoryQty/InventoryUnit agar tracking akurat
	cycle := &domain.ConsumptionCycle{
		ChatID:           chatID,
		GoodsID:          goods.ID,
		BatchNumber:      batchNumber,
		StartDate:        startDate,
		InventoryQty:     usageQty,  // qty pemakaian dalam satuan stok (hasil konversi master)
		InventoryUnit:    usageUnit, // satuan stok (dari master goods)
		ConversionFactor: finalConversionFactor,
		ConsumedQty:      finalConsumptionQty * finalConversionFactor, // total dalam satuan konversi master (mis. lt)
		ConsumedUnit:     smallestUnit,                                // satuan konversi master
		Status:           domain.ConsumptionCycleActive,
	}

	if err := s.cycleRepo.Create(ctx, cycle); err != nil {
		return nil, fmt.Errorf("gagal membuat consumption cycle: %w", err)
	}

	s.log.InfoContext(ctx, "consumption cycle dimulai dengan auto-batch", "item", itemName, "batch", batchNumber, "qty", finalConsumptionQty)
	return cycle, nil
}

// CompleteUsage menyelesaikan siklus konsumsi saat item habis ("susu sudah habis").
// Menghitung durasi dan daily rate, lalu return laporan lengkap.
func (s *Service) CompleteUsage(ctx context.Context, chatID string, goods *domain.Good, batchNumber string) (string, error) {
	return s.CompleteUsageWithDate(ctx, chatID, goods, batchNumber, time.Now())
}

// CompleteUsageWithDate seperti CompleteUsage tapi memakai tanggal habis
// eksplisit dari user (mis. "habis 20/01") alih-alih waktu sekarang.
func (s *Service) CompleteUsageWithDate(ctx context.Context, chatID string, goods *domain.Good, batchNumber string, endTime time.Time) (string, error) {
	itemName := goods.Name
	// Cari cycle aktif untuk barang+batch ini
	var cycle *domain.ConsumptionCycle
	var err error

	if batchNumber != "" {
		cycle, err = s.cycleRepo.GetActiveByGoodsAndBatch(ctx, chatID, goods.ID, batchNumber)
	} else {
		cycle, err = s.cycleRepo.GetActiveByGoods(ctx, chatID, goods.ID)
	}

	if err != nil {
		batchInfo := ""
		if batchNumber != "" {
			batchInfo = fmt.Sprintf(" (batch %s)", batchNumber)
		}
		return "", fmt.Errorf("tidak ada siklus aktif untuk %s%s", itemName, batchInfo)
	}

	// Hitung durasi dalam hari (dari tanggal habis eksplisit user)
	daysInUse := endTime.Sub(cycle.StartDate).Hours() / 24

	if daysInUse <= 0 {
		return "", fmt.Errorf("durasi penggunaan tidak valid")
	}

	// Hitung dalam satuan konversi master (mis. lt)
	totalInConversionUnit := cycle.InventoryQty * cycle.ConversionFactor

	// Satuan tampilan dari data cycle (satuan master)
	displayUnit := cycleDisplayUnit(cycle)

	// Update cycle ke completed
	cycle.Status = domain.ConsumptionCycleCompleted
	cycle.EndDate = &endTime
	cycle.ConsumedQty = totalInConversionUnit // penuh, dalam satuan konversi master
	cycle.ConsumedUnit = displayUnit

	if err := s.cycleRepo.Update(ctx, cycle); err != nil {
		return "", fmt.Errorf("gagal menyelesaikan cycle: %w", err)
	}

	// Hitung daily rate
	dailyRate := totalInConversionUnit / daysInUse

	s.log.InfoContext(ctx, "consumption cycle selesai", "item", itemName, "batch", batchNumber, "days", daysInUse, "daily_rate", dailyRate)

	// Format laporan lengkap dengan batch info
	itemLabel := itemName
	if cycle.BatchNumber != "" {
		itemLabel = fmt.Sprintf("%s (%s)", itemName, cycle.BatchNumber)
	}

	totalStr, totalUnitStr := formatQty(totalInConversionUnit, displayUnit)
	rateStr, rateUnitStr := formatQty(dailyRate, displayUnit)

	return fmt.Sprintf(
		"✅ %s sudah habis!\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📊 Total: %s %s (%.1f %s)\n"+
			"📈 Rate: %s %s/hari\n"+
			"📅 Mulai: %s\n"+
			"📅 Selesai: %s",
		itemLabel,
		daysInUse,
		totalStr, totalUnitStr, cycle.InventoryQty, cycle.InventoryUnit,
		rateStr, rateUnitStr,
		cycle.StartDate.Format("02/01/2006"),
		endTime.Format("02/01/2006"),
	), nil
}

// GetActiveCycleInfo mendapatkan informasi siklus aktif untuk analisa.
func (s *Service) GetActiveCycleInfo(ctx context.Context, chatID string, goods *domain.Good, batchNumber string) (string, error) {
	itemName := goods.Name
	var cycle *domain.ConsumptionCycle
	var err error

	if batchNumber != "" {
		cycle, err = s.cycleRepo.GetActiveByGoodsAndBatch(ctx, chatID, goods.ID, batchNumber)
	} else {
		cycle, err = s.cycleRepo.GetActiveByGoods(ctx, chatID, goods.ID)
	}

	if err != nil {
		batchInfo := ""
		if batchNumber != "" {
			batchInfo = fmt.Sprintf(" (batch %s)", batchNumber)
		}
		return "", fmt.Errorf("tidak ada siklus aktif untuk %s%s", itemName, batchInfo)
	}

	daysInUse := time.Since(cycle.StartDate).Hours() / 24

	totalInConversionUnit := cycle.InventoryQty * cycle.ConversionFactor
	totalConsumedInSmallestUnit := cycle.ConsumedQty // sudah dalam satuan konversi master
	remainingInSmallestUnit := totalInConversionUnit - totalConsumedInSmallestUnit

	dailyRateInSmallestUnit := 0.0
	if daysInUse > 0 && totalConsumedInSmallestUnit > 0 {
		dailyRateInSmallestUnit = totalConsumedInSmallestUnit / daysInUse
	}

	estimationDays := 0
	if dailyRateInSmallestUnit > 0 && remainingInSmallestUnit > 0 {
		estimationDays = int(remainingInSmallestUnit / dailyRateInSmallestUnit)
	}

	status := "🔄 Aktif"
	if cycle.Status == domain.ConsumptionCycleCompleted {
		status = "✅ Selesai"
	}

	itemLabel := itemName
	if cycle.BatchNumber != "" {
		itemLabel = fmt.Sprintf("%s (%s)", itemName, cycle.BatchNumber)
	}

	// Satuan tampilan dari data cycle (satuan master)
	displayUnit := cycleDisplayUnit(cycle)

	beliStr, beliUnitStr := formatQty(totalInConversionUnit, displayUnit)
	terpakaiStr, terpakaiUnitStr := formatQty(totalConsumedInSmallestUnit, displayUnit)
	sisaStr, sisaUnitStr := formatQty(remainingInSmallestUnit, displayUnit)
	rateStr, rateUnitStr := formatQty(dailyRateInSmallestUnit, displayUnit)

	return fmt.Sprintf(
		"📊 %s: %s\n"+
			"📦 Dipakai: %g %s (%s %s) pada %s\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📉 Terpakai: %s %s\n"+
			"📊 Sisa: %s %s (%.1f %s)\n"+
			"📈 Rate: %s %s/hari\n"+
			"🔮 Estimasi: %d hari lagi\n"+
			"Status: %s\n\n"+
			"💡 Koreksi data: ketik \"terpakai %s (%s) [jumlah] [unit]\"",
		itemLabel,
		status,
		cycle.InventoryQty, cycle.InventoryUnit, beliStr, beliUnitStr, cycle.StartDate.Format("02/01/2006"),
		daysInUse,
		terpakaiStr, terpakaiUnitStr,
		sisaStr, sisaUnitStr, remainingInSmallestUnit/cycle.ConversionFactor, cycle.InventoryUnit,
		rateStr, rateUnitStr,
		estimationDays,
		cycle.Status,
		cycle.Name(), cycle.BatchNumber,
	), nil
}

// ListActiveItems menampilkan semua item aktif dengan batch numbers untuk user selection.
func (s *Service) ListActiveItems(ctx context.Context, chatID string) (string, error) {
	cycles, err := s.cycleRepo.ListByChat(ctx, chatID, 0) // 0 = no limit
	if err != nil {
		return "", fmt.Errorf("gagal mengambil list active items: %w", err)
	}

	// Filter hanya yang aktif
	var activeCycles []domain.ConsumptionCycle
	for _, cycle := range cycles {
		if cycle.Status == domain.ConsumptionCycleActive {
			activeCycles = append(activeCycles, cycle)
		}
	}

	if len(activeCycles) == 0 {
		return "📋 Belum ada item yang sedang aktif (dalam pemakaian).", nil
	}

	var result string
	result += "📋 **Item Aktif (dalam pemakaian)**\n\n"

	for i := range activeCycles {
		cycle := &activeCycles[i]
		daysInUse := time.Since(cycle.StartDate).Hours() / 24
		totalInSmallestUnit := cycle.InventoryQty * cycle.ConversionFactor
		displayUnit := cycleDisplayUnit(cycle)

		itemLabel := cycle.Name()
		if cycle.BatchNumber != "" {
			itemLabel = fmt.Sprintf("%s (%s)", cycle.Name(), cycle.BatchNumber)
		}

		qtyStr, qtyUnitStr := formatQty(totalInSmallestUnit, displayUnit)

		result += fmt.Sprintf(
			"%d. %s\n   📦 %g %s (%s %s)\n   📅 Mulai: %s (%.0f hari lalu)\n\n",
			i+1, itemLabel,
			cycle.InventoryQty, cycle.InventoryUnit, qtyStr, qtyUnitStr,
			cycle.StartDate.Format("02/01/2006"), daysInUse,
		)
	}

	result += "💡 Untuk menyelesaikan, ketik: \"[nama item] [batch] sudah habis\""

	return result, nil
}

// GetHistory mendapatkan history siklus konsumsi untuk item tertentu.
func (s *Service) GetHistory(ctx context.Context, chatID string, goods *domain.Good) (string, error) {
	itemName := goods.Name
	cycles, err := s.cycleRepo.ListByDateRange(ctx, chatID, goods.ID, time.Time{}, time.Time{})
	if err != nil {
		return "", fmt.Errorf("gagal mengambil history: %w", err)
	}

	if len(cycles) == 0 {
		return fmt.Sprintf("Belum ada data konsumsi untuk %s.", itemName), nil
	}

	var result string
	result += fmt.Sprintf("📊 History Konsumsi: %s\n\n", itemName)

	for i := range cycles {
		cycle := &cycles[i]
		daysInUse := 0.0
		if cycle.EndDate != nil {
			daysInUse = cycle.EndDate.Sub(cycle.StartDate).Hours() / 24
		} else {
			daysInUse = time.Since(cycle.StartDate).Hours() / 24
		}

		status := "✅ Selesai"
		if cycle.Status == domain.ConsumptionCycleActive {
			status = "🔄 Aktif"
		}

		totalInConversionUnit := cycle.InventoryQty * cycle.ConversionFactor
		totalConsumedInSmallestUnit := cycle.ConsumedQty // satuan konversi tersimpan
		displayUnit := cycleDisplayUnit(cycle)

		dailyConsumptionInSmallestUnit := 0.0
		if daysInUse > 0 && totalConsumedInSmallestUnit > 0 {
			dailyConsumptionInSmallestUnit = totalConsumedInSmallestUnit / daysInUse
		}

		beliStr, beliUnitStr := formatQty(totalInConversionUnit, displayUnit)
		terpakaiStr, terpakaiUnitStr := formatQty(totalConsumedInSmallestUnit, displayUnit)
		rateStr, rateUnitStr := formatQty(dailyConsumptionInSmallestUnit, displayUnit)

		result += fmt.Sprintf(
			"%d. %s - %s\n",
			i+1, cycle.StartDate.Format("02/01/2006"), status,
		)
		result += fmt.Sprintf(
			"   Dipakai: %g %s (%s %s), Terpakai: %g %s (%s %s)\n",
			cycle.InventoryQty, cycle.InventoryUnit, beliStr, beliUnitStr,
			cycle.ConsumedQty, cycle.ConsumedUnit, terpakaiStr, terpakaiUnitStr,
		)
		result += fmt.Sprintf(
			"   Durasi: %.0f hari, Rate: %s %s/hari\n\n",
			daysInUse, rateStr, rateUnitStr,
		)
	}

	return result, nil
}

// CalculateDailyConsumption menghitung konsumsi harian dalam satuan pembelian.
func (s *Service) CalculateDailyConsumption(ctx context.Context, chatID, itemName string, purchaseDate, endDate time.Time, purchaseQty float64, purchaseUnit string, conversionFactor float64) (string, error) {
	daysInUse := endDate.Sub(purchaseDate).Hours() / 24
	if daysInUse <= 0 {
		return "", fmt.Errorf("tanggal habis harus setelah tanggal pembelian")
	}

	totalInConversionUnit := purchaseQty * conversionFactor
	dailyConsumption := totalInConversionUnit / daysInUse
	displayUnit := purchaseUnit

	beliStr, beliUnitStr := formatQty(totalInConversionUnit, displayUnit)
	rateStr, rateUnitStr := formatQty(dailyConsumption, displayUnit)

	result := fmt.Sprintf(
		"📊 Hasil Perhitungan Konsumsi: %s\n"+
			"📦 Pembelian: %g %s (%s %s)\n"+
			"📅 Tanggal Beli: %s\n"+
			"📅 Tanggal Habis: %s\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📈 Konsumsi Per Hari: %s %s/hari\n"+
			"📉 Total Konsumsi: %s %s",
		itemName,
		purchaseQty, purchaseUnit, beliStr, beliUnitStr,
		purchaseDate.Format("02/01/2006"),
		endDate.Format("02/01/2006"),
		daysInUse,
		rateStr, rateUnitStr,
		beliStr, beliUnitStr,
	)

	return result, nil
}

// UpdateConsumption mengupdate nilai konsumsi untuk cycle yang sudah ada (koreksi data)
func (s *Service) UpdateConsumption(ctx context.Context, chatID string, goods *domain.Good, batchNumber string, consumedQty float64, consumedUnit string) (string, error) {
	itemName := goods.Name
	// Cari cycle aktif untuk barang+batch ini
	var cycle *domain.ConsumptionCycle
	var err error

	if batchNumber != "" {
		cycle, err = s.cycleRepo.GetActiveByGoodsAndBatch(ctx, chatID, goods.ID, batchNumber)
	} else {
		cycle, err = s.cycleRepo.GetActiveByGoods(ctx, chatID, goods.ID)
	}

	if err != nil {
		batchInfo := ""
		if batchNumber != "" {
			batchInfo = fmt.Sprintf(" (batch %s)", batchNumber)
		}
		return "", fmt.Errorf("tidak ada siklus aktif untuk %s%s", itemName, batchInfo)
	}

	// Update consumed quantity (replace, bukan tambah)
	cycle.ConsumedQty = consumedQty
	cycle.ConsumedUnit = consumedUnit

	if err := s.cycleRepo.Update(ctx, cycle); err != nil {
		return "", fmt.Errorf("gagal update consumption cycle: %w", err)
	}

	// Hitung ulang info untuk display
	daysInUse := time.Since(cycle.StartDate).Hours() / 24
	totalInConversionUnit := cycle.InventoryQty * cycle.ConversionFactor
	totalConsumedInSmallestUnit := cycle.ConsumedQty // sudah dalam satuan konversi master
	remainingInSmallestUnit := totalInConversionUnit - totalConsumedInSmallestUnit

	dailyRateInSmallestUnit := 0.0
	if daysInUse > 0 && totalConsumedInSmallestUnit > 0 {
		dailyRateInSmallestUnit = totalConsumedInSmallestUnit / daysInUse
	}

	estimationDays := 0
	if dailyRateInSmallestUnit > 0 && remainingInSmallestUnit > 0 {
		estimationDays = int(remainingInSmallestUnit / dailyRateInSmallestUnit)
	}

	// Satuan tampilan dari data cycle (satuan master)
	displayUnit := cycleDisplayUnit(cycle)

	itemLabel := itemName
	if cycle.BatchNumber != "" {
		itemLabel = fmt.Sprintf("%s (%s)", itemName, cycle.BatchNumber)
	}

	s.log.InfoContext(ctx, "consumption cycle diupdate (koreksi)", "item", itemName, "batch", cycle.BatchNumber, "new_consumed_qty", consumedQty, "new_consumed_unit", consumedUnit)

	terpakaiStr, terpakaiUnitStr := formatQty(totalConsumedInSmallestUnit, displayUnit)
	sisaStr, sisaUnitStr := formatQty(remainingInSmallestUnit, displayUnit)
	rateStr, rateUnitStr := formatQty(dailyRateInSmallestUnit, displayUnit)

	return fmt.Sprintf(
		"✅ Konsumisi %s diupdate!\n"+
			"📉 Terpakai: %s %s\n"+
			"📊 Sisa: %s %s (%.1f %s)\n"+
			"📈 Rate: %s %s/hari\n"+
			"🔮 Estimasi: %d hari lagi\n"+
			"📅 Mulai: %s",
		itemLabel,
		terpakaiStr, terpakaiUnitStr,
		sisaStr, sisaUnitStr, remainingInSmallestUnit/cycle.ConversionFactor, cycle.InventoryUnit,
		rateStr, rateUnitStr,
		estimationDays,
		cycle.StartDate.Format("02/01/2006"),
	), nil
}
