// Package service berisi orkestrasi business logic: ekstraksi LLM,
// persistensi transaksional ke DB, dan pengiriman balasan WhatsApp.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/sender"
)

// MessageSender abstraksi pengiriman pesan ke WhatsApp.
type MessageSender interface {
	Enqueue(msg sender.Message) bool
}

// Agent adalah orchestrator utama (RFC §4.1 langkah 4-6).
type Agent struct {
	db                 *gorm.DB
	chatRepo           repository.ChatRepository
	txnRepo            repository.TransactionRepository
	invRepo            repository.InventoryRepository
	logRepo            repository.StockLogRepository
	consumptionService *ConsumptionService
	llm                llm.Extractor
	intent             llm.IntentExtractor
	sender             MessageSender
	invCache           *cache.Cache // inventory snapshot cache (TTL 5m, invalidated on write)
	log                *slog.Logger
}

// NewAgent membuat agent baru dengan dependency injection.
func NewAgent(
	db *gorm.DB,
	chatRepo repository.ChatRepository,
	txnRepo repository.TransactionRepository,
	invRepo repository.InventoryRepository,
	logRepo repository.StockLogRepository,
	consumptionCycleRepo repository.ConsumptionCycleRepository,
	extractor llm.Extractor,
	intentExtractor llm.IntentExtractor,
	sender MessageSender,
	logger *slog.Logger,
) *Agent {
	if logger == nil {
		logger = slog.Default()
	}

	consumptionService := NewConsumptionService(db, consumptionCycleRepo, logger)

	return &Agent{
		db:                 db,
		chatRepo:           chatRepo,
		txnRepo:            txnRepo,
		invRepo:            invRepo,
		logRepo:            logRepo,
		consumptionService: consumptionService,
		llm:                extractor,
		intent:             intentExtractor,
		sender:             sender,
		invCache:           cache.New(5*time.Minute, 10*time.Minute),
		log:                logger,
	}
}

// Process menjalankan pipeline penuh untuk satu pesan masuk menggunakan LLM-based routing.
// Setiap jalur mengirim balasan ke pengguna via WAHA.
func (a *Agent) Process(ctx context.Context, msg entity.IncomingMessage) error {
	a.log.InfoContext(ctx, "memproses pesan", "chat", msg.ChatID, "sender", msg.UserPhone, "text", msg.Text)

	chat, err := a.chatRepo.GetOrCreate(ctx, msg.ChatID)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal get/create chat", "err", err)
		return a.reply(ctx, msg.ChatID, "Maaf, terjadi kendala. Coba lagi nanti.")
	}

	// LLM Intent Classification
	action, err := a.intent.ClassifyIntent(ctx, msg.Text)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal klasifikasi intent", "err", err)
		return a.reply(ctx, msg.ChatID, "Maaf, gagal memahami pesan. Coba kirim ulang ya.")
	}

	// Route berdasarkan action yang diklasifikasikan oleh LLM
	switch action.Action {
	case domain.ActionInit:
		return a.handleInitAction(ctx, msg, chat, action.Params)

	case domain.ActionHelp:
		return a.reply(ctx, msg.ChatID, OnboardingTemplate)

	case domain.ActionInfo:
		return a.handleInfo(ctx, msg, chat)

	case domain.ActionGetStock:
		return a.handleGetStock(ctx, msg, chat, action.Params)

	case domain.ActionGetReport:
		return a.handleGetReport(ctx, msg, chat, action.Params)

	case domain.ActionConsumption:
		return a.handleConsumptionAction(ctx, msg, action.Params)

	case domain.ActionRecordTransaction:
		return a.handleRecordTransaction(ctx, msg, chat)

	case domain.ActionNone:
		a.log.InfoContext(ctx, "pesan tidak dikenali diabaikan", "text", msg.Text)
		return a.reply(ctx, msg.ChatID, SmallTalkMessage)

	default:
		a.log.WarnContext(ctx, "action tidak dikenali", "action", action.Action)
		return a.reply(ctx, msg.ChatID, "Maaf, gagal mengenali intent pesan.")
	}
}

// persist menjalankan persistensi sesuai tipe transaksi dalam DB transaction.
func (a *Agent) persist(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction) (string, error) {
	switch ext.Type {
	case domain.ExtractionIncome:
		return a.handleIncome(ctx, msg, ext)
	case domain.ExtractionExpense:
		return a.handleExpense(ctx, msg, ext)
	case domain.ExtractionConsumption:
		return a.handleConsumption(ctx, msg, ext)
	default:
		return "", fmt.Errorf("tipe transaksi tidak dikenal: %s", ext.Type)
	}
}

// handleIncome: catat transaksi pemasukan saja (RFC §5.1).
func (a *Agent) handleIncome(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction) (string, error) {
	txnDate, err := parseTransactionDate(ext.TransactionDate)
	if err != nil {
		return "", fmt.Errorf("format tanggal tidak valid: %w", err)
	}

	txn := &domain.Transaction{
		ChatID:          msg.ChatID,
		SenderPhone:     msg.UserPhone,
		Type:            domain.TransactionIncome,
		Category:        ext.Category,
		ItemName:        ext.ItemName,
		Amount:          ext.Amount,
		RawPayload:      msg.Text,
		TransactionDate: txnDate,
	}
	if err := a.txnRepo.WithTx(a.db).Create(ctx, txn); err != nil {
		return "", fmt.Errorf("catat income: %w", err)
	}
	return fmt.Sprintf(
		"Pemasukan tercatat: %s sebesar Rp%s (%s).",
		ext.ItemName, formatRupiah(ext.Amount), ext.Category,
	), nil
}

