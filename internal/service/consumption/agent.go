package consumption

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/service/agent"
)

// consumptionAgent menangani consumption cycle (pakai/terpakai/habis,
// history, info, list) sebagai action LLM `consumption`.
type consumptionAgent struct {
	db                 *gorm.DB
	goodsRepo          repository.GoodsRepository
	invRepo            repository.InventoryRepository
	logRepo            repository.StockLogRepository
	consumptionService *Service
	invCache           *cache.Cache // shared dengan transactionAgent (invalidate silang)
	// systemPrompt adalah prompt skill agent ini (lihat prompt.go);
	// dipakai bila agent diberi LLM call sendiri.
	systemPrompt string
	// reasoner menalar konversi satuan di jalur ambigu (nil = fallback
	// ke pertanyaan template deterministik, dipakai di test).
	reasoner llm.ConversionReasoner
	// pending menyimpan konfirmasi batch menunggu jawaban user ("1"/"2").
	pending *agent.PendingConfirms
	sender  agent.MessageSender
	log     *slog.Logger
}

func NewAgent(
	db *gorm.DB,
	goodsRepo repository.GoodsRepository,
	invRepo repository.InventoryRepository,
	logRepo repository.StockLogRepository,
	consumptionService *Service,
	invCache *cache.Cache,
	pending *agent.PendingConfirms,
	sender agent.MessageSender,
	logger *slog.Logger,
) agent.SubAgent {
	return NewAgentWithReasoner(db, goodsRepo, invRepo, logRepo, consumptionService, invCache, pending, nil, sender, logger)
}

// NewAgentWithReasoner seperti NewAgent plus penalar konversi LLM untuk
// jalur ambigu (dipakai composition root di cmd/server).
func NewAgentWithReasoner(
	db *gorm.DB,
	goodsRepo repository.GoodsRepository,
	invRepo repository.InventoryRepository,
	logRepo repository.StockLogRepository,
	consumptionService *Service,
	invCache *cache.Cache,
	pending *agent.PendingConfirms,
	reasoner llm.ConversionReasoner,
	sender agent.MessageSender,
	logger *slog.Logger,
) agent.SubAgent {
	return &consumptionAgent{
		db:                 db,
		goodsRepo:          goodsRepo,
		invRepo:            invRepo,
		logRepo:            logRepo,
		consumptionService: consumptionService,
		invCache:           invCache,
		pending:            pending,
		reasoner:           reasoner,
		systemPrompt:       consumptionSystemPrompt,
		sender:             sender,
		log:                logger,
	}
}

func (a *consumptionAgent) Actions() []string {
	return []string{domain.ActionConsumption}
}

// SystemPrompt mengembalikan prompt skill milik agent ini.
func (a *consumptionAgent) SystemPrompt() string { return a.systemPrompt }

func (a *consumptionAgent) Handle(ctx context.Context, req agent.Request) error {
	return a.handleConsumptionAction(ctx, req.Message, req.Action.Params, req.IntentCost)
}

