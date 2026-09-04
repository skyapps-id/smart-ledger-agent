package transaction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/service/agent"
	"smart-ledger-agent/internal/service/consumption"
)

// transactionAgent menangani pencatatan transaksi (beli/jual/pakai):
// ekstraksi entitas via LLM lalu persistensi transaksional ke DB.
type transactionAgent struct {
	db                 *gorm.DB
	txnRepo            repository.TransactionRepository
	goodsRepo          repository.GoodsRepository
	invRepo            repository.InventoryRepository
	logRepo            repository.StockLogRepository
	consumptionService *consumption.Service
	llm                llm.Extractor
	// extractionPrompt adalah system prompt milik transactionAgent untuk
	// ekstraksi entitas transaksi (lihat prompt.go).
	extractionPrompt string
	invCache         *cache.Cache
	// pending menyimpan konfirmasi pilihan barang menunggu jawaban user.
	pending *agent.PendingConfirms
	sender  agent.MessageSender
	log     *slog.Logger
}

func NewAgent(
	db *gorm.DB,
	txnRepo repository.TransactionRepository,
	goodsRepo repository.GoodsRepository,
	invRepo repository.InventoryRepository,
	logRepo repository.StockLogRepository,
	consumptionService *consumption.Service,
	extractor llm.Extractor,
	invCache *cache.Cache,
	pending *agent.PendingConfirms,
	sender agent.MessageSender,
	logger *slog.Logger,
) agent.SubAgent {
	return &transactionAgent{
		db:                 db,
		txnRepo:            txnRepo,
		goodsRepo:          goodsRepo,
		invRepo:            invRepo,
		logRepo:            logRepo,
		consumptionService: consumptionService,
		llm:                extractor,
		extractionPrompt:   transactionSystemPrompt,
		invCache:           invCache,
		pending:            pending,
		sender:             sender,
		log:                logger,
	}
}

func (a *transactionAgent) Actions() []string {
	return []string{domain.ActionRecordTransaction}
}

// SystemPrompt mengembalikan prompt ekstraksi milik agent ini.
func (a *transactionAgent) SystemPrompt() string { return a.extractionPrompt }

func (a *transactionAgent) Handle(ctx context.Context, req agent.Request) error {
	// item_name di params hanya terisi bila ini resume konfirmasi pilihan
	// barang ("ambil susu" → "1") — dipakai sebagai item terpaksa.
	forcedItem, _ := req.Action.Params["item_name"].(string)
	return a.handleRecordTransaction(ctx, req.Message, req.Chat, req.IntentCost, forcedItem)
}

// handleRecordTransaction menangani action record_transaction (pencatatan transaksi).
func (a *transactionAgent) handleRecordTransaction(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, intentCost float64, forcedItem string) error {
	a.log.InfoContext(ctx, "handler: record_transaction")
	if !chat.Initialized {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.PreInitMessage, intentCost)
	}

	// Path pencatatan: ekstraksi LLM -> persist.
	// Sertakan snapshot inventory (pakai search optimization) agar LLM meresolve nama barang
	// ke item yang sudah ada di inventory chat ini.
	items := a.searchInventory(ctx, msg.ChatID, msg.Text)
	invContext := llm.BuildInventoryPrompt(items)

	// Hop LLM ekstraksi: catat ke task (biaya + durasi) untuk ringkasan orchestrator.
	// TimeContext memberi tahu LLM tanggal hari ini agar kata relatif
	// ("kemarin", "besok") dan tahun berjalan tidak dihalusinasi.
	t0 := time.Now()
	ext, usage, err := a.llm.Extract(ctx, a.extractionPrompt+llm.TimeContext(time.Now()), msg.Text, invContext, msg.ChatID)
	agent.TaskFromContext(ctx).AddStep("transaction", "llm.extract", string(ext.Type), usage.CostUSD, err, time.Since(t0))
	if err != nil {
		a.log.ErrorContext(ctx, "gagal ekstraksi LLM", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.LLMErrorMessage(err), intentCost)
	}

	// Pesan non-transaksi (sapaan/chitchat): jangan dicatat, balas ramah.
	if ext.Type == domain.ExtractionNone {
		a.log.InfoContext(ctx, "pesan non-transaksi diabaikan", "text", msg.Text)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.SmallTalkMessage, intentCost+usage.CostUSD)
	}

	t1 := time.Now()
	reply, err := a.persist(ctx, msg, ext, forcedItem)
	agent.TaskFromContext(ctx).AddStep("transaction", "persist", string(ext.Type), 0, err, time.Since(t1))
	if err != nil {
		var be *agent.BusinessError
		if errors.As(err, &be) {
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, be.Error(), intentCost+usage.CostUSD)
		}
		a.log.ErrorContext(ctx, "gagal persistensi", "err", err, "type", ext.Type)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, terjadi kendala saat mencatat. Coba lagi nanti.", intentCost+usage.CostUSD)
	}
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, reply, intentCost+usage.CostUSD)
}