// handleExpense: catat pengeluaran. Hanya tambah stok bila affects_stock=true (RFC §7.1).
func (a *Agent) handleExpense(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction) (string, error) {
	var inv *domain.Inventory
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Skip financial transaction creation if amount is 0 but affects stock (inventory-only update)
		if ext.Amount == 0 && ext.AffectsStock {
			upserted, err := a.invRepo.WithTx(tx).AddStock(ctx, msg.ChatID, ext.ItemName, ext.Quantity, ext.Unit)
			if err != nil {
				return fmt.Errorf("tambah stok: %w", err)
			}
			inv = upserted

			log := &domain.StockLog{
				InventoryID: inv.ID,
				ChangeType:  domain.StockIn,
				Quantity:    ext.Quantity,
				Notes:       ext.Notes,
			}
			if err := a.logRepo.WithTx(tx).Create(ctx, log); err != nil {
				return fmt.Errorf("catat stock log IN: %w", err)
			}
			return nil
		}

		txnDate, err := parseTransactionDate(ext.TransactionDate)
		if err != nil {
			return fmt.Errorf("format tanggal tidak valid: %w", err)
		}

		var consumptionDate *time.Time
		if ext.ConsumptionDate != "" {
			cd, err := parseTransactionDate(ext.ConsumptionDate)
			if err != nil {
				return fmt.Errorf("format tanggal konsumsi tidak valid: %w", err)
			}
			consumptionDate = &cd
		}

		txn := &domain.Transaction{
			ChatID:          msg.ChatID,
			SenderPhone:     msg.UserPhone,
			Type:            domain.TransactionExpense,
			Category:        ext.Category,
			ItemName:        ext.ItemName,
			Amount:          ext.Amount,
			RawPayload:      msg.Text,
			TransactionDate: txnDate,
			ConsumptionDate: consumptionDate,
			TotalConsumed:   ext.TotalConsumption,
		}
		if err := a.txnRepo.WithTx(tx).Create(ctx, txn); err != nil {
			return fmt.Errorf("catat expense: %w", err)
		}

		// Lewati inventaris bila pengeluaran bukan barang stok (jasa/utilitas/dll).
		if !ext.AffectsStock {
			return nil
		}

		upserted, err := a.invRepo.WithTx(tx).AddStock(ctx, msg.ChatID, ext.ItemName, ext.Quantity, ext.Unit)
		if err != nil {
			return fmt.Errorf("tambah stok: %w", err)
		}
		inv = upserted

		log := &domain.StockLog{
			InventoryID: inv.ID,
			ChangeType:  domain.StockIn,
			Quantity:    ext.Quantity,
			Notes:       ext.Notes,
		}
		if err := a.logRepo.WithTx(tx).Create(ctx, log); err != nil {
			return fmt.Errorf("catat stock log IN: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	// Balasan berbeda tergantung apakah stok ikut tercatat.
	if inv != nil {
		a.invCache.Delete(msg.ChatID) // invalidate cache karena stok berubah

		// Jika amount=0 tapi stok terupdate, berarti ini inventory-only update
		if ext.Amount == 0 {
			return fmt.Sprintf(
				"Stok tercatat: %s +%g %s. Stok saat ini: %g %s.",
				ext.ItemName, ext.Quantity, ext.Unit,
				inv.StockQty, inv.Unit,
			), nil
		}

		// Parse conversion info dari notes (misal "100g per pcs")
		perUnitQty, perUnitUnit := parseConversionInfo(ext.Notes)

		// Hitung total pembelian dalam satuan dasar
		totalPurchased := ext.Quantity
		if perUnitQty > 0 {
			totalPurchased = ext.Quantity * perUnitQty
		}

		// Tampilkan analisa konsumsi bila ada consumption_date dan total_consumption
		var consumptionAnalysis string
		if ext.ConsumptionDate != "" && ext.TotalConsumption > 0 {
			txnDate, _ := parseTransactionDate(ext.TransactionDate)
			consumptionDate, err := parseTransactionDate(ext.ConsumptionDate)
			if err == nil {
				duration := consumptionDate.Sub(txnDate).Hours() / 24 // durasi dalam hari
				if duration > 0 {
					// Hitung rate konsumsi per hari
					dailyRate := ext.TotalConsumption / duration
					percentageConsumed := (ext.TotalConsumption / totalPurchased) * 100

					unitDisplay := perUnitUnit
					if unitDisplay == "" {
						unitDisplay = ext.Unit
					}

					consumptionAnalysis = fmt.Sprintf(
						" Analisa konsumsi: %g dari %g %s (%.0f%%) habis dalam %.0f hari (%s → %s). Rate: %.1f %s/hari.",
						ext.TotalConsumption, totalPurchased, unitDisplay,
						percentageConsumed, duration,
						txnDate.Format("02/01/2006"), consumptionDate.Format("02/01/2006"),
						dailyRate, unitDisplay,
					)
				}
			}
		} else if ext.ConsumptionDate != "" {
			// Hanya tanggal habis tanpa total_consumption
			txnDate, _ := parseTransactionDate(ext.TransactionDate)
			consumptionDate, err := parseTransactionDate(ext.ConsumptionDate)
			if err == nil {
				duration := consumptionDate.Sub(txnDate).Hours() / 24
				if duration > 0 {
					consumptionAnalysis = fmt.Sprintf(
						" Estimasi habis dalam: %.0f hari (%s → %s: %s).",
						duration, txnDate.Format("02/01/2006"),
						consumptionDate.Format("02/01/2006"), formatDuration(duration),
					)
				}
			}
		}

		// Build reply utama
		baseReply := fmt.Sprintf(
			"Pengeluaran tercatat: %s x%g %s = Rp%s (%s). Stok saat ini: %g %s.",
			ext.ItemName, ext.Quantity, ext.Unit,
			formatRupiah(ext.Amount), ext.Category,
			inv.StockQty, inv.Unit,
		)

		if consumptionAnalysis != "" {
			baseReply += consumptionAnalysis
		}

		return baseReply, nil
	}

	baseReply := fmt.Sprintf(
		"Pengeluaran tercatat: %s sebesar Rp%s (%s).",
		ext.ItemName, formatRupiah(ext.Amount), ext.Category,
	)

	// Analisa konsumsi untuk non-stock items
	if ext.ConsumptionDate != "" && ext.TotalConsumption > 0 {
		txnDate, _ := parseTransactionDate(ext.TransactionDate)
		consumptionDate, err := parseTransactionDate(ext.ConsumptionDate)
		if err == nil {
			duration := consumptionDate.Sub(txnDate).Hours() / 24
			if duration > 0 {
				dailyRate := ext.TotalConsumption / duration
				baseReply += fmt.Sprintf(
					" Analisa konsumsi: %g habis dalam %.0f hari. Rate: %.1f /hari.",
					ext.TotalConsumption, duration, dailyRate,
				)
			}
		}
	} else if ext.ConsumptionDate != "" {
		txnDate, _ := parseTransactionDate(ext.TransactionDate)
		consumptionDate, err := parseTransactionDate(ext.ConsumptionDate)
		if err == nil {
			duration := consumptionDate.Sub(txnDate).Hours() / 24
			if duration > 0 {
				baseReply += fmt.Sprintf(" Estimasi habis dalam: %.0f hari.", duration)
			}
		}
	}

	return baseReply, nil
}

// handleConsumption: kurangi stok + log OUT + update consumption cycle (RFC §7.2).
func (a *Agent) handleConsumption(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction) (string, error) {
	// Cek keberadaan barang di inventaris chat.
	inv, err := a.invRepo.WithTx(a.db).GetByChatItem(ctx, msg.ChatID, ext.ItemName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", &businessError{
				msg: fmt.Sprintf("Barang '%s' belum tercatat di inventaris.", ext.ItemName),
			}
		}
		return "", fmt.Errorf("cari inventaris: %w", err)
	}

	// Validasi stok cukup (pesan informatif). Pengurangan tetap atomik di tx.
	if inv.StockQty < ext.Quantity {
		return "", &businessError{
			msg: fmt.Sprintf(
				"Stok %s tidak cukup (sisa %g %s).",
				ext.ItemName, inv.StockQty, inv.Unit,
			),
		}
	}

	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Kurangi stok
		if err := a.invRepo.WithTx(tx).DecreaseStock(ctx, inv.ID, ext.Quantity); err != nil {
			return err
		}

		// Log stock OUT
		log := &domain.StockLog{
			InventoryID: inv.ID,
			ChangeType:  domain.StockOut,
			Quantity:    ext.Quantity,
			Notes:       ext.Notes,
		}
		if err := a.logRepo.WithTx(tx).Create(ctx, log); err != nil {
			return err
		}

		// Start/update consumption cycle dengan conversion factor default
		conversionFactor := 1.0 // default, bisa diupdate nanti
		_, err := a.consumptionService.StartUsage(ctx, msg.ChatID, ext.ItemName, ext.Quantity, ext.Unit, conversionFactor)
		return err
	})
	if err != nil {
		if errors.Is(err, repository.ErrInsufficientStock) {
			return "", &businessError{
				msg: fmt.Sprintf("Stok %s habis/tidak cukup saat pemakaian.", ext.ItemName),
			}
		}
		return "", fmt.Errorf("kurangi stok: %w", err)
	}

	a.invCache.Delete(msg.ChatID) // invalidate cache karena stok berkurang

	// Fetch updated inventory after transaction for accurate remaining stock
	updatedInv, err := a.invRepo.WithTx(a.db).GetByChatItem(ctx, msg.ChatID, ext.ItemName)
	if err != nil {
		// Fallback to calculation if fetch fails
		remaining := inv.StockQty - ext.Quantity
		return fmt.Sprintf(
			"🔄 Pemakaian tercatat: %s -%g %s. Sisa stok: %g %s.\n✅ Consumption cycle: ACTIVE",
			ext.ItemName, ext.Quantity, ext.Unit, remaining, inv.Unit,
		), nil
	}

	return fmt.Sprintf(
		"🔄 Pemakaian tercatat: %s -%g %s. Sisa stok: %g %s.\n✅ Consumption cycle: ACTIVE",
		ext.ItemName, ext.Quantity, ext.Unit, updatedInv.StockQty, updatedInv.Unit,
	), nil
}

