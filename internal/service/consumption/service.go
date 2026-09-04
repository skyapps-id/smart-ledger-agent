// Package consumption menyediakan business logic untuk consumption cycles
package consumption

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

// Service menangani pembuatan dan analisa consumption cycles.
type Service struct {
	db        *gorm.DB
	cycleRepo repository.ConsumptionCycleRepository
	log       *slog.Logger
}

func NewService(db *gorm.DB, cycleRepo repository.ConsumptionCycleRepository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		db:        db,
		cycleRepo: cycleRepo,
		log:       logger,
	}
}

// StartCycle memulai siklus konsumsi baru ketika pembelian terjadi.
func (s *Service) StartCycle(ctx context.Context, chatID string, goods *domain.Good, purchaseQty float64, purchaseUnit string, conversionFactor float64) (*domain.ConsumptionCycle, error) {
	return s.StartCycleWithDate(ctx, chatID, goods, purchaseQty, purchaseUnit, conversionFactor, time.Now())
}

// StartCycleWithDate memulai siklus konsumsi baru dengan tanggal pembelian spesifik.
func (s *Service) StartCycleWithDate(ctx context.Context, chatID string, goods *domain.Good, purchaseQty float64, purchaseUnit string, conversionFactor float64, purchaseDate time.Time) (*domain.ConsumptionCycle, error) {
	// Determine smallest unit based on purchase unit
	smallestUnit := determineSmallestUnit(purchaseUnit)

	cycle := &domain.ConsumptionCycle{
		ChatID:           chatID,
		GoodsID:          goods.ID,
		StartDate:        purchaseDate,
		PurchaseQty:      purchaseQty,
		PurchaseUnit:     purchaseUnit,
		ConversionFactor: conversionFactor,
		ConsumedQty:      0,
		ConsumedUnit:     smallestUnit, // gunakan satuan terkecil yang sesuai
		Status:           domain.ConsumptionCycleActive,
	}

	if err := s.cycleRepo.Create(ctx, cycle); err != nil {
		return nil, fmt.Errorf("gagal membuat consumption cycle: %w", err)
	}

	s.log.InfoContext(ctx, "consumption cycle dibuat", "item", goods.Name, "qty", purchaseQty, "date", purchaseDate)
	return cycle, nil
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

// determineSmallestUnit menentukan satuan terkecil berdasarkan satuan pembelian dan item name
func determineSmallestUnit(purchaseUnit string) string {
	lowerUnit := strings.ToLower(purchaseUnit)

	// Liquid units -> ml (be more specific to avoid false matches)
	if strings.Contains(lowerUnit, "ml") || strings.Contains(lowerUnit, "mililiter") || strings.Contains(lowerUnit, "mililitre") {
		return "ml"
	}
	if strings.Contains(lowerUnit, "liter") {
		return "ml"
	}
	if strings.Contains(lowerUnit, "lt") {
		return "ml"
	}
	// Only match standalone "l" as a unit, not "l" inside words like "kaleng"
	if lowerUnit == "l" || strings.HasSuffix(lowerUnit, " l") || strings.HasPrefix(lowerUnit, "l ") {
		return "ml"
	}

	// Solid units -> gr
	if strings.Contains(lowerUnit, "gr") || strings.Contains(lowerUnit, "gram") {
		return "gr"
	}
	if strings.Contains(lowerUnit, "kg") || strings.Contains(lowerUnit, "kilogram") {
		return "gr"
	}

	// Count units -> pcs
	if strings.Contains(lowerUnit, "pcs") || strings.Contains(lowerUnit, "buah") ||
		strings.Contains(lowerUnit, "keping") || strings.Contains(lowerUnit, "ball") {
		return "pcs"
	}

	// Default to gr for unknown units
	return "gr"
}

// ExtractOriginalUnitFromItemName mengekstrak satuan ASLI (mentah, tanpa
// normalisasi) dari nama item. Contoh: "susu uht 500ml" → "ml",
// "susu 1kg" → "kg", "teh 200gr" → "gr", "pampers 48pcs" → "pcs".
// Satuan user dipakai apa adanya — konversi hanya lewat jalur eksplisit
// (lihat convert.go). Nama tanpa ukuran → "".
func ExtractOriginalUnitFromItemName(itemName string) string {
	_, unit := extractSizeFromItemName(itemName)
	return unit
}

// ExtractQuantityFromItemName mengekstrak angka ukuran dari nama item.
// Contoh: "susu uht 500ml" → 500, "pampers mamypoko 48" → 48.
func ExtractQuantityFromItemName(itemName string) float64 {
	qty, _ := extractSizeFromItemName(itemName)
	return qty
}

// determineSmallestUnitFromName menentukan satuan terkecil berdasarkan nama barang
func determineSmallestUnitFromName(itemName string) string {
	lowerName := strings.ToLower(itemName)

	// Solid indicators in item name - weight units
	if strings.Contains(lowerName, "gr") || strings.Contains(lowerName, "gram") {
		return "gr"
	}
	if strings.Contains(lowerName, "kg") || strings.Contains(lowerName, "kilogram") {
		return "gr"
	}

	// Liquid indicators in item name - volume units
	if strings.Contains(lowerName, "ml") || strings.Contains(lowerName, "mililiter") || strings.Contains(lowerName, "mililitre") {
		return "ml"
	}
	if strings.Contains(lowerName, "liter") || (strings.Contains(lowerName, "l") && !strings.Contains(lowerName, " kaleng")) {
		return "ml"
	}

	// Specific liquid types with volume indicators
	if strings.Contains(lowerName, "uht") && (strings.Contains(lowerName, "ml") || strings.Contains(lowerName, "liter")) {
		return "ml"
	}

	// Default to gr for unknown or solid items
	return "gr"
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

	// Determine smallest unit based on usage unit
	smallestUnit := determineSmallestUnit(usageUnit)

	// Jika ada original unit dari nama item, gunakan untuk consumption tracking.
	// Semantic seragam: usageQty dalam SATUAN INVENTORY (pcs/ball hasil konversi),
	// conversion factor = isi per satuan inventory dalam SATUAN DASAR
	// ("susu bmt 200g" → 1 pcs = 200 gr; "pampers mamypoko 48" → 1 ball = 48 pcs;
	// "galon 15lt" → 1 galon = 15000 ml), ConsumedQty = pemakaian dalam satuan dasar.
	finalConsumptionQty := usageQty
	finalConversionFactor := conversionFactor

	if perQty, perUnit := extractSizeFromItemName(itemName); perQty > 0 {
		if base, baseVal, ok := unitToBase(perUnit, perQty); ok {
			finalConversionFactor = baseVal
			smallestUnit = base
			if base == "ct" {
				smallestUnit = "pcs"
			}
		}
	}

	// SELALU buat cycle baru setiap kali pemakaian (setiap pakai = batch baru)
	// Ini memungkinkan tracking per batch dengan start date yang berbeda

	// Buat cycle baru dengan auto-generated batch
	// Gunakan data inventory untuk PurchaseQty/PurchaseUnit agar tracking akurat
	cycle := &domain.ConsumptionCycle{
		ChatID:           chatID,
		GoodsID:          goods.ID,
		BatchNumber:      batchNumber,
		StartDate:        startDate,
		PurchaseQty:      usageQty,  // gunakan quantity dari inventory (pcs)
		PurchaseUnit:     usageUnit, // gunakan unit dari inventory (pcs)
		ConversionFactor: finalConversionFactor,
		ConsumedQty:      finalConsumptionQty * finalConversionFactor, // tracking dalam unit asli (ml/gr)
		ConsumedUnit:     smallestUnit,                                // tracking dalam unit asli (ml/gr)
		Status:           domain.ConsumptionCycleActive,
	}

	if err := s.cycleRepo.Create(ctx, cycle); err != nil {
		return nil, fmt.Errorf("gagal membuat consumption cycle: %w", err)
	}

	s.log.InfoContext(ctx, "consumption cycle dimulai dengan auto-batch", "item", itemName, "batch", batchNumber, "qty", finalConsumptionQty)
	return cycle, nil
}

// RecordConsumption mencatat pemakaian dan mengupdate siklus aktif.
func (s *Service) RecordConsumption(ctx context.Context, chatID string, goods *domain.Good, consumedQty float64, consumedUnit string) (*domain.ConsumptionCycle, error) {
	// Cek siklus aktif
	cycle, err := s.cycleRepo.GetActiveByGoods(ctx, chatID, goods.ID)
	if err != nil {
		return nil, fmt.Errorf("tidak ada siklus aktif untuk %s: %w", goods.Name, err)
	}

	// Update consumed quantity
	cycle.ConsumedQty += consumedQty
	cycle.ConsumedUnit = consumedUnit

	// Check jika cycle selesai (consumed >= purchased)
	totalPurchasedInSmallestUnit := cycle.PurchaseQty * cycle.ConversionFactor
	totalConsumedInSmallestUnit := cycle.ConsumedQty // asumsi consumedQty sudah dalam satuan terkecil

	if totalConsumedInSmallestUnit >= totalPurchasedInSmallestUnit {
		cycle.Status = domain.ConsumptionCycleCompleted
		endTime := time.Now()
		cycle.EndDate = &endTime

		if err := s.cycleRepo.Update(ctx, cycle); err != nil {
			return nil, fmt.Errorf("gagal menyelesaikan cycle: %w", err)
		}

		durationDays := time.Since(cycle.StartDate).Hours() / 24
		s.log.InfoContext(ctx, "consumption cycle selesai", "item", goods.Name, "duration_days", durationDays)
		return cycle, nil
	}

	if err := s.cycleRepo.Update(ctx, cycle); err != nil {
		return nil, fmt.Errorf("gagal update consumption cycle: %w", err)
	}

	s.log.DebugContext(ctx, "consumption diupdate", "item", goods.Name, "consumed_qty", consumedQty, "consumed_unit", consumedUnit)
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

	// Hitung dalam satuan terkecil (gram/ml)
	totalPurchasedInSmallestUnit := cycle.PurchaseQty * cycle.ConversionFactor

	// Determine the correct display unit based on both purchase unit and item name
	displayUnit := determineSmallestUnit(cycle.PurchaseUnit)
	if displayUnit == "gr" {
		// Check if item name suggests liquid
		displayUnit = determineSmallestUnitFromName(itemName)
	}

	// Update cycle ke completed
	cycle.Status = domain.ConsumptionCycleCompleted
	cycle.EndDate = &endTime
	cycle.ConsumedQty = totalPurchasedInSmallestUnit // penuh, dalam satuan dasar (gr/ml)
	cycle.ConsumedUnit = displayUnit

	if err := s.cycleRepo.Update(ctx, cycle); err != nil {
		return "", fmt.Errorf("gagal menyelesaikan cycle: %w", err)
	}

	// Hitung daily rate
	dailyRate := totalPurchasedInSmallestUnit / daysInUse

	s.log.InfoContext(ctx, "consumption cycle selesai", "item", itemName, "batch", batchNumber, "days", daysInUse, "daily_rate", dailyRate)

	// Format laporan lengkap dengan batch info
	itemLabel := itemName
	if cycle.BatchNumber != "" {
		itemLabel = fmt.Sprintf("%s (%s)", itemName, cycle.BatchNumber)
	}

	totalStr, totalUnitStr := FormatQtyForDisplay(totalPurchasedInSmallestUnit, displayUnit, itemName, cycle.PurchaseUnit)
	rateStr, rateUnitStr := FormatQtyForDisplay(dailyRate, displayUnit, itemName, cycle.PurchaseUnit)

	return fmt.Sprintf(
		"✅ %s sudah habis!\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📊 Total: %s %s (%.1f %s)\n"+
			"📈 Rate: %s %s/hari\n"+
			"📅 Mulai: %s\n"+
			"📅 Selesai: %s",
		itemLabel,
		daysInUse,
		totalStr, totalUnitStr, cycle.PurchaseQty, cycle.PurchaseUnit,
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

	totalPurchasedInSmallestUnit := cycle.PurchaseQty * cycle.ConversionFactor
	totalConsumedInSmallestUnit := cycle.ConsumedQty // sudah dalam satuan dasar (gr/ml)
	remainingInSmallestUnit := totalPurchasedInSmallestUnit - totalConsumedInSmallestUnit

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

	// Determine the correct display unit based on both purchase unit and item name
	displayUnit := determineSmallestUnit(cycle.PurchaseUnit)
	if displayUnit == "gr" {
		// Check if item name suggests liquid
		displayUnit = determineSmallestUnitFromName(itemName)
	}

	beliStr, beliUnitStr := FormatQtyForDisplay(totalPurchasedInSmallestUnit, displayUnit, itemName, cycle.PurchaseUnit)
	terpakaiStr, terpakaiUnitStr := FormatQtyForDisplay(totalConsumedInSmallestUnit, displayUnit, itemName, cycle.PurchaseUnit)
	sisaStr, sisaUnitStr := FormatQtyForDisplay(remainingInSmallestUnit, displayUnit, itemName, cycle.PurchaseUnit)
	rateStr, rateUnitStr := FormatQtyForDisplay(dailyRateInSmallestUnit, displayUnit, itemName, cycle.PurchaseUnit)

	return fmt.Sprintf(
		"📊 %s: %s\n"+
			"📦 Beli: %g %s (%s %s) pada %s\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📉 Terpakai: %s %s\n"+
			"📊 Sisa: %s %s (%.1f %s)\n"+
			"📈 Rate: %s %s/hari\n"+
			"🔮 Estimasi: %d hari lagi\n"+
			"Status: %s\n\n"+
			"💡 Koreksi data: ketik \"terpakai %s (%s) [jumlah] [unit]\"",
		itemLabel,
		status,
		cycle.PurchaseQty, cycle.PurchaseUnit, beliStr, beliUnitStr, cycle.StartDate.Format("02/01/2006"),
		daysInUse,
		terpakaiStr, terpakaiUnitStr,
		sisaStr, sisaUnitStr, remainingInSmallestUnit/cycle.ConversionFactor, cycle.PurchaseUnit,
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

	for i, cycle := range activeCycles {
		daysInUse := time.Since(cycle.StartDate).Hours() / 24
		totalInSmallestUnit := cycle.PurchaseQty * cycle.ConversionFactor
		displayUnit := determineSmallestUnit(cycle.PurchaseUnit)
		if displayUnit == "gr" {
			// Check if item name suggests liquid
			displayUnit = determineSmallestUnitFromName(cycle.Name())
		}

		itemLabel := cycle.Name()
		if cycle.BatchNumber != "" {
			itemLabel = fmt.Sprintf("%s (%s)", cycle.Name(), cycle.BatchNumber)
		}

		// Extract actual quantity from item name for accurate display
		displayQty := ExtractQuantityFromItemName(cycle.Name())
		if displayQty == 0 {
			displayQty = totalInSmallestUnit // fallback ke calculated quantity
		}

		qtyStr, qtyUnitStr := FormatQtyForDisplay(displayQty, displayUnit, cycle.Name(), cycle.PurchaseUnit)

		result += fmt.Sprintf(
			"%d. %s\n   📦 %g %s (%s %s)\n   📅 Mulai: %s (%.0f hari lalu)\n\n",
			i+1, itemLabel,
			cycle.PurchaseQty, cycle.PurchaseUnit, qtyStr, qtyUnitStr,
			cycle.StartDate.Format("02/01/2006"), daysInUse,
		)
	}

	result += "💡 Untuk menyelesaikan, ketik: \"[nama item] [batch] sudah habis\""

	return result, nil
}

// GetHistory mendapatkan history siklus konsumsi untuk item tertentu.
func (s *Service) GetHistory(ctx context.Context, chatID string, goods *domain.Good, limit int) (string, error) {
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

	for i, cycle := range cycles {

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

		totalPurchasedInSmallestUnit := cycle.PurchaseQty * cycle.ConversionFactor
		totalConsumedInSmallestUnit := cycle.ConsumedQty // sudah dalam satuan dasar (gr/ml)
		displayUnit := determineSmallestUnit(cycle.PurchaseUnit)

		dailyConsumptionInSmallestUnit := 0.0
		if daysInUse > 0 && totalConsumedInSmallestUnit > 0 {
			dailyConsumptionInSmallestUnit = totalConsumedInSmallestUnit / daysInUse
		}

		beliStr, beliUnitStr := FormatQtyForDisplay(totalPurchasedInSmallestUnit, displayUnit, cycle.Name(), cycle.PurchaseUnit)
		terpakaiStr, terpakaiUnitStr := FormatQtyForDisplay(totalConsumedInSmallestUnit, displayUnit, cycle.Name(), cycle.PurchaseUnit)
		rateStr, rateUnitStr := FormatQtyForDisplay(dailyConsumptionInSmallestUnit, displayUnit, cycle.Name(), cycle.PurchaseUnit)

		result += fmt.Sprintf(
			"%d. %s - %s\n",
			i+1, cycle.StartDate.Format("02/01/2006"), status,
		)
		result += fmt.Sprintf(
			"   Beli: %g %s (%s %s), Terpakai: %g %s (%s %s)\n",
			cycle.PurchaseQty, cycle.PurchaseUnit, beliStr, beliUnitStr,
			cycle.ConsumedQty, cycle.ConsumedUnit, terpakaiStr, terpakaiUnitStr,
		)
		result += fmt.Sprintf(
			"   Durasi: %.0f hari, Rate: %s %s/hari\n\n",
			daysInUse, rateStr, rateUnitStr,
		)
	}

	return result, nil
}

// CompleteCycleWithEndDate menyelesaikan siklus konsumsi dengan tanggal habis yang spesifik.
func (s *Service) CompleteCycleWithEndDate(ctx context.Context, chatID string, goods *domain.Good, endDate time.Time) (*domain.ConsumptionCycle, error) {
	itemName := goods.Name
	cycle, err := s.cycleRepo.GetActiveByGoods(ctx, chatID, goods.ID)
	if err != nil {
		return nil, fmt.Errorf("tidak ada siklus aktif untuk %s: %w", itemName, err)
	}

	if cycle.Status == domain.ConsumptionCycleCompleted {
		return nil, fmt.Errorf("siklus %s sudah selesai", itemName)
	}

	daysInUse := endDate.Sub(cycle.StartDate).Hours() / 24
	if daysInUse <= 0 {
		return nil, fmt.Errorf("tanggal habis harus setelah tanggal pembelian")
	}

	totalPurchasedInSmallestUnit := cycle.PurchaseQty * cycle.ConversionFactor
	dailyConsumption := totalPurchasedInSmallestUnit / daysInUse

	// Determine the correct display unit based on both purchase unit and item name
	displayUnit := determineSmallestUnit(cycle.PurchaseUnit)
	if displayUnit == "gr" {
		// Check if item name suggests liquid
		displayUnit = determineSmallestUnitFromName(itemName)
	}

	cycle.ConsumedQty = totalPurchasedInSmallestUnit // penuh, dalam satuan dasar (gr/ml)
	cycle.ConsumedUnit = displayUnit
	cycle.Status = domain.ConsumptionCycleCompleted
	cycle.EndDate = &endDate

	if err := s.cycleRepo.Update(ctx, cycle); err != nil {
		return nil, fmt.Errorf("gagal menyelesaikan cycle: %w", err)
	}

	s.log.InfoContext(ctx, "consumption cycle selesai dengan tanggal spesifik",
		"item", itemName,
		"days", daysInUse,
		"daily_rate", dailyConsumption)

	return cycle, nil
}

// CalculateDailyConsumption menghitung konsumsi harian dalam satuan terkecil (gr/ml).
func (s *Service) CalculateDailyConsumption(ctx context.Context, chatID, itemName string, purchaseDate, endDate time.Time, purchaseQty float64, purchaseUnit string, conversionFactor float64) (string, error) {
	daysInUse := endDate.Sub(purchaseDate).Hours() / 24
	if daysInUse <= 0 {
		return "", fmt.Errorf("tanggal habis harus setelah tanggal pembelian")
	}

	totalPurchasedInSmallestUnit := purchaseQty * conversionFactor
	dailyConsumption := totalPurchasedInSmallestUnit / daysInUse
	displayUnit := determineSmallestUnit(purchaseUnit)

	beliStr, beliUnitStr := FormatQtyForDisplay(totalPurchasedInSmallestUnit, displayUnit, itemName, purchaseUnit)
	rateStr, rateUnitStr := FormatQtyForDisplay(dailyConsumption, displayUnit, itemName, purchaseUnit)

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
	totalPurchasedInSmallestUnit := cycle.PurchaseQty * cycle.ConversionFactor
	totalConsumedInSmallestUnit := cycle.ConsumedQty // sudah dalam satuan dasar (gr/ml)
	remainingInSmallestUnit := totalPurchasedInSmallestUnit - totalConsumedInSmallestUnit

	dailyRateInSmallestUnit := 0.0
	if daysInUse > 0 && totalConsumedInSmallestUnit > 0 {
		dailyRateInSmallestUnit = totalConsumedInSmallestUnit / daysInUse
	}

	estimationDays := 0
	if dailyRateInSmallestUnit > 0 && remainingInSmallestUnit > 0 {
		estimationDays = int(remainingInSmallestUnit / dailyRateInSmallestUnit)
	}

	// Determine the correct display unit based on both purchase unit and item name
	displayUnit := determineSmallestUnit(cycle.PurchaseUnit)
	if displayUnit == "gr" {
		// Check if item name suggests liquid
		displayUnit = determineSmallestUnitFromName(itemName)
	}

	itemLabel := itemName
	if cycle.BatchNumber != "" {
		itemLabel = fmt.Sprintf("%s (%s)", itemName, cycle.BatchNumber)
	}

	s.log.InfoContext(ctx, "consumption cycle diupdate (koreksi)", "item", itemName, "batch", cycle.BatchNumber, "new_consumed_qty", consumedQty, "new_consumed_unit", consumedUnit)

	terpakaiStr, terpakaiUnitStr := FormatQtyForDisplay(totalConsumedInSmallestUnit, displayUnit, itemName, cycle.PurchaseUnit)
	sisaStr, sisaUnitStr := FormatQtyForDisplay(remainingInSmallestUnit, displayUnit, itemName, cycle.PurchaseUnit)
	rateStr, rateUnitStr := FormatQtyForDisplay(dailyRateInSmallestUnit, displayUnit, itemName, cycle.PurchaseUnit)

	return fmt.Sprintf(
		"✅ Konsumisi %s diupdate!\n"+
			"📉 Terpakai: %s %s\n"+
			"📊 Sisa: %s %s (%.1f %s)\n"+
			"📈 Rate: %s %s/hari\n"+
			"🔮 Estimasi: %d hari lagi\n"+
			"📅 Mulai: %s",
		itemLabel,
		terpakaiStr, terpakaiUnitStr,
		sisaStr, sisaUnitStr, remainingInSmallestUnit/cycle.ConversionFactor, cycle.PurchaseUnit,
		rateStr, rateUnitStr,
		estimationDays,
		cycle.StartDate.Format("02/01/2006"),
	), nil
}