// persist menjalankan persistensi sesuai tipe transaksi dalam DB transaction.
func (a *transactionAgent) persist(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction, forcedItem string) (string, error) {
	switch ext.Type {
	case domain.ExtractionIncome:
		return a.handleIncome(ctx, msg, ext)
	case domain.ExtractionExpense:
		return a.handleExpense(ctx, msg, ext)
	case domain.ExtractionConsumption:
		return a.handleConsumption(ctx, msg, ext, forcedItem)
	default:
		return "", fmt.Errorf("tipe transaksi tidak dikenal: %s", ext.Type)
	}
}

// handleIncome: catat transaksi pemasukan saja (RFC §5.1).
func (a *transactionAgent) handleIncome(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction) (string, error) {
	txnDate, err := parseTransactionDate(ext.TransactionDate)
	if err != nil {
		return "", fmt.Errorf("format tanggal tidak valid: %w", err)
	}

	// Resolve nama barang ke master goods (auto-create bila baru) —
	// relasi transaksi via goods_id, nama disimpan sebagai snapshot display.
	goods, err := a.goodsRepo.WithTx(a.db).GetOrCreateByName(ctx, ext.ItemName, ext.Unit)
	if err != nil {
		return "", fmt.Errorf("resolve goods: %w", err)
	}

	txn := &domain.Transaction{
		ChatID:          msg.ChatID,
		SenderPhone:     msg.UserPhone,
		Type:            domain.TransactionIncome,
		Category:        ext.Category,
		GoodsID:         goods.ID,
		ItemName:        goods.Name,
		Amount:          ext.Amount,
		RawPayload:      msg.Text,
		TransactionDate: txnDate,
	}
	if err := a.txnRepo.WithTx(a.db).Create(ctx, txn); err != nil {
		return "", fmt.Errorf("catat income: %w", err)
	}
	return fmt.Sprintf(
		"Pemasukan tercatat: %s sebesar Rp%s (%s).",
		ext.ItemName, agent.FormatRupiah(ext.Amount), ext.Category,
	), nil
}