// handleConsumptionAction menangani action konsumsi dari LLM.
func (a *Agent) handleConsumptionAction(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}) error {
	// Ambil parameter yang diperlukan
	itemName, ok := params["item_name"].(string)
	if !ok || itemName == "" {
		// Jika tidak ada item_name specific, list semua active items
		result, err := a.consumptionService.ListActiveItems(ctx, msg.ChatID)
		if err != nil {
			return a.reply(ctx, msg.ChatID, fmt.Sprintf("Gagal mengambil list active items: %v", err))
		}
		return a.reply(ctx, msg.ChatID, result)
	}

	actionType, ok := params["consumption_action"].(string)
	if !ok {
		actionType = "info" // default action
	}

	var result string
	var err error

	switch actionType {
	case "use", "start":
		// "pakai" - mulai consumption cycle
		usageQty, ok := params["usage_qty"].(float64)
		if !ok {
			// Coba alternate parameter names
			if qty, ok2 := params["quantity"].(float64); ok2 {
				usageQty = qty
			} else {
				// Default to 1 if quantity not specified
				usageQty = 1.0
			}
		}

		usageUnit, _ := params["usage_unit"].(string)
		conversionFactor, _ := params["conversion_factor"].(float64)

		if usageUnit == "" {
			usageUnit = "pcs"
		}
		if conversionFactor == 0 {
			conversionFactor = 1.0
		}

		// Kurangi stok dan mulai consumption cycle dengan auto-generated batch
		err = a.handleUsageWithConsumption(ctx, msg, itemName, usageQty, usageUnit, conversionFactor)
		return err // handleUsageWithConsumption already sends reply

	case "complete", "finish":
		// "habis" - selesaikan consumption cycle
		batchNumber, _ := params["batch_number"].(string)
		result, err = a.consumptionService.CompleteUsage(ctx, msg.ChatID, itemName, batchNumber)
		if err != nil {
			return a.reply(ctx, msg.ChatID, fmt.Sprintf("Gagal menyelesaikan consumption: %v", err))
		}

		return a.reply(ctx, msg.ChatID, result)

	case "calculate":
		// Menghitung konsumsi harian tanpa menyimpan cycle
		purchaseQty, ok := params["purchase_qty"].(float64)
		if !ok {
			return a.reply(ctx, msg.ChatID, "Maaf, perlu specify jumlah pembelian (purchase_qty).")
		}

		purchaseUnit, _ := params["purchase_unit"].(string)
		conversionFactor, _ := params["conversion_factor"].(float64)

		if purchaseUnit == "" {
			purchaseUnit = "pcs"
		}
		if conversionFactor == 0 {
			conversionFactor = 1.0
		}

		purchaseDateStr, ok := params["purchase_date"].(string)
		if !ok || purchaseDateStr == "" {
			return a.reply(ctx, msg.ChatID, "Maaf, perlu specify tanggal pembelian (purchase_date).")
		}

		endDateStr, ok := params["end_date"].(string)
		if !ok || endDateStr == "" {
			return a.reply(ctx, msg.ChatID, "Maaf, perlu specify tanggal habis (end_date).")
		}

		purchaseDate, err := time.Parse("2006-01-02", purchaseDateStr)
		if err != nil {
			return a.reply(ctx, msg.ChatID, "Format tanggal purchase_date tidak valid (YYYY-MM-DD).")
		}

		endDate, err := time.Parse("2006-01-02", endDateStr)
		if err != nil {
			return a.reply(ctx, msg.ChatID, "Format tanggal end_date tidak valid (YYYY-MM-DD).")
		}

		result, err = a.consumptionService.CalculateDailyConsumption(ctx, msg.ChatID, itemName, purchaseDate, endDate, purchaseQty, purchaseUnit, conversionFactor)
		if err != nil {
			return a.reply(ctx, msg.ChatID, fmt.Sprintf("Gagal menghitung konsumsi: %v", err))
		}

		return a.reply(ctx, msg.ChatID, result)

	case "history":
		// Mendapatkan history konsumsi
		limit := 10
		if limitParam, ok := params["limit"].(float64); ok {
			limit = int(limitParam)
		}

		result, err = a.consumptionService.GetHistory(ctx, msg.ChatID, itemName, limit)
		if err != nil {
			return a.reply(ctx, msg.ChatID, fmt.Sprintf("Gagal mengambil history: %v", err))
		}

		return a.reply(ctx, msg.ChatID, result)

	case "info":
		// Tampilkan info konsumsi aktif untuk item spesifik
		batchNumber, _ := params["batch_number"].(string)
		result, err = a.consumptionService.GetActiveCycleInfo(ctx, msg.ChatID, itemName, batchNumber)
		if err != nil {
			// Jika item tidak ditemukan, tarkan pesan yang lebih informatif
			return a.reply(ctx, msg.ChatID, fmt.Sprintf("Tidak ada consumption cycle aktif untuk '%s'. Ketik 'barang aktif' untuk melihat semua item yang sedang dikonsumsi.", itemName))
		}
		return a.reply(ctx, msg.ChatID, result)

	case "list":
		// List semua active items dengan batch numbers
		result, err = a.consumptionService.ListActiveItems(ctx, msg.ChatID)
		if err != nil {
			return a.reply(ctx, msg.ChatID, fmt.Sprintf("Gagal mengambil list active items: %v", err))
		}

		return a.reply(ctx, msg.ChatID, result)

	default:
		// Default: coba info dulu, jika tidak ada maka list active items
		batchNumber, _ := params["batch_number"].(string)
		result, err = a.consumptionService.GetActiveCycleInfo(ctx, msg.ChatID, itemName, batchNumber)
		if err != nil {
			// Jika tidak ada spesifik item, list semua active items
			result, err = a.consumptionService.ListActiveItems(ctx, msg.ChatID)
			if err != nil {
				return a.reply(ctx, msg.ChatID, fmt.Sprintf("Gagal mengambil list active items: %v", err))
			}
			return a.reply(ctx, msg.ChatID, result)
		}
		return a.reply(ctx, msg.ChatID, result)
	}
}

