// Package service menyediakan business logic untuk consumption cycles.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

// ConsumptionService menangani pembuatan dan analisa consumption cycles.
type ConsumptionService struct {
	db        *gorm.DB
	cycleRepo repository.ConsumptionCycleRepository
	log       *slog.Logger
}

func NewConsumptionService(db *gorm.DB, cycleRepo repository.ConsumptionCycleRepository, logger *slog.Logger) *ConsumptionService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ConsumptionService{
		db:        db,
		cycleRepo: cycleRepo,
		log:       logger,
	}
}

// StartCycle memulai siklus konsumsi baru ketika pembelian terjadi.
func (s *ConsumptionService) StartCycle(ctx context.Context, chatID, itemName string, purchaseQty float64, purchaseUnit string, conversionFactor float64) (*domain.ConsumptionCycle, error) {
	return s.StartCycleWithDate(ctx, chatID, itemName, purchaseQty, purchaseUnit, conversionFactor, time.Now())
}

// StartCycleWithDate memulai siklus konsumsi baru dengan tanggal pembelian spesifik.
func (s *ConsumptionService) StartCycleWithDate(ctx context.Context, chatID, itemName string, purchaseQty float64, purchaseUnit string, conversionFactor float64, purchaseDate time.Time) (*domain.ConsumptionCycle, error) {
	// Determine smallest unit based on purchase unit
	smallestUnit := determineSmallestUnit(purchaseUnit)

	cycle := &domain.ConsumptionCycle{
		ChatID:           chatID,
		ItemName:         itemName,
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

	s.log.InfoContext(ctx, "consumption cycle dibuat", "item", itemName, "qty", purchaseQty, "date", purchaseDate)
	return cycle, nil
}

// generateBatchNumber membuat batch number otomatis dengan format: MMM-DD-HHmmss
// Contoh: AUG-12-204315
func generateBatchNumber() string {
	now := time.Now()
	months := []string{"JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"}
	month := months[int(now.Month())-1]
	return fmt.Sprintf("%s-%02d-%02d%02d%02d", month, now.Day(), now.Hour(), now.Minute(), now.Second())
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

	// Default to gr for unknown units
	return "gr"
}

// extractOriginalUnitFromItemName mengekstrak satuan asli dari nama item
// Contoh: "susu uht 500ml" → "ml", "susu 1kg" → "kg", "teh 200gr" → "gr"
func extractOriginalUnitFromItemName(itemName string) string {
	lowerName := strings.ToLower(itemName)
	
	// Pattern untuk mencari angka + unit di nama item
	re := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(ml|mililiter|gr|gram|kg|kilogram|l|liter)`)
	matches := re.FindStringSubmatch(lowerName)
	
	if len(matches) >= 3 {
		unit := matches[2]
		// Normalize unit
		switch unit {
		case "gr", "gram":
			return "gr"
		case "kg", "kilogram":
			return "gr" // akan dikonversi ke gr
		case "ml", "mililiter", "mililitre":
			return "ml"
		case "l", "liter":
			return "ml" // akan dikonversi ke ml
		}
	}
	
	return ""
}

// extractQuantityFromItemName mengekstrak quantity dari nama item
// Contoh: "susu uht 500ml" → 500, "susu 1liter" → 1
func extractQuantityFromItemName(itemName string) float64 {
	lowerName := strings.ToLower(itemName)
	
	// Pattern untuk mencari angka + unit di nama item
	re := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(ml|mililiter|gr|gram|kg|kilogram|l|liter)`)
	matches := re.FindStringSubmatch(lowerName)
	
	if len(matches) >= 2 {
		if qty, err := strconv.ParseFloat(matches[1], 64); err == nil {
			return qty
		}
	}
	
	return 0
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
func (s *ConsumptionService) StartUsage(ctx context.Context, chatID, itemName string, usageQty float64, usageUnit string, conversionFactor float64) (*domain.ConsumptionCycle, error) {
	// Auto-generate batch number
	batchNumber := generateBatchNumber()

	// Extract original unit from item name untuk tracking yang akurat
	originalUnit := extractOriginalUnitFromItemName(itemName)
	originalQty := extractQuantityFromItemName(itemName)
	
	// Determine smallest unit based on usage unit
	smallestUnit := determineSmallestUnit(usageUnit)
	
	// Jika ada original unit dari nama item, gunakan untuk consumption tracking
	finalConsumptionQty := usageQty
	finalConversionFactor := conversionFactor
	
	if originalUnit != "" && originalQty > 0 {
		// Use original unit from item name for accurate tracking
		finalConsumptionQty = originalQty
		finalConversionFactor = 1.0 // already in smallest unit
		smallestUnit = determineSmallestUnit(originalUnit)
	}

	// SELALU buat cycle baru setiap kali pemakaian (setiap pakai = batch baru)
	// Ini memungkinkan tracking per batch dengan start date yang berbeda

	// Buat cycle baru dengan auto-generated batch
	// Gunakan data inventory untuk PurchaseQty/PurchaseUnit agar tracking akurat
	cycle := &domain.ConsumptionCycle{
		ChatID:           chatID,
		ItemName:         itemName,
		BatchNumber:      batchNumber,
		StartDate:        time.Now(),
		PurchaseQty:      usageQty,    // gunakan quantity dari inventory (pcs)
		PurchaseUnit:     usageUnit,   // gunakan unit dari inventory (pcs)  
		ConversionFactor: finalConversionFactor,
		ConsumedQty:      finalConsumptionQty * finalConversionFactor, // tracking dalam unit asli (ml/gr)
		ConsumedUnit:     smallestUnit, // tracking dalam unit asli (ml/gr)
		Status:           domain.ConsumptionCycleActive,
	}

	if err := s.cycleRepo.Create(ctx, cycle); err != nil {
		return nil, fmt.Errorf("gagal membuat consumption cycle: %w", err)
	}

	s.log.InfoContext(ctx, "consumption cycle dimulai dengan auto-batch", "item", itemName, "batch", batchNumber, "qty", finalConsumptionQty)
	return cycle, nil
}

// RecordConsumption mencatat pemakaian dan mengupdate siklus aktif.
func (s *ConsumptionService) RecordConsumption(ctx context.Context, chatID, itemName string, consumedQty float64, consumedUnit string) (*domain.ConsumptionCycle, error) {
	// Cek siklus aktif
	cycle, err := s.cycleRepo.GetActiveByItem(ctx, chatID, itemName)
	if err != nil {
		return nil, fmt.Errorf("tidak ada siklus aktif untuk %s: %w", itemName, err)
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
		s.log.InfoContext(ctx, "consumption cycle selesai", "item", itemName, "duration_days", durationDays)
		return cycle, nil
	}

	if err := s.cycleRepo.Update(ctx, cycle); err != nil {
		return nil, fmt.Errorf("gagal update consumption cycle: %w", err)
	}

	s.log.DebugContext(ctx, "consumption diupdate", "item", itemName, "consumed_qty", consumedQty, "consumed_unit", consumedUnit)
	return cycle, nil
}

// CompleteUsage menyelesaikan siklus konsumsi saat item habis ("susu sudah habis").
// Menghitung durasi dan daily rate, lalu return laporan lengkap.
func (s *ConsumptionService) CompleteUsage(ctx context.Context, chatID, itemName, batchNumber string) (string, error) {
	// Cari cycle aktif untuk item+batch ini
	var cycle *domain.ConsumptionCycle
	var err error

	if batchNumber != "" {
		cycle, err = s.cycleRepo.GetActiveByItemAndBatch(ctx, chatID, itemName, batchNumber)
	} else {
		cycle, err = s.cycleRepo.GetActiveByItem(ctx, chatID, itemName)
	}

	if err != nil {
		batchInfo := ""
		if batchNumber != "" {
			batchInfo = fmt.Sprintf(" (batch %s)", batchNumber)
		}
		return "", fmt.Errorf("tidak ada siklus aktif untuk %s%s", itemName, batchInfo)
	}

	// Hitung durasi dalam hari
	endTime := time.Now()
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
	cycle.ConsumedQty = totalPurchasedInSmallestUnit / cycle.ConversionFactor // Set ke full amount
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

	return fmt.Sprintf(
		"✅ %s sudah habis!\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📊 Total: %.0f %s (%.1f %s)\n"+
			"📈 Rate: %.1f %s/hari\n"+
			"📅 Mulai: %s\n"+
			"📅 Selesai: %s",
		itemLabel,
		daysInUse,
		totalPurchasedInSmallestUnit, displayUnit, cycle.PurchaseQty, cycle.PurchaseUnit,
		dailyRate, displayUnit,
		cycle.StartDate.Format("02/01/2006"),
		endTime.Format("02/01/2006"),
	), nil
}

// GetActiveCycleInfo mendapatkan informasi siklus aktif untuk analisa.
func (s *ConsumptionService) GetActiveCycleInfo(ctx context.Context, chatID, itemName, batchNumber string) (string, error) {
	var cycle *domain.ConsumptionCycle
	var err error

	if batchNumber != "" {
		cycle, err = s.cycleRepo.GetActiveByItemAndBatch(ctx, chatID, itemName, batchNumber)
	} else {
		cycle, err = s.cycleRepo.GetActiveByItem(ctx, chatID, itemName)
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
	totalConsumedInSmallestUnit := cycle.ConsumedQty * cycle.ConversionFactor
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

	return fmt.Sprintf(
		"📊 %s: %s\n"+
			"📦 Beli: %g %s (%.0f %s) pada %s\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📉 Terpakai: %.0f %s\n"+
			"📊 Sisa: %.0f %s (%.1f %s)\n"+
			"📈 Rate: %.1f %s/hari\n"+
			"🔮 Estimasi: %d hari lagi\n"+
			"Status: %s",
		itemLabel,
		status,
		cycle.PurchaseQty, cycle.PurchaseUnit, totalPurchasedInSmallestUnit, displayUnit, cycle.StartDate.Format("02/01/2006"),
		daysInUse,
		totalConsumedInSmallestUnit, displayUnit,
		remainingInSmallestUnit, displayUnit, remainingInSmallestUnit/cycle.ConversionFactor, cycle.PurchaseUnit,
		dailyRateInSmallestUnit, displayUnit,
		estimationDays,
		cycle.Status,
	), nil
}

// ListActiveItems menampilkan semua item aktif dengan batch numbers untuk user selection.
func (s *ConsumptionService) ListActiveItems(ctx context.Context, chatID string) (string, error) {
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
			displayUnit = determineSmallestUnitFromName(cycle.ItemName)
		}

		itemLabel := cycle.ItemName
		if cycle.BatchNumber != "" {
			itemLabel = fmt.Sprintf("%s (%s)", cycle.ItemName, cycle.BatchNumber)
		}

		// Extract actual quantity from item name for accurate display
		displayQty := extractQuantityFromItemName(cycle.ItemName)
		if displayQty == 0 {
			displayQty = totalInSmallestUnit // fallback ke calculated quantity
		}

		result += fmt.Sprintf(
			"%d. %s\n   📦 %g %s (%.0f %s)\n   📅 Mulai: %s (%.0f hari lalu)\n\n",
			i+1, itemLabel,
			cycle.PurchaseQty, cycle.PurchaseUnit, displayQty, displayUnit,
			cycle.StartDate.Format("02/01/2006"), daysInUse,
		)
	}

	result += "💡 Untuk menyelesaikan, ketik: \"[nama item] [batch] sudah habis\""

	return result, nil
}