// handleExpense: catat pengeluaran. Hanya tambah stok bila affects_stock=true (RFC §7.1).
func (a *transactionAgent) handleExpense(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction) (string, error) {
	var inv *domain.Inventory
	var lastPurchase *domain.Transaction
	var txnDate time.Time
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Resolve nama barang ke master goods (auto-create bila baru):
		// seluruh relasi berikutnya (transaksi, inventory) via goods_id.
		goods, err := a.goodsRepo.WithTx(tx).GetOrCreateByName(ctx, ext.ItemName, ext.Unit)
		if err != nil {
			return fmt.Errorf("resolve goods: %w", err)
		}

		// Skip financial transaction creation if amount is 0 but affects stock (inventory-only update)
		if ext.Amount == 0 && ext.AffectsStock {
			upserted, err := a.invRepo.WithTx(tx).AddStock(ctx, msg.ChatID, goods.ID, ext.Quantity, ext.Unit)
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

		parsedDate, err := parseTransactionDate(ext.TransactionDate)
		if err != nil {
			return fmt.Errorf("format tanggal tidak valid: %w", err)
		}
		txnDate = parsedDate

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
			GoodsID:         goods.ID,
			ItemName:        goods.Name,
			Amount:          ext.Amount,
			RawPayload:      msg.Text,
			TransactionDate: txnDate,
			ConsumptionDate: consumptionDate,
			TotalConsumed:   ext.TotalConsumption,
		}
		if err := a.txnRepo.WithTx(tx).Create(ctx, txn); err != nil {
			return fmt.Errorf("catat expense: %w", err)
		}

		// Ambil pembelian terakhir barang yang sama (relasi goods) untuk
		// analisa beli ulang (stok maupun non-stok: umur 1 ball pampers /
		// 1 token listrik, dll).
		if ext.Amount > 0 {
			lastPurchase, _ = a.txnRepo.WithTx(tx).LastExpenseByGoods(ctx, msg.ChatID, goods.ID, txn.ID, txnDate)
		}

		// Lewati inventaris bila pengeluaran bukan barang stok (jasa/utilitas/dll).
		if !ext.AffectsStock {
			return nil
		}

		upserted, err := a.invRepo.WithTx(tx).AddStock(ctx, msg.ChatID, goods.ID, ext.Quantity, ext.Unit)
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
			agent.FormatRupiah(ext.Amount), ext.Category,
			inv.StockQty, inv.Unit,
		)

		if consumptionAnalysis != "" {
			baseReply += consumptionAnalysis
		}

		if analysis := repurchaseAnalysis(txnDate, lastPurchase); analysis != "" {
			baseReply += analysis
		}

		return baseReply, nil
	}

	baseReply := fmt.Sprintf(
		"Pengeluaran tercatat: %s sebesar Rp%s (%s).",
		ext.ItemName, agent.FormatRupiah(ext.Amount), ext.Category,
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

	if analysis := repurchaseAnalysis(txnDate, lastPurchase); analysis != "" {
		baseReply += analysis
	}

	return baseReply, nil
}