// handleUsageWithConsumption menangani "pakai" action: kurangi stok + mulai consumption cycle.
func (a *Agent) handleUsageWithConsumption(ctx context.Context, msg entity.IncomingMessage, itemName string, usageQty float64, usageUnit string, conversionFactor float64) error {
	// Cek inventory item dulu
	inv, err := a.invRepo.WithTx(a.db).GetByChatItem(ctx, msg.ChatID, itemName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return a.reply(ctx, msg.ChatID, fmt.Sprintf("Barang '%s' belum ada di inventaris. Beli dulu ya!", itemName))
		}
		return a.reply(ctx, msg.ChatID, fmt.Sprintf("Gagal cek inventaris: %v", err))
	}

	// Validasi stok cukup
	if inv.StockQty < usageQty {
		return a.reply(ctx, msg.ChatID, fmt.Sprintf("Stok %s tidak cukup! Sisa: %.1f %s", itemName, inv.StockQty, inv.Unit))
	}

	// Jalankan dalam transaction: kurangi stok + mulai/updates consumption cycle
	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Kurangi stok
		if err := a.invRepo.WithTx(tx).DecreaseStock(ctx, inv.ID, usageQty); err != nil {
			return err
		}

		// Log stock movement
		log := &domain.StockLog{
			InventoryID: inv.ID,
			ChangeType:  domain.StockOut,
			Quantity:    usageQty,
			Notes:       "Mulai pemakaian - consumption cycle",
		}
		if err := a.logRepo.WithTx(tx).Create(ctx, log); err != nil {
			return err
		}

		// Mulai/update consumption cycle dengan auto-generated batch
		cycle, err := a.consumptionService.StartUsage(ctx, msg.ChatID, itemName, usageQty, usageUnit, conversionFactor)
		if err != nil {
			return err
		}

		// Simpan batch number untuk reply message
		a.log.DebugContext(ctx, "consumption cycle created/updated", "item", itemName, "batch", cycle.BatchNumber)
		return nil
	})

	if err != nil {
		a.log.ErrorContext(ctx, "gagal handle usage dengan consumption", "err", err)
		return a.reply(ctx, msg.ChatID, fmt.Sprintf("Gagal mulai pemakaian: %v", err))
	}

	// Invalidate cache
	a.invCache.Delete(msg.ChatID)

	// Get updated stock dan active cycle untuk batch info
	updatedInv, err := a.invRepo.WithTx(a.db).GetByChatItem(ctx, msg.ChatID, itemName)
	if err != nil {
		return a.reply(ctx, msg.ChatID, fmt.Sprintf("🔄 Pemakaian %s %.1f %s dicatat. Consumption cycle dimulai dengan auto-generated batch!", itemName, usageQty, usageUnit))
	}

	// Get cycle info untuk menampilkan batch number
	cycle, err := a.consumptionService.cycleRepo.GetActiveByItem(ctx, msg.ChatID, itemName)
	if err != nil {
		return a.reply(ctx, msg.ChatID, fmt.Sprintf(
			"🔄 Pemakaian %s %.1f %s dicatat.\n✅ Consumption cycle: OPEN\n📦 Sisa stok: %.1f %s",
			itemName, usageQty, usageUnit, updatedInv.StockQty, updatedInv.Unit,
		))
	}

	batchInfo := ""
	if cycle.BatchNumber != "" {
		batchInfo = fmt.Sprintf(" (%s)", cycle.BatchNumber)
	}

	return a.reply(ctx, msg.ChatID, fmt.Sprintf(
		"🔄 Pemakaian %s%s %.1f %s dicatat.\n✅ Consumption cycle: OPEN\n📦 Sisa stok: %.1f %s",
		itemName, batchInfo, usageQty, usageUnit, updatedInv.StockQty, updatedInv.Unit,
	))
}