// handleConsumptionAction menangani action konsumsi dari LLM.
func (a *consumptionAgent) handleConsumptionAction(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}, intentCost float64) error {
	a.log.InfoContext(ctx, "handler: consumption", "params", params)
	// Ambil parameter yang diperlukan
	itemName, ok := params["item_name"].(string)
	if !ok || itemName == "" {
		// Jika tidak ada item_name specific, list semua active items
		result, err := a.consumptionService.ListActiveItems(ctx, msg.ChatID)
		if err != nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal mengambil list active items: %v", err), intentCost)
		}
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)
	}

	actionType, ok := params["consumption_action"].(string)
	if !ok {
		actionType = "info" // default action
	}

	// Resolve nama barang hasil LLM ke inventory (via relasi goods): exact →
	// ILIKE → saring via pesan asli. Classifier meniru kapitalisasi user
	// ("Susu BMT 200g") sedangkan master goods tersimpan lowercase — query
	// exact match akan gagal. goods non-nil hanya bila resolve sukses; branch
	// tanpa goods membalas pesan informatif "tidak ada siklus aktif".
	var goods *domain.Good
	if inv, rerr := agent.ResolveInventoryItem(ctx, a.db, a.goodsRepo, a.invRepo, msg.ChatID, msg.Text, itemName); rerr == nil {
		itemName = inv.Name()
		goods = inv.Good
	} else {
		var amb *agent.AmbiguousInventoryError
		if errors.As(rerr, &amb) {
			// Beberapa kandidat barang ("susu" → uht/bmt/bubuk): minta user
			// pilih nomor; jawaban "1"/"2" di-resolve orchestrator tanpa LLM.
			return a.confirmItemChoice(ctx, msg, params, amb, intentCost)
		}
		// Item tidak ada di inventory / gagal DB: lanjut dengan nama apa adanya —
		// branch yang butuh goods akan membalas pesan informatif.
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
		usageDate, _ := params["usage_date"].(string)
		a.log.InfoContext(ctx, "consumption use", "item", itemName, "usage_qty", usageQty, "usage_unit", usageUnit, "usage_date", usageDate)

		// conversion_factor hanya fallback bila nama barang TIDAK memuat
		// ukuran; bila ada (mis. "susu bmt 200g"), StartUsage menurunkan
		// faktor gr/ml sendiri dari nama barang hasil resolusi.
		conversionFactor, _ := params["conversion_factor"].(float64)
		if usageUnit == "" {
			usageUnit = "pcs"
		}
		if conversionFactor == 0 {
			conversionFactor = 1.0
		}

		// Jawaban pertanyaan faktor konversi ("15lt"): simpan ke inventory
		// lalu lanjutkan pemakaian — konversi kini memakai isi tersimpan.
		if ans, has := params["conversion_answer"].(string); has && ans != "" {
			if stop, err := a.applyConversionAnswer(ctx, msg, itemName, ans, intentCost); stop {
				return err
			}
		}

		// Kurangi stok dan mulai consumption cycle dengan auto-generated batch
		return a.handleUsageWithConsumption(ctx, msg, itemName, usageQty, usageUnit, conversionFactor, usageDate, intentCost)

	case "update":
		// "terpakai" - update nilai konsumsi untuk cycle yang sudah ada
		batchNumber, _ := params["batch_number"].(string)
		a.log.InfoContext(ctx, "consumption update", "item", itemName, "batch", batchNumber, "params", params)
		if batchNumber == "" {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, perlu sebut batch number untuk update konsumsi. Contoh: \"terpakai susu uht 500ml (AUG-12-152714) 100ml\"", intentCost)
		}

		updateQty, ok := params["usage_qty"].(float64)
		if !ok {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, perlu sebut jumlah konsumsi untuk update. Contoh: \"terpakai susu uht 500ml (AUG-12-152714) 100ml\"", intentCost)
		}

		updateUnit, _ := params["usage_unit"].(string)
		if updateUnit == "" {
			updateUnit = "ml" // default unit
		}

		if goods == nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal update konsumsi: tidak ada siklus aktif untuk %s (batch %s)", itemName, batchNumber), intentCost)
		}

		// Update consumption cycle
		result, err = a.consumptionService.UpdateConsumption(ctx, msg.ChatID, goods, batchNumber, updateQty, updateUnit)
		if err != nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal update konsumsi: %v", err), intentCost)
		}

		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)

	case "complete", "finish":
		// "habis" - selesaikan consumption cycle
		batchNumber, _ := params["batch_number"].(string)
		a.log.InfoContext(ctx, "consumption complete", "item", itemName, "batch", batchNumber)

		if goods == nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal menyelesaikan consumption: tidak ada siklus aktif untuk %s", itemName), intentCost)
		}

		// Beberapa batch aktif tanpa batch disebut → minta konfirmasi dulu.
		if stop, err := a.confirmBatchIfNeeded(ctx, msg, "complete", params, goods, batchNumber, intentCost); stop {
			return err
		}

		// "susu habis 20/01" — tanggal habis eksplisit; tanpa itu pakai waktu sekarang.
		var endAt time.Time
		if dateStr, ok := params["usage_date"].(string); ok && dateStr != "" {
			// Catatan: jangan pakai `:=` untuk err di sini — err bayangan
			// menelan error service dan balasan menjadi kosong.
			parsed, dateErr := parseUsageDate(dateStr)
			if dateErr != nil {
				return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Format tanggal habis tidak valid (pakai DD/MM atau YYYY-MM-DD).", intentCost)
			}
			endAt = parsed
			result, err = a.consumptionService.CompleteUsageWithDate(ctx, msg.ChatID, goods, batchNumber, endAt)
		} else {
			result, err = a.consumptionService.CompleteUsage(ctx, msg.ChatID, goods, batchNumber)
		}
		if err != nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal menyelesaikan consumption: %v", err), intentCost)
		}

		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)

	case "calculate":
		// Menghitung konsumsi harian tanpa menyimpan cycle
		purchaseQty, ok := params["purchase_qty"].(float64)
		if !ok {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, perlu specify jumlah pembelian (purchase_qty).", intentCost)
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
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, perlu specify tanggal pembelian (purchase_date).", intentCost)
		}

		endDateStr, ok := params["end_date"].(string)
		if !ok || endDateStr == "" {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, perlu specify tanggal habis (end_date).", intentCost)
		}

		purchaseDate, err := time.Parse("2006-01-02", purchaseDateStr)
		if err != nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Format tanggal purchase_date tidak valid (YYYY-MM-DD).", intentCost)
		}

		endDate, err := time.Parse("2006-01-02", endDateStr)
		if err != nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Format tanggal end_date tidak valid (YYYY-MM-DD).", intentCost)
		}

		result, err = a.consumptionService.CalculateDailyConsumption(ctx, msg.ChatID, itemName, purchaseDate, endDate, purchaseQty, purchaseUnit, conversionFactor)
		if err != nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal menghitung konsumsi: %v", err), intentCost)
		}

		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)

	case "history":
		// Mendapatkan history konsumsi
		limit := 10
		if limitParam, ok := params["limit"].(float64); ok {
			limit = int(limitParam)
		}

		if goods == nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Belum ada data konsumsi untuk %s.", itemName), intentCost)
		}

		result, err = a.consumptionService.GetHistory(ctx, msg.ChatID, goods, limit)
		if err != nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal mengambil history: %v", err), intentCost)
		}

		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)

	case "info":
		// Tampilkan info konsumsi aktif untuk item spesifik
		batchNumber, _ := params["batch_number"].(string)

		if goods == nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Tidak ada consumption cycle aktif untuk '%s'. Ketik 'barang aktif' untuk melihat semua item yang sedang dikonsumsi.", itemName), intentCost)
		}

		// Beberapa batch aktif tanpa batch disebut → minta konfirmasi dulu.
		if stop, err := a.confirmBatchIfNeeded(ctx, msg, "info", params, goods, batchNumber, intentCost); stop {
			return err
		}

		result, err = a.consumptionService.GetActiveCycleInfo(ctx, msg.ChatID, goods, batchNumber)
		if err != nil {
			// Jika item tidak ditemukan, tarkan pesan yang lebih informatif
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Tidak ada consumption cycle aktif untuk '%s'. Ketik 'barang aktif' untuk melihat semua item yang sedang dikonsumsi.", itemName), intentCost)
		}
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)

	case "list":
		// List semua active items dengan batch numbers
		result, err = a.consumptionService.ListActiveItems(ctx, msg.ChatID)
		if err != nil {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal mengambil list active items: %v", err), intentCost)
		}

		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)

	default:
		// Default: coba info dulu, jika tidak ada maka list active items
		batchNumber, _ := params["batch_number"].(string)

		if goods == nil {
			result, err = a.consumptionService.ListActiveItems(ctx, msg.ChatID)
			if err != nil {
				return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal mengambil list active items: %v", err), intentCost)
			}
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)
		}

		// Beberapa batch aktif tanpa batch disebut → minta konfirmasi dulu.
		if stop, err := a.confirmBatchIfNeeded(ctx, msg, "info", params, goods, batchNumber, intentCost); stop {
			return err
		}

		result, err = a.consumptionService.GetActiveCycleInfo(ctx, msg.ChatID, goods, batchNumber)
		if err != nil {
			// Jika tidak ada spesifik item, list semua active items
			result, err = a.consumptionService.ListActiveItems(ctx, msg.ChatID)
			if err != nil {
				return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal mengambil list active items: %v", err), intentCost)
			}
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)
		}
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, result, intentCost)
	}
}