// GetHistory mendapatkan history siklus konsumsi untuk item tertentu.
func (s *ConsumptionService) GetHistory(ctx context.Context, chatID, itemName string, limit int) (string, error) {
	cycles, err := s.cycleRepo.ListByChat(ctx, chatID, limit)
	if err != nil {
		return "", fmt.Errorf("gagal mengambil history: %w", err)
	}

	if len(cycles) == 0 {
		return fmt.Sprintf("Belum ada data konsumsi untuk %s.", itemName), nil
	}

	var result string
	result += fmt.Sprintf("📊 History Konsumsi: %s\n\n", itemName)

	for i, cycle := range cycles {
		if itemName != "" && cycle.ItemName != itemName {
			continue
		}

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
		totalConsumedInSmallestUnit := cycle.ConsumedQty * cycle.ConversionFactor
		displayUnit := determineSmallestUnit(cycle.PurchaseUnit)

		dailyConsumptionInSmallestUnit := 0.0
		if daysInUse > 0 && totalConsumedInSmallestUnit > 0 {
			dailyConsumptionInSmallestUnit = totalConsumedInSmallestUnit / daysInUse
		}

		result += fmt.Sprintf(
			"%d. %s - %s\n",
			i+1, cycle.StartDate.Format("02/01/2006"), status,
		)
		result += fmt.Sprintf(
			"   Beli: %g %s (%.0f %s), Terpakai: %g %s (%.0f %s)\n",
			cycle.PurchaseQty, cycle.PurchaseUnit, totalPurchasedInSmallestUnit, displayUnit,
			cycle.ConsumedQty, cycle.ConsumedUnit, totalConsumedInSmallestUnit, displayUnit,
		)
		result += fmt.Sprintf(
			"   Durasi: %.0f hari, Rate: %.1f %s/hari\n\n",
			daysInUse, dailyConsumptionInSmallestUnit, displayUnit,
		)
	}

	return result, nil
}