// initReply memilih template konfirmasi init sesuai ada/tidak-nya nama ledger.
func initReply(name string) string {
	if name == "" {
		return InitSuccessMessage
	}
	return fmt.Sprintf(InitSuccessNamedMessage, name)
}

// ── LLM-based Action Handlers ──

// handleInitAction menangani action init (aktivasi ledger).
func (a *Agent) handleInitAction(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, params map[string]interface{}) error {
	// Extract ledger name dari params
	var ledgerName string
	if name, ok := params["ledger_name"].(string); ok {
		ledgerName = name
	}

	if !chat.Initialized {
		if err := a.chatRepo.MarkInitialized(ctx, msg.ChatID, ledgerName); err != nil {
			a.log.ErrorContext(ctx, "gagal mark init", "err", err)
		}
		a.log.InfoContext(ctx, "chat melakukan init", "chat", msg.ChatID, "name", ledgerName)
		return a.reply(ctx, msg.ChatID, initReply(ledgerName))
	}

	// Sudah init: update nama bila diberikan, kalau tidak cukup balas status.
	if ledgerName != "" {
		if err := a.chatRepo.MarkInitialized(ctx, msg.ChatID, ledgerName); err != nil {
			a.log.ErrorContext(ctx, "gagal rename ledger", "err", err)
		}
		a.log.InfoContext(ctx, "ledger di-rename", "chat", msg.ChatID, "name", ledgerName)
		return a.reply(ctx, msg.ChatID, fmt.Sprintf("Nama ledger diperbarui: %s", ledgerName))
	}
	return a.reply(ctx, msg.ChatID, "Akun sudah aktif. Ketik \"bantuan\" untuk format.")
}