// handleConsumption: kurangi stok + log OUT + update consumption cycle (RFC §7.2).
func (a *transactionAgent) handleConsumption(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction, forcedItem string) (string, error) {
	// Resume konfirmasi pilihan barang: user sudah memilih ("1"), pakai item
	// itu langsung (ekstraksi LLM ulang tetap jalan untuk qty/amount).
	if forcedItem != "" {
		ext.ItemName = forcedItem
	}

	// Resolve nama barang ke inventory (via relasi goods): exact → ILIKE →
	// saring via pesan asli, agar "ambil susu bmt 200g" tetap ketemu walau
	// ekstraksi LLM melepas ukuran.
	inv, err := agent.ResolveInventoryItem(ctx, a.db, a.goodsRepo, a.invRepo, msg.ChatID, msg.Text, ext.ItemName)
	if err != nil {
		var amb *agent.AmbiguousInventoryError
		switch {
		case errors.Is(err, agent.ErrInventoryNotFound):
			return "", agent.NewBusinessError(fmt.Sprintf("Barang '%s' belum tercatat di inventaris.", ext.ItemName))
		case errors.As(err, &amb):
			// Beberapa kandidat ("susu" → uht/bmt): daftarkan konfirmasi
			// bernomor; jawaban "1"/"2" resume transaksi ini tanpa LLM intent.
			if a.pending != nil {
				a.pending.Set(msg.ChatID, agent.PendingChoice{
					Action:       domain.ActionRecordTransaction,
					OptionKey:    "item_name",
					Options:      agent.ItemOptionNames(amb),
					OriginalText: msg.Text,
				})
			}
			return agent.FormatItemChoice(msg.Text, amb), nil
		default:
			return "", fmt.Errorf("cari inventaris: %w", err)
		}
	}
	// Gunakan nama resmi barang (relasi goods) untuk operasi berikutnya.
	ext.ItemName = inv.Name()

	// Konversi jumlah pakai ke satuan inventory dengan prioritas: isi
	// tersimpan → isi di nama barang → pola "<kemasan> <isi>" di pesan.
	// Bila tak bisa dikonversi dan isinya belum diketahui, tanya user dulu
	// (jawaban bebas "15lt" di-resume ke consumption agent).
	convQty, convUnit, learnedQty, learnedUnit, ok := consumption.ResolveUsageConversion(inv, ext.Quantity, ext.Unit, msg.Text)
	if ok {
		ext.Quantity, ext.Unit = convQty, convUnit
		if learnedQty > 0 {
			// Promosikan faktor yang baru diketahui ke master goods.
			if err := a.goodsRepo.WithTx(a.db).UpdateConversion(ctx, inv.GoodsID, learnedUnit, learnedQty); err != nil {
				a.log.ErrorContext(ctx, "gagal simpan faktor konversi", "err", err)
			}
		}
	} else if q := consumption.ConversionQuestion(inv, ext.Unit); q != "" {
		if a.pending != nil {
			a.pending.Set(msg.ChatID, agent.PendingChoice{
				Action: domain.ActionConsumption,
				Params: map[string]interface{}{
					"consumption_action": "use",
					"item_name":          ext.ItemName,
					"usage_qty":          ext.Quantity,
					"usage_unit":         ext.Unit,
					"usage_date":         ext.TransactionDate,
				},
				FreeTextKey:  "conversion_answer",
				OriginalText: msg.Text,
			})
		}
		return q, nil
	}

	// Extract satuan asli dari nama item untuk consumption tracking
	// Contoh: "susu uht 500ml" → satuan asli: "500ml"
	originalUnit := consumption.ExtractOriginalUnitFromItemName(ext.ItemName)

	// Jika user menyebutkan satuan spesifik dalam consumption, gunakan itu sebagai satuan asli
	if ext.Unit != "pcs" && ext.Unit != "" {
		originalUnit = ext.Unit
	}

	// Konversi quantity ke satuan asli untuk consumption tracking
	// Contoh: inventory 1 pcs (500ml), user pakai 1 pcs → consumption: 500ml
	quantityInOriginalUnit := ext.Quantity
	if originalUnit != "" && ext.Unit == "pcs" {
		// User sebut "pakai susu uht 500ml" (1 pcs) → extract qty dari nama item
		if extractedQty := consumption.ExtractQuantityFromItemName(ext.ItemName); extractedQty > 0 {
			quantityInOriginalUnit = extractedQty
		}
	}

	// Validasi stok cukup (pesan informatif). Pengurangan tetap atomik di tx.
	if inv.StockQty < ext.Quantity {
		return "", agent.NewBusinessError(fmt.Sprintf(
			"Stok %s tidak cukup (sisa %g %s).",
			ext.ItemName, inv.StockQty, inv.Unit,
		))
	}

	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Kurangi stok dalam unit inventory (pcs)
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

		// Start/update consumption cycle: kirim qty dalam SATUAN INVENTORY
		// (hasil konversi) — StartUsage menghitung sendiri faktor gr/ml-nya
		// dari ukuran di nama barang. Relasi cycle via goods.
		conversionFactor := 1.0 // fallback bila nama barang tanpa ukuran
		_, err := a.consumptionService.StartUsage(ctx, msg.ChatID, inv.Good, ext.Quantity, ext.Unit, conversionFactor, ext.TransactionDate)
		return err
	})
	if err != nil {
		if errors.Is(err, repository.ErrInsufficientStock) {
			return "", agent.NewBusinessError(fmt.Sprintf("Stok %s habis/tidak cukup saat pemakaian.", ext.ItemName))
		}
		return "", fmt.Errorf("kurangi stok: %w", err)
	}

	a.invCache.Delete(msg.ChatID) // invalidate cache karena stok berkurang

	// Fetch updated inventory after transaction for accurate remaining stock
	updatedInv, err := a.invRepo.WithTx(a.db).GetByChatGoods(ctx, msg.ChatID, inv.GoodsID)
	if err != nil {
		// Fallback to calculation if fetch fails
		remaining := inv.StockQty - ext.Quantity
		return fmt.Sprintf(
			"🔄 Pemakaian tercatat: %s -%g %s (%g %s dari stok). Sisa stok: %g %s.\n✅ Consumption cycle: ACTIVE",
			ext.ItemName, ext.Quantity, ext.Unit, quantityInOriginalUnit, originalUnit, remaining, inv.Unit,
		), nil
	}

	return fmt.Sprintf(
		"🔄 Pemakaian tercatat: %s -%g %s (%g %s dari stok). Sisa stok: %g %s.\n✅ Consumption cycle: ACTIVE",
		ext.ItemName, ext.Quantity, ext.Unit, quantityInOriginalUnit, originalUnit, updatedInv.StockQty, updatedInv.Unit,
	), nil
}