// handleUsageWithConsumption menangani "pakai" action: kurangi stok + mulai consumption cycle.
func (a *consumptionAgent) handleUsageWithConsumption(ctx context.Context, msg entity.IncomingMessage, itemName string, usageQty float64, usageUnit string, conversionFactor float64, usageDate string, intentCost float64) error {
	// Resolve nama barang ke inventory: exact → ILIKE → saring via pesan asli.
	// Classifier kadang melepas ukuran dari nama ("pakai susu bmt 200g" →
	// item_name "susu bmt") padahal inventory menyimpan "susu bmt 200g".
	inv, err := agent.ResolveInventoryItem(ctx, a.db, a.goodsRepo, a.invRepo, msg.ChatID, msg.Text, itemName)
	if err != nil {
		var amb *agent.AmbiguousInventoryError
		switch {
		case errors.Is(err, agent.ErrInventoryNotFound):
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Barang '%s' belum ada di inventaris. Beli dulu ya!", itemName), intentCost)
		case errors.As(err, &amb):
			return a.confirmItemChoice(ctx, msg, map[string]interface{}{"consumption_action": "use", "item_name": itemName}, amb, intentCost)
		default:
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal cek inventaris: %v", err), intentCost)
		}
	}
	// Gunakan nama resmi barang (dari relasi goods) untuk semua operasi
	// berikutnya, dan relasi goods untuk persistensi cycle.
	itemName = inv.Name()

	// Konversi jumlah pakai ke satuan inventory dengan prioritas: isi
	// tersimpan (jawaban user sebelumnya) → isi di nama barang → pola
	// "<kemasan> <isi>" di pesan. Isi yang baru diketahui disimpan agar
	// pemakaian berikutnya stabil; bila tak bisa dikonversi dan isinya
	// belum diketahui, tanya user dulu (jawaban bebas, mis. "15lt").
	convQty, convUnit, learnedQty, learnedUnit, ok := ResolveUsageConversion(inv, usageQty, usageUnit, msg.Text)
	if ok {
		usageQty, usageUnit = convQty, convUnit
		if learnedQty > 0 {
			// Promosikan faktor yang baru diketahui ke master goods agar
			// pemakaian berikutnya di chat ini stabil.
			if err := a.goodsRepo.WithTx(a.db).UpdateConversion(ctx, inv.GoodsID, learnedUnit, learnedQty); err != nil {
				a.log.ErrorContext(ctx, "gagal simpan faktor konversi", "err", err)
			}
		}
	} else if ConversionQuestion(inv, usageUnit) != "" {
		// Jalur ambigu: kode tidak tahu faktornya → LLM penalar menentukan
		// (faktor eksplisit di pesan? pertanyaan natural? tidak relevan?),
		// dengan fallback deterministik bila LLM gagal.
		stop, newQty, newUnit, extraCost, rerr := a.resolveAmbiguousConversion(ctx, msg, inv, itemName, usageQty, usageUnit, usageDate, intentCost)
		intentCost += extraCost
		if stop {
			return rerr
		}
		usageQty, usageUnit = newQty, newUnit
	}

	// Validasi stok cukup
	if inv.StockQty < usageQty {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Stok %s tidak cukup! Sisa: %.1f %s", itemName, inv.StockQty, inv.Unit), intentCost)
	}

	// Jalankan dalam transaction: kurangi stok + mulai/updates consumption cycle
	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Kurangi stok dalam unit inventory (pcs)
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

		// Mulai consumption cycle: qty dalam SATUAN INVENTORY (pcs hasil
		// konversi); StartUsage menurukan satuan dasar (gr/ml) dari nama barang.
		cycle, err := a.consumptionService.StartUsage(ctx, msg.ChatID, inv.Good, usageQty, usageUnit, conversionFactor, usageDate)
		if err != nil {
			return err
		}

		// Simpan batch number untuk reply message
		a.log.DebugContext(ctx, "consumption cycle created/updated", "item", itemName, "batch", cycle.BatchNumber)
		return nil
	})

	if err != nil {
		a.log.ErrorContext(ctx, "gagal handle usage dengan consumption", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Gagal mulai pemakaian: %v", err), intentCost)
	}

	// Invalidate cache
	a.invCache.Delete(msg.ChatID)

	// Get updated stock dan active cycle untuk batch info
	updatedInv, err := a.invRepo.WithTx(a.db).GetByChatGoods(ctx, msg.ChatID, inv.GoodsID)
	if err != nil {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("🔄 Pemakaian %s %.1f %s dicatat. Consumption cycle dimulai dengan auto-generated batch!", itemName, usageQty, usageUnit), intentCost)
	}

	// Get cycle info untuk menampilkan batch number
	cycle, err := a.consumptionService.cycleRepo.GetActiveByGoods(ctx, msg.ChatID, inv.GoodsID)
	if err != nil {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf(
			"🔄 Pemakaian %s %.1f %s dicatat.\n✅ Consumption cycle: OPEN\n📦 Sisa stok: %.1f %s",
			itemName, usageQty, usageUnit, updatedInv.StockQty, updatedInv.Unit,
		), intentCost)
	}

	batchInfo := ""
	if cycle.BatchNumber != "" {
		batchInfo = fmt.Sprintf(" (%s)", cycle.BatchNumber)
	}

	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf(
		"🔄 Pemakaian %s%s %.1f %s dicatat.\n✅ Consumption cycle: OPEN\n📦 Sisa stok: %.1f %s",
		itemName, batchInfo, usageQty, usageUnit, updatedInv.StockQty, updatedInv.Unit,
	), intentCost)
}