// handleGetStock menangani action get_stock (query stok/inventory).
func (a *Agent) handleGetStock(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, params map[string]interface{}) error {
	if !chat.Initialized {
		return a.reply(ctx, msg.ChatID, PreInitMessage)
	}

	// Extract item filter dari params
	var itemFilter string
	if filter, ok := params["item_filter"].(string); ok {
		itemFilter = filter
	}

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

// handleGetReport menangani action get_report (query laporan).
func (a *Agent) handleGetReport(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, params map[string]interface{}) error {
	if !chat.Initialized {
		return a.reply(ctx, msg.ChatID, PreInitMessage)
	}

	// Extract parameters dari params
	reportType := "summary" // default
	period := "today"       // default
	var itemFilter string
	var customDateRange string

	if rt, ok := params["report_type"].(string); ok {
		reportType = rt
	}
	if p, ok := params["period"].(string); ok {
		period = p
	}
	if filter, ok := params["item_filter"].(string); ok {
		itemFilter = filter
	}
	if cdr, ok := params["custom_date_range"].(string); ok {
		customDateRange = cdr
	}

	// Create action object for passing to generateReport
	action := domain.ServiceAction{
		Action: domain.ActionGetReport,
		Params: params,
	}

	return a.generateReport(ctx, msg, action, reportType, period, itemFilter, customDateRange)
}

// handleRecordTransaction menangani action record_transaction (pencatatan transaksi).
func (a *Agent) handleRecordTransaction(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat) error {
	if !chat.Initialized {
		return a.reply(ctx, msg.ChatID, PreInitMessage)
	}

	// Path pencatatan: ekstraksi LLM -> persist.
	// Sertakan snapshot inventory (pakai cache) agar LLM meresolve nama barang
	// ke item yang sudah ada di inventory chat ini.
	items := a.cachedInventory(ctx, msg.ChatID)
	invContext := llm.BuildInventoryPrompt(items)
	ext, err := a.llm.Extract(ctx, msg.Text, invContext)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal ekstraksi LLM", "err", err)
		return a.reply(ctx, msg.ChatID, "Maaf, gagal memahami pesan. Coba kirim ulang ya.")
	}

	// Pesan non-transaksi (sapaan/chitchat): jangan dicatat, balas ramah.
	if ext.Type == domain.ExtractionNone {
		a.log.InfoContext(ctx, "pesan non-transaksi diabaikan", "text", msg.Text)
		return a.reply(ctx, msg.ChatID, SmallTalkMessage)
	}

	reply, err := a.persist(ctx, msg, ext)
	if err != nil {
		var be *businessError
		if errors.As(err, &be) {
			return a.reply(ctx, msg.ChatID, be.msg)
		}
		a.log.ErrorContext(ctx, "gagal persistensi", "err", err, "type", ext.Type)
		return a.reply(ctx, msg.ChatID, "Maaf, terjadi kendala saat mencatat. Coba lagi nanti.")
	}
	return a.reply(ctx, msg.ChatID, reply)
}

// cachedInventory mengembalikan snapshot inventory chat dari cache (TTL 5m)
// atau dari DB bila cache miss. Dipakai sebagai konteks LLM agar LLM dapat
// meresolve nama barang (mis. "susu" → "susu uht" di inventory).
func (a *Agent) cachedInventory(ctx context.Context, chatID string) []domain.Inventory {
	if cached, found := a.invCache.Get(chatID); found {
		return cached.([]domain.Inventory)
	}
	items, err := a.invRepo.WithTx(a.db).ListByChat(ctx, chatID)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal load inventory untuk cache", "err", err)
		return nil
	}
	a.invCache.Set(chatID, items, cache.DefaultExpiration)
	return items
}

// handleInfo merangkai pesan metadata sesi/chat untuk diagnostic.
// Selalu tersedia (pre-init maupun post-init).
func (a *Agent) handleInfo(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat) error {
	count, err := a.txnRepo.WithTx(a.db).CountByChat(ctx, msg.ChatID)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal query count transaksi", "err", err)
		count = -1 // tetap kirim info, tandai error
	}

	chatType := "Privat"
	switch {
	case strings.HasSuffix(msg.ChatID, "@g.us"):
		chatType = "Group"
	case strings.HasSuffix(msg.ChatID, "@lid"):
		chatType = "Privat (LID)"
	}

	status := "Belum init"
	if chat.Initialized {
		status = "Aktif"
	}

	countStr := fmt.Sprintf("%d tercatat", count)
	if count < 0 {
		countStr = "(gagal mengambil)"
	}

	var b strings.Builder
	b.WriteString("Info Sesi\n")
	fmt.Fprintf(&b, "Chat ID   : %s\n", msg.ChatID)
	if chat.Name != "" {
		fmt.Fprintf(&b, "Nama      : %s\n", chat.Name)
	}
	fmt.Fprintf(&b, "Tipe      : %s\n", chatType)
	fmt.Fprintf(&b, "Status    : %s\n", status)
	fmt.Fprintf(&b, "Sender    : %s\n", msg.UserPhone)
	fmt.Fprintf(&b, "Session   : %s\n", msg.SessionName)
	fmt.Fprintf(&b, "Bot ID    : %s\n", msg.BotID)
	fmt.Fprintf(&b, "Bot LID   : %s\n", msg.BotLid)
	fmt.Fprintf(&b, "Transaksi : %s\n", countStr)
	return a.reply(ctx, msg.ChatID, b.String())
}