// cachedInventory mengembalikan snapshot inventory chat dari cache (TTL 5m)
// atau dari DB bila cache miss. Dipakai sebagai konteks LLM agar LLM dapat
// meresolve nama barang (mis. "susu" → "susu uht" di inventory).
func (a *transactionAgent) cachedInventory(ctx context.Context, chatID string) []domain.Inventory {
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

// extractKeywords mengambil keywords dari pesan user untuk inventory search.
// Focus pada kata-kata yang kemungkinan adalah nama barang/produk.
func (a *transactionAgent) extractKeywords(userMessage string) []string {
	words := strings.Fields(strings.ToLower(userMessage))
	keywords := []string{}

	// Indonesian stopwords + action words yang tidak relevan untuk inventory search
	stopwords := map[string]bool{
		// Stopwords umum
		"yang": true, "dan": true, "atau": true, "ada": true, "dari": true,
		"ke": true, "di": true, "untuk": true, "dengan": true, "pada": true,
		"adalah": true, "itu": true, "ini": true, "berapa": true, "sisa": true,
		// Query words
		"cek": true, "stok": true, "stock": true, "barang": true, "item": true,
		"persediaan": true, "punya": true, "punyai": true, "milik": true,
		// Action words (transactions)
		"beli": true, "bayar": true, "ambil": true, "pakai": true, "terpakai": true,
		"jual": true, "transfer": true, "masuk": true, "keluar": true,
		// Numbers and quantities (biasanya bukan nama barang)
		"rb": true, "ribu": true, "jt": true, "juta": true, "k": true, "pcs": true,
	}

	// Skip common query patterns yang jelas general
	generalPatterns := []string{"barang saya", "apa aja", "apa saja", "semua", "list", "daftar", "inventaris"}
	lowerMsg := strings.ToLower(userMessage)
	for _, pattern := range generalPatterns {
		if strings.Contains(lowerMsg, pattern) {
			return []string{} // Return empty untuk trigger fallback ke full inventory
		}
	}

	for _, word := range words {
		// Skip stopwords, action words, dan kata pendek
		if !stopwords[word] && len(word) > 2 && !strings.HasPrefix(word, "http") {
			// Skip angka murni
			if _, err := strconv.Atoi(word); err != nil {
				keywords = append(keywords, word)
			}
		}
	}

	return keywords
}

// searchInventory mencari inventory items berdasarkan keywords dari pesan user.
// Returns: relevant items (1-5 items) untuk efisiensi LLM tokens.
func (a *transactionAgent) searchInventory(ctx context.Context, chatID, userMessage string) []domain.Inventory {
	keywords := a.extractKeywords(userMessage)

	// Kalau tidak ada keywords extracted atau general query, fallback ke full inventory
	if len(keywords) == 0 {
		a.log.DebugContext(ctx, "general query detected, using full inventory", "chat", chatID)
		return a.cachedInventory(ctx, chatID)
	}

	// Cari dengan keyword pertama (paling relevant)
	keyword := keywords[0]
	items, err := a.invRepo.WithTx(a.db).SearchByName(ctx, chatID, keyword)
	if err != nil {
		a.log.ErrorContext(ctx, "search inventory error, fallback to full", "keyword", keyword, "err", err)
		return a.cachedInventory(ctx, chatID)
	}

	// Kalau search tidak return hasil apa-apa, fallback ke full inventory
	if len(items) == 0 {
		a.log.DebugContext(ctx, "no search results, fallback to full inventory", "keyword", keyword)
		return a.cachedInventory(ctx, chatID)
	}

	a.log.DebugContext(ctx, "search inventory success", "keyword", keyword, "found", len(items), "items", len(items))
	return items
}