// applyConversionAnswer memproses jawaban user atas pertanyaan faktor
// konversi (mis. "15lt"): parse angka+satuan (regex dulu, lalu penalar LLM
// untuk jawaban bebas), simpan sebagai isi per kemasan di inventory.
// Return stop=true bila reply sudah dikirim (format tidak dikenali /
// item tidak ketemu) sehingga aksi use dihentikan.
func (a *consumptionAgent) applyConversionAnswer(ctx context.Context, msg entity.IncomingMessage, itemName, answer string, intentCost float64) (bool, error) {
	inv, err := agent.ResolveInventoryItem(ctx, a.db, a.goodsRepo, a.invRepo, msg.ChatID, msg.Text, itemName)
	if err != nil {
		return true, agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			fmt.Sprintf("Barang '%s' tidak ditemukan di inventaris.", itemName), intentCost)
	}

	qty, unit := extractSizeFromItemName(answer)
	if qty <= 0 && a.reasoner != nil {
		input := fmt.Sprintf("Pesan user: %q\nBarang: %q (stok dalam satuan: %s)\nPemakaian: dalam satuan lain, menunggu faktor konversi",
			answer, inv.Name(), inv.Unit)
		reasoning, usage, rerr := a.reasoner.ReasonConversion(ctx, conversionReasonPrompt, input, msg.ChatID)
		if rerr != nil {
			a.log.ErrorContext(ctx, "gagal reason jawaban konversi", "err", rerr)
		} else if reasoning.Action == "convert" {
			qty, unit = reasoning.ContentQty, reasoning.ContentUnit
		}
		_ = usage
	}
	if qty <= 0 || unit == "" {
		return true, agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			"Format belum jelas. Ketik angka + satuan, contoh: 15lt atau 48pcs", intentCost)
	}
	if err := a.goodsRepo.WithTx(a.db).UpdateConversion(ctx, inv.GoodsID, unit, qty); err != nil {
		a.log.ErrorContext(ctx, "gagal simpan faktor konversi", "err", err)
	}
	a.log.InfoContext(ctx, "faktor konversi dipelajari", "item", inv.Name(), "content", qty, "unit", unit)
	return false, nil
}

