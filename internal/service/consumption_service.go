// Package service menyediakan business logic untuk consumption cycles.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

// ConsumptionService menangani pembuatan dan analisa consumption cycles.
type ConsumptionService struct {
	db          *gorm.DB
	cycleRepo   repository.ConsumptionCycleRepository
	log         *slog.Logger
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
	cycle := &domain.ConsumptionCycle{
		ChatID:           chatID,
		ItemName:         itemName,
		StartDate:        purchaseDate,
		PurchaseQty:      purchaseQty,
		PurchaseUnit:     purchaseUnit,
		ConversionFactor: conversionFactor,
		ConsumedQty:      0,
		ConsumedUnit:     "gr", // selalu pakai satuan terkecil
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

// StartUsage memulai pemakaian item (saat user bilang "pakai susu 400gr").
// Ini akan membuat consumption cycle baru dengan auto-generated batch number.
func (s *ConsumptionService) StartUsage(ctx context.Context, chatID, itemName string, usageQty float64, usageUnit string, conversionFactor float64) (*domain.ConsumptionCycle, error) {
	// Auto-generate batch number
	batchNumber := generateBatchNumber()

	// Cek apakah sudah ada cycle aktif untuk item ini (tanpa batch)
	cycle, err := s.cycleRepo.GetActiveByItem(ctx, chatID, itemName)
	if err == nil && cycle != nil {
		// Sudah ada cycle aktif, update info penggunaan
		cycle.ConsumedQty += usageQty * conversionFactor
		cycle.ConsumedUnit = "gr"
		if err := s.cycleRepo.Update(ctx, cycle); err != nil {
			return nil, fmt.Errorf("gagal update consumption cycle: %w", err)
		}
		s.log.InfoContext(ctx, "consumption cycle diupdate", "item", itemName, "batch", cycle.BatchNumber, "added_qty", usageQty)
		return cycle, nil
	}

	// Belum ada cycle aktif, buat baru dengan auto-generated batch
	cycle = &domain.ConsumptionCycle{
		ChatID:           chatID,
		ItemName:         itemName,
		BatchNumber:      batchNumber,
		StartDate:        time.Now(),
		PurchaseQty:      usageQty,
		PurchaseUnit:     usageUnit,
		ConversionFactor: conversionFactor,
		ConsumedQty:      usageQty * conversionFactor, // set initial consumption
		ConsumedUnit:     "gr",
		Status:           domain.ConsumptionCycleActive,
	}

	if err := s.cycleRepo.Create(ctx, cycle); err != nil {
		return nil, fmt.Errorf("gagal membuat consumption cycle: %w", err)
	}

	s.log.InfoContext(ctx, "consumption cycle dimulai dengan auto-batch", "item", itemName, "batch", batchNumber, "qty", usageQty)
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

	// Update cycle ke completed
	cycle.Status = domain.ConsumptionCycleCompleted
	cycle.EndDate = &endTime
	cycle.ConsumedQty = totalPurchasedInSmallestUnit / cycle.ConversionFactor // Set ke full amount
	cycle.ConsumedUnit = "gr"

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
			"📊 Total: %.0f gr (%.1f %s)\n"+
			"📈 Rate: %.1f gr/hari\n"+
			"📅 Mulai: %s\n"+
			"📅 Selesai: %s",
		itemLabel,
		daysInUse,
		totalPurchasedInSmallestUnit, cycle.PurchaseQty, cycle.PurchaseUnit,
		dailyRate,
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

	return fmt.Sprintf(
		"📊 %s: %s\n"+
			"📦 Beli: %g %s (%.0f gr) pada %s\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📉 Terpakai: %.0f gr\n"+
			"📊 Sisa: %.0f gr (%.1f %s)\n"+
			"📈 Rate: %.1f gr/hari\n"+
			"🔮 Estimasi: %d hari lagi\n"+
			"Status: %s",
		itemLabel,
		status,
		cycle.PurchaseQty, cycle.PurchaseUnit, totalPurchasedInSmallestUnit, cycle.StartDate.Format("02/01/2006"),
		daysInUse,
		totalConsumedInSmallestUnit,
		remainingInSmallestUnit, remainingInSmallestUnit/cycle.ConversionFactor, cycle.PurchaseUnit,
		dailyRateInSmallestUnit,
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

		itemLabel := cycle.ItemName
		if cycle.BatchNumber != "" {
			itemLabel = fmt.Sprintf("%s (%s)", cycle.ItemName, cycle.BatchNumber)
		}

		result += fmt.Sprintf(
			"%d. %s\n   📦 %g %s (%.0f gr)\n   📅 Mulai: %s (%.0f hari lalu)\n\n",
			i+1, itemLabel,
			cycle.PurchaseQty, cycle.PurchaseUnit, totalInSmallestUnit,
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

		dailyConsumptionInSmallestUnit := 0.0
		if daysInUse > 0 && totalConsumedInSmallestUnit > 0 {
			dailyConsumptionInSmallestUnit = totalConsumedInSmallestUnit / daysInUse
		}

		result += fmt.Sprintf(
			"%d. %s - %s\n",
			i+1, cycle.StartDate.Format("02/01/2006"), status,
		)
		result += fmt.Sprintf(
			"   Beli: %g %s (%.0f gr), Terpakai: %g %s (%.0f gr)\n",
			cycle.PurchaseQty, cycle.PurchaseUnit, totalPurchasedInSmallestUnit,
			cycle.ConsumedQty, cycle.ConsumedUnit, totalConsumedInSmallestUnit,
		)
		result += fmt.Sprintf(
			"   Durasi: %.0f hari, Rate: %.1f gr/hari\n\n",
			daysInUse, dailyConsumptionInSmallestUnit,
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

	cycle.ConsumedQty = totalPurchasedInSmallestUnit / cycle.ConversionFactor
	cycle.ConsumedUnit = "gr"
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

	result := fmt.Sprintf(
		"📊 Hasil Perhitungan Konsumsi: %s\n"+
			"📦 Pembelian: %g %s (%.0f gr)\n"+
			"📅 Tanggal Beli: %s\n"+
			"📅 Tanggal Habis: %s\n"+
			"⏰ Durasi: %.0f hari\n"+
			"📈 Konsumsi Per Hari: %.1f gr/hari\n"+
			"📉 Total Konsumsi: %.0f gr",
		itemName,
		purchaseQty, purchaseUnit, totalPurchasedInSmallestUnit,
		purchaseDate.Format("02/01/2006"),
		endDate.Format("02/01/2006"),
		daysInUse,
		dailyConsumption,
		totalPurchasedInSmallestUnit,
	)

	return result, nil
}