// generateReport membuat laporan berdasarkan parameter yang diekstrak oleh LLM.
func (a *Agent) generateReport(ctx context.Context, msg entity.IncomingMessage, action domain.ServiceAction, reportType, periodType, itemFilter, customDateRange string) error {
	// Parse period ke time range
	var from, to time.Time
	now := time.Now()

	switch periodType {
	case "today":
		from = startOfDay(now)
		to = now
	case "yesterday":
		from = startOfDay(now).AddDate(0, 0, -1)
		to = from.Add(24*time.Hour - time.Second)
	case "this_week":
		from = startOfWeek(now)
		to = now
	case "last_week":
		thisWeek := startOfWeek(now)
		from = thisWeek.AddDate(0, 0, -7)
		to = thisWeek.Add(-time.Second)
	case "this_month":
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		to = now
	case "last_month":
		firstThis := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		from = firstThis.AddDate(0, -1, 0)
		to = firstThis.Add(-time.Second)
	case "custom":
		// Parse tanggal langsung dari parameter LLM (from_date, to_date)
		if fromDate, ok := action.Params["from_date"].(string); ok && fromDate != "" {
			parsedFrom, err := parseLLMDate(fromDate)
			if err == nil {
				from = parsedFrom
			} else {
				a.log.WarnContext(ctx, "gagal parse from_date dari LLM", "from_date", fromDate, "err", err)
				from = startOfDay(now)
			}
		} else if customDateRange != "" {
			// Fallback ke parsing manual jika from_date tidak ada
			a.log.InfoContext(ctx, "using custom_date_range fallback", "custom_date_range", customDateRange)
			// Untuk sementara gunakan today sebagai fallback
			from = startOfDay(now)
			to = now
		} else {
			// Fallback jika tidak ada from_date
			from = startOfDay(now)
		}

		if toDate, ok := action.Params["to_date"].(string); ok && toDate != "" {
			parsedTo, err := parseLLMDate(toDate)
			if err == nil {
				to = parsedTo
			} else {
				a.log.WarnContext(ctx, "gagal parse to_date dari LLM", "to_date", toDate, "err", err)
				to = now
			}
		} else if customDateRange != "" {
			// Gunakan same fallback untuk to_date
			to = now
		} else {
			// Fallback jika tidak ada to_date
			to = now
		}
	case "all":
		from = time.Time{}
		to = now
	default:
		// Default ke today
		from = startOfDay(now)
		to = now
	}

	// Generate report based on type
	switch reportType {
	case "summary", "income", "expense":
		summary, err := a.txnRepo.WithTx(a.db).Summary(ctx, msg.ChatID, from, to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query ringkasan", "err", err)
			return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil laporan.")
		}

		// Convert reportType to metric
		var metric reportMetric
		switch reportType {
		case "income":
			metric = metricIncome
		case "expense":
			metric = metricExpense
		default:
			metric = metricSummary
		}

		periodLabel := formatPeriodLabel(periodType, from, to)
		periodStruct := period{from: from, to: to, label: periodLabel}
		return a.reply(ctx, msg.ChatID, formatTxnReport(metric, periodStruct, summary))

	case "expense_by_item":
		items, err := a.txnRepo.WithTx(a.db).ExpenseByItem(ctx, msg.ChatID, from, to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query per item", "err", err)
			return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil laporan.")
		}
		periodLabel := formatPeriodLabel(periodType, from, to)
		periodStruct := period{from: from, to: to, label: periodLabel}
		return a.reply(ctx, msg.ChatID, formatExpenseByItem(periodStruct, items))

	case "consumption":
		moves, err := a.logRepo.WithTx(a.db).MovementsByChat(ctx, msg.ChatID, from, to)
		if err != nil {
			a.log.ErrorContext(ctx, "gagal query pemakaian", "err", err)
			return a.reply(ctx, msg.ChatID, "Maaf, gagal mengambil laporan.")
		}
		periodLabel := formatPeriodLabel(periodType, from, to)
		periodStruct := period{from: from, to: to, label: periodLabel}
		return a.reply(ctx, msg.ChatID, formatConsumption(periodStruct, moves))

	default:
		return a.reply(ctx, msg.ChatID, "Maaf, tipe laporan tidak dikenali.")
	}
}

// formatPeriodLabel membuat label untuk periode berdasarkan type dan date range
func formatPeriodLabel(period string, from, to time.Time) string {
	switch period {
	case "today":
		return "hari ini (" + formatDay(from) + ")"
	case "yesterday":
		return "kemarin"
	case "this_week":
		return "minggu ini"
	case "last_week":
		return "minggu lalu"
	case "this_month":
		return "bulan ini"
	case "last_month":
		return formatMonth(from)
	case "all":
		return "sejauh ini"
	case "custom":
		return fmt.Sprintf("%s - %s", from.Format("02/01/2006"), to.Format("02/01/2006"))
	default:
		return "periode ini"
	}
}

// parseLLMDate memparsing tanggal dari LLM dengan berbagai format
// Mendukung: "YYYY-MM-DD", "DD/MM/YYYY", "DD-MM-YYYY", "DD/MM", "DD-MM"
// Untuk to_date, otomatis set jam ke 23:59:59 untuk include seluruh hari tersebut
func parseLLMDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		return time.Now(), nil
	}

	// Try format YYYY-MM-DD dulu (standard ISO)
	if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
		return parsed, nil
	}

	// Try format DD/MM/YYYY (format Indonesia)
	if parsed, err := time.Parse("02/01/2006", dateStr); err == nil {
		// Set jam ke 23:59:59 untuk include seluruh hari
		year := parsed.Year()
		month := parsed.Month()
		day := parsed.Day()
		return time.Date(year, month, day, 23, 59, 59, 0, time.UTC), nil
	}

	// Try format DD-MM-YYYY
	if parsed, err := time.Parse("02-01-2006", dateStr); err == nil {
		// Set jam ke 23:59:59 untuk include seluruh hari
		year := parsed.Year()
		month := parsed.Month()
		day := parsed.Day()
		return time.Date(year, month, day, 23, 59, 59, 0, time.UTC), nil
	}

	// Try format DD/MM (hanya hari dan bulan, tahun di-set ke tahun sekarang)
	if parsed, err := time.Parse("02/01", dateStr); err == nil {
		year := time.Now().Year()
		return time.Date(year, parsed.Month(), parsed.Day(), 23, 59, 59, 0, time.UTC), nil
	}

	// Try format DD-MM (hanya hari dan bulan dengan dash)
	if parsed, err := time.Parse("02-01", dateStr); err == nil {
		year := time.Now().Year()
		return time.Date(year, parsed.Month(), parsed.Day(), 23, 59, 59, 0, time.UTC), nil
	}

	return time.Now(), fmt.Errorf("format tanggal tidak dikenali: %s (gunakan YYYY-MM-DD, DD/MM/YYYY, atau DD/MM)", dateStr)
}