// resolveAmbiguousConversion menangani pemakaian yang tak bisa dikonversi
// secara deterministik. Penalar LLM memutuskan: (a) faktor eksplisit ada
// di pesan → konversi & simpan; (b) perlu klarifikasi → kirim pertanyaan
// natural + daftarkan pending; (c) tidak relevan → biarkan validasi stok
// yang menolak. Fallback ke pertanyaan template bila LLM gagal.
// Return: stop=true bila reply terkirim; extraCost = biaya LLM reasoning.
func (a *consumptionAgent) resolveAmbiguousConversion(
	ctx context.Context, msg entity.IncomingMessage, inv *domain.Inventory,
	itemName string, usageQty float64, usageUnit, usageDate string, intentCost float64,
) (stop bool, newQty float64, newUnit string, extraCost float64, err error) {
	question := ConversionQuestion(inv, usageUnit)

	if a.reasoner != nil {
		input := fmt.Sprintf("Pesan user: %q\nBarang: %q (stok dalam satuan: %s)\nPemakaian: %g %s",
			msg.Text, inv.Name(), inv.Unit, usageQty, usageUnit)
		reasoning, usage, rerr := a.reasoner.ReasonConversion(ctx, conversionReasonPrompt, input, msg.ChatID)
		if rerr != nil {
			a.log.ErrorContext(ctx, "gagal reason konversi, fallback template", "err", rerr)
		} else {
			extraCost = usage.CostUSD
			switch reasoning.Action {
			case "convert":
				if q, u, okC := convertByContent(inv, reasoning.ContentQty, reasoning.ContentUnit, usageQty, usageUnit); okC {
					if uerr := a.goodsRepo.WithTx(a.db).UpdateConversion(ctx, inv.GoodsID, reasoning.ContentUnit, reasoning.ContentQty); uerr != nil {
						a.log.ErrorContext(ctx, "gagal simpan faktor konversi", "err", uerr)
					}
					a.log.InfoContext(ctx, "faktor konversi dari penalaran LLM",
						"item", inv.Name(), "content", reasoning.ContentQty, "unit", reasoning.ContentUnit)
					return false, q, u, extraCost, nil
				}
			case "ask":
				if reasoning.Question != "" {
					question = reasoning.Question
				}
			case "reject":
				return false, usageQty, usageUnit, extraCost, nil
			}
		}
	}

	if a.pending != nil {
		a.pending.Set(msg.ChatID, agent.PendingChoice{
			Action: domain.ActionConsumption,
			Params: map[string]interface{}{
				"consumption_action": "use",
				"item_name":          itemName,
				"usage_qty":          usageQty,
				"usage_unit":         usageUnit,
				"usage_date":         usageDate,
			},
			FreeTextKey:  "conversion_answer",
			OriginalText: msg.Text,
		})
	}
	return true, usageQty, usageUnit, extraCost,
		agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, question, intentCost+extraCost)
}