// CompleteCycleWithEndDate menyelesaikan siklus konsumsi dengan tanggal habis yang spesifik.
func (s *ConsumptionService) CompleteCycleWithEndDate(ctx context.Context, chatID, itemName string, endDate time.Time) (*domain.ConsumptionCycle, error) {
	cycle, err := s.cycleRepo.GetActiveByItem(ctx, chatID, itemName)
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

	cycle.ConsumedQty = totalPurchasedInSmallestUnit / cycle.ConversionFactor
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
func (s *ConsumptionService) CalculateDailyConsumption(ctx context.Context, chatID, itemName string, purchaseDate, endDate time.Time, purchaseQty float64, purchaseUnit string, conversionFactor float64) (string, error) {
	daysInUse := endDate.Sub(purchaseDate).Hours() / 24
	if daysInUse <= 0 {
		return "", fmt.Errorf("tanggal habis harus setelah tanggal pembelian")
	}

	totalPurchasedInSmallestUnit := purchaseQty * conversionFactor
	dailyConsumption := totalPurchasedInSmallestUnit / daysInUse
	displayUnit := determineSmallestUnit(purchaseUnit)

	result := fmt.Sprintf(
		"📊 Hasil Perhitungan Konsumsi: %s\n"+
			"📦 Pembelian: %g %s (%.0f %s)\n"+
			"📅 Tanggal Beli: %s\n"+
			"📅 Tanggal Habis: %s\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📈 Konsumsi Per Hari: %.1f %s/hari\n"+
			"📉 Total Konsumsi: %.0f %s",
		itemName,
		purchaseQty, purchaseUnit, totalPurchasedInSmallestUnit, displayUnit,
		purchaseDate.Format("02/01/2006"),
		endDate.Format("02/01/2006"),
		daysInUse,
		dailyConsumption, displayUnit,
		totalPurchasedInSmallestUnit, displayUnit,
	)

	return result, nil
}