// reply membungkus pengiriman pesan dengan logging.
func (a *Agent) reply(ctx context.Context, chatID, text string) error {
	msg := sender.Message{
		ChatID: chatID,
		Text:   text,
	}

	if !a.sender.Enqueue(msg) {
		a.log.ErrorContext(ctx, "gagal meng-enqueue balasan", "chat", chatID)
		return fmt.Errorf("gagal meng-enqueue balasan ke waha sender")
	}

	preview := text
	if len(text) > 50 {
		preview = text[:50] + "..."
	}
	a.log.InfoContext(ctx, "balasan di-enqueue ke waha sender", "chat", chatID, "preview", preview)
	return nil
}

// ── Helpers ──

// businessError menandai error yang sudah membawa pesan siap tampil.
type businessError struct{ msg string }

func (e *businessError) Error() string { return e.msg }

// formatRupiah memformat angka ke "1.000.000" (titik pemisah ribuan).
func formatRupiah(n float64) string {
	rounded := int64(n)
	s := strconv.FormatInt(rounded, 10)
	return insertThousands(s)
}

func insertThousands(s string) string {
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(ch)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// parseTransactionDate mengubah string tanggal dari ekstraksi LLM ke time.Time.
// Jika string kosong, gunakan waktu saat ini (hari ini).
// Format yang didukung: "YYYY-MM-DD", "DD/MM/YYYY", "DD/MM/YY", "DD/MM", "DD-MM".
func parseTransactionDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		// Jika tidak ada tanggal yang disebutkan, gunakan tanggal hari ini
		return time.Now(), nil
	}

	// Try format YYYY-MM-DD dulu (standard ISO)
	if parsed, err := time.Parse("2006-01-02", dateStr); err == nil {
		return parsed, nil
	}

	// Try format DD/MM/YYYY (format Indonesia)
	if parsed, err := time.Parse("02/01/2006", dateStr); err == nil {
		return parsed, nil
	}

	// Try format DD/MM/YY (format pendek dengan 2 digit tahun)
	if parsed, err := time.Parse("02/01/06", dateStr); err == nil {
		// Tambahkan 2000 untuk tahun 2 digit (contoh: 25 -> 2025)
		year := parsed.Year()
		if year < 100 {
			parsed = parsed.AddDate(2000-year, 0, 0)
		}
		return parsed, nil
	}

	// Try format DD/MM (hanya hari dan bulan, tahun di-set ke 2025)
	if parsed, err := time.Parse("02/01", dateStr); err == nil {
		// Set tahun ke 2025
		parsed = time.Date(2025, parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
		return parsed, nil
	}

	// Try format DD-MM (hanya hari dan bulan dengan dash)
	if parsed, err := time.Parse("02-01", dateStr); err == nil {
		// Set tahun ke 2025
		parsed = time.Date(2025, parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
		return parsed, nil
	}

	// Try format DD-MM-YY (dengan 2 digit tahun)
	if parsed, err := time.Parse("02-01-06", dateStr); err == nil {
		year := parsed.Year()
		if year < 100 {
			parsed = parsed.AddDate(2000-year, 0, 0)
		}
		return parsed, nil
	}

	return time.Now(), fmt.Errorf("format tanggal tidak dikenali: %s (gunakan DD/MM atau DD/MM/YYYY)", dateStr)
}

// formatDuration mengubah durasi dalam hari menjadi format yang mudah dibaca
func formatDuration(days float64) string {
	if days < 1 {
		return "< 1 hari"
	}
	if days == 1 {
		return "1 hari"
	}
	if days < 7 {
		return fmt.Sprintf("%.0f hari", days)
	}
	if days < 30 {
		weeks := days / 7
		if weeks == 1 {
			return "1 minggu"
		}
		return fmt.Sprintf("%.0f minggu", weeks)
	}
	months := days / 30
	if months == 1 {
		return "1 bulan"
	}
	return fmt.Sprintf("%.0f bulan", months)
}

// parseConversionInfo mengambil informasi konversi dari notes
// Contoh: "100g per pcs" → (100, "g")
// Contoh: "200ml per botol" → (200, "ml")
// Contoh: "" → (0, "")
func parseConversionInfo(notes string) (float64, string) {
	if notes == "" {
		return 0, ""
	}

	// Cari pattern angka + unit + "per" + unit packaging
	// Menggunakan regex sederhana
	lowerNotes := strings.ToLower(notes)

	// Pattern 1: "100g per pcs", "200ml per botol", "1kg per pack"
	re := `(\d+(?:\.\d+)?)\s*([a-zA-Z]+)\s*per\s*([a-zA-Z]+)`
	matches := regexp.MustCompile(re).FindStringSubmatch(lowerNotes)

	if len(matches) >= 3 {
		// matches[0] = full match
		// matches[1] = number (100, 200, etc)
		// matches[2] = unit (g, ml, kg, etc)
		// matches[3] = packaging unit (pcs, botol, pack, etc)

		quantity, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			unit := matches[2]
			// Normalize unit
			switch unit {
			case "gram", "g":
				unit = "g"
			case "mililiter", "mililitre", "ml":
				unit = "ml"
			case "kilogram", "kg":
				unit = "kg"
			}
			return quantity, unit
		}
	}

	return 0, ""
}