// confirmBatchIfNeeded menangani kasus item punya LEBIH DARI SATU batch aktif
// tapi user tidak menyebut batch: daftar batchnya dan minta konfirmasi
// alih-alih menebak cycle terbaru. Return (true, err) bila permintaan
// konfirmasi sudah terkirim — pemrosesan aksi harus berhenti.
func (a *consumptionAgent) confirmBatchIfNeeded(ctx context.Context, msg entity.IncomingMessage, actionType string, params map[string]interface{}, goods *domain.Good, batchNumber string, intentCost float64) (bool, error) {
	if batchNumber != "" || goods == nil {
		return false, nil
	}

	itemName := goods.Name
	cycles, err := a.consumptionService.cycleRepo.ListActiveByGoods(ctx, msg.ChatID, goods.ID)
	if err != nil || len(cycles) <= 1 {
		// 0 atau 1 batch: lanjut sebagaimana mestinya — service akan membalas
		// "tidak ada siklus aktif" bila memang kosong.
		return false, nil
	}

	// Daftarkan konfirmasi: pesan berikutnya bisa "1"/"2" atau batch lengkap.
	// Params pesan asli (usage_date, dll.) ikut dibawa supaya aksi lanjut
	// seolah-olah user menyebut batch sejak awal.
	options := make([]string, len(cycles))
	for i, c := range cycles {
		options[i] = c.BatchNumber
	}
	pendingParams := map[string]interface{}{
		"consumption_action": actionType,
		"item_name":          itemName,
	}
	for _, k := range []string{"usage_date", "usage_qty", "usage_unit", "limit"} {
		if v, ok := params[k]; ok {
			pendingParams[k] = v
		}
	}
	if a.pending != nil {
		a.pending.Set(msg.ChatID, agent.PendingChoice{
			Action:       domain.ActionConsumption,
			Params:       pendingParams,
			OptionKey:    "batch_number",
			Options:      options,
			OriginalText: msg.Text,
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "⚠️ \"%s\" punya %d batch aktif — pilih nomornya ya:\n", itemName, len(cycles))
	for i, c := range cycles {
		fmt.Fprintf(&b, "%d. (%s) mulai %s, %g %s\n", i+1, c.BatchNumber, c.StartDate.Format("02/01"), c.PurchaseQty, c.PurchaseUnit)
	}
	fmt.Fprintf(&b, "\nBalas nomornya (1-%d), atau sebut batch lengkap.", len(cycles))
	return true, agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, b.String(), intentCost)
}

// confirmItemChoice menangani kasus nama barang ambigu (mis. "susu" cocok
// dengan susu uht 500ml & susu bmt 200g): daftar kandidat bernomor dan
// daftarkan konfirmasi — jawaban "1"/"2" di-resume tanpa LLM hop dengan
// params asli + item terpilih.
func (a *consumptionAgent) confirmItemChoice(ctx context.Context, msg entity.IncomingMessage, params map[string]interface{}, amb *agent.AmbiguousInventoryError, intentCost float64) error {
	options := agent.ItemOptionNames(amb)

	// Params pesan asli dibawa; item_name akan ditimpa pilihan user.
	pendingParams := map[string]interface{}{}
	for k, v := range params {
		pendingParams[k] = v
	}
	delete(pendingParams, "batch_number")
	if a.pending != nil {
		a.pending.Set(msg.ChatID, agent.PendingChoice{
			Action:       domain.ActionConsumption,
			Params:       pendingParams,
			OptionKey:    "item_name",
			Options:      options,
			OriginalText: msg.Text,
		})
	}

	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.FormatItemChoice(msg.Text, amb), intentCost)
}
