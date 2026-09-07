package transaction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

	// Path pencatatan: ekstraksi LLM -> persist. Tanpa context injection
	// (hemat token): LLM mengekstrak nama barang verbatim; pencocokan ke
	// master goods dilakukan persist via query DB (agent.ResolveGoods).
	// TimeContext memberi tahu LLM tanggal hari ini agar kata relatif
	// ("kemarin", "besok") dan tahun berjalan tidak dihalusinasi.
	t0 := time.Now()
	ext, usage, err := a.llm.Extract(ctx, a.extractionPrompt+llm.TimeContext(time.Now()), msg.Text, msg.ChatID)
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
		return a.handleIncome(ctx, msg, ext, forcedItem)
	case domain.ExtractionExpense:
		return a.handleExpense(ctx, msg, ext, forcedItem)
	case domain.ExtractionConsumption:
		return a.handleConsumption(ctx, msg, ext, forcedItem)
	default:
		return "", fmt.Errorf("tipe transaksi tidak dikenal: %s", ext.Type)
	}
}

// handleIncome: catat transaksi pemasukan saja (RFC §5.1).
func (a *transactionAgent) handleIncome(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction, forcedItem string) (string, error) {
	txnDate, err := parseTransactionDate(ext.TransactionDate)
	if err != nil {
		return "", fmt.Errorf("format tanggal tidak valid: %w", err)
	}
	if forcedItem != "" {
		ext.ItemName = forcedItem
	}

	// Master-first: barang HARUS terdaftar di goods — tidak ada auto-create.
	goods, err := agent.ResolveGoods(ctx, a.db, a.goodsRepo, msg.ChatID, msg.Text, ext.ItemName)
	if err != nil {
		if amb, ok := err.(*agent.AmbiguousGoodsError); ok {
			// Nama mirip beberapa barang: konfirmasi bernomor; jawaban "1"
			// di-resume tanpa LLM intent hop.
			return a.confirmGoodsChoice(ctx, msg, amb)
		}
		return "", goodsRejectError(err, ext.ItemName)
	}
	// Kategori kanonik dari master menang; seed bila belum ada.
	category := resolveCategory(ctx, a.goodsRepo, a.db, goods, ext.Category)

	txn := &domain.Transaction{
		ChatID:          msg.ChatID,
		SenderPhone:     msg.UserPhone,
		Type:            domain.TransactionIncome,
		Category:        category,
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
		goods.Name, agent.FormatRupiah(ext.Amount), category,
	), nil
}

// confirmGoodsChoice menangani nama barang yang mirip beberapa barang
// master: daftar kandidat bernomor dan daftarkan pending — jawaban "1"/"2"
// di-resume orchestrator tanpa LLM hop dengan item terpilih.
func (a *transactionAgent) confirmGoodsChoice(ctx context.Context, msg entity.IncomingMessage, amb *agent.AmbiguousGoodsError) (string, error) {
	if a.pending != nil {
		a.pending.Set(msg.ChatID, agent.PendingChoice{
			Action:       domain.ActionRecordTransaction,
			OptionKey:    "item_name",
			Options:      agent.GoodsOptionNames(amb),
			OriginalText: msg.Text,
		})
	}
	return agent.FormatGoodsChoice(msg.Text, amb), nil
}

// handleExpense: catat pengeluaran. Hanya tambah stok bila affects_stock=true (RFC §7.1).
func (a *transactionAgent) handleExpense(ctx context.Context, msg entity.IncomingMessage, ext domain.Extraction, forcedItem string) (string, error) {
	if forcedItem != "" {
		ext.ItemName = forcedItem
	}

	// Master-first: resolve SEBELUM membuka DB transaction agar konfirmasi
	// pilihan barang bisa dikirim tanpa rollback. Barang HARUS terdaftar —
	// tidak ada auto-create.
	goods, rerr := agent.ResolveGoods(ctx, a.db, a.goodsRepo, msg.ChatID, msg.Text, ext.ItemName)
	if rerr != nil {
		if amb, ok := rerr.(*agent.AmbiguousGoodsError); ok {
			return a.confirmGoodsChoice(ctx, msg, amb)
		}
		return "", goodsRejectError(rerr, ext.ItemName)
	}
	// Kategori kanonik dari master menang; seed bila belum ada.
	category := resolveCategory(ctx, a.goodsRepo, a.db, goods, ext.Category)
	// Satuan stok dari master menang (master-first): ekstraksi LLM hanya
	// fallback bila uom master belum diatur (hindari "pcs" ngaco).
	stockUnit := goods.Uom
	if stockUnit == "" {
		stockUnit = ext.Unit
	}

	var inv *domain.Inventory
	var lastPurchase *domain.Transaction
	var txnDate time.Time
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {

		// Skip financial transaction creation if amount is 0 but affects stock (inventory-only update)
		if ext.Amount == 0 && ext.AffectsStock {
			upserted, err := a.invRepo.WithTx(tx).AddStock(ctx, msg.ChatID, goods.ID, ext.Quantity, stockUnit)
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

		txn := &domain.Transaction{
			ChatID:          msg.ChatID,
			SenderPhone:     msg.UserPhone,
			Type:            domain.TransactionExpense,
			Category:        category,
			GoodsID:         goods.ID,
			ItemName:        goods.Name,
			Amount:          ext.Amount,
			RawPayload:      msg.Text,
			TransactionDate: txnDate,
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

		upserted, err := a.invRepo.WithTx(tx).AddStock(ctx, msg.ChatID, goods.ID, ext.Quantity, stockUnit)
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
		// Jika amount=0 tapi stok terupdate, berarti ini inventory-only update
		if ext.Amount == 0 {
			return fmt.Sprintf(
				"Stok tercatat: %s +%g %s. Stok saat ini: %g %s.",
				goods.Name, ext.Quantity, stockUnit,
				inv.StockQty, inv.Unit,
			), nil
		}

		// Build reply utama
		baseReply := fmt.Sprintf(
			"Pengeluaran tercatat: %s x%g %s = Rp%s (%s). Stok saat ini: %g %s.",
			goods.Name, ext.Quantity, stockUnit,
			agent.FormatRupiah(ext.Amount), category,
			inv.StockQty, inv.Unit,
		)

		if analysis := repurchaseAnalysis(txnDate, lastPurchase); analysis != "" {
			baseReply += analysis
		}

		return baseReply, nil
	}

	baseReply := fmt.Sprintf(
		"Pengeluaran tercatat: %s sebesar Rp%s (%s).",
		ext.ItemName, agent.FormatRupiah(ext.Amount), category,
	)

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

	// Default "pcs" dari LLM (pesan tidak menyebut pcs) = satu satuan stok
	// dari master — normalkan sebelum konversi ("ambil galon" → galon).
	if ext.Unit == "pcs" && inv.Unit != "" && inv.Unit != "pcs" &&
		!strings.Contains(strings.ToLower(msg.Text), "pcs") {
		ext.Unit = inv.Unit
	}

	// Konversi jumlah pakai ke satuan stok HANYA dari faktor master goods.
	// Satuan lain DITOLAK dengan bimbingan — tanpa heuristik nama barang.
	convQty, convUnit, ok := consumption.ConvertUsage(inv, ext.Quantity, ext.Unit)
	if !ok {
		return "", agent.NewBusinessError(fmt.Sprintf(
			"Satuan '%s' tidak dikenali untuk %s. Sebut dalam %s, atau atur faktornya: \"set 1 %s [angka][satuan]\".",
			ext.Unit, ext.ItemName, consumption.UsageUnitHint(inv), ext.ItemName))
	}
	ext.Quantity, ext.Unit = convQty, convUnit

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

	// Fetch updated inventory after transaction for accurate remaining stock
	updatedInv, err := a.invRepo.WithTx(a.db).GetByChatGoods(ctx, msg.ChatID, inv.GoodsID)
	if err != nil {
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

// goodsRejectError menerjemahkan error resolusi goods ke BusinessError yang
// memandu user mendaftarkan barang (kebijakan master-first: no auto-create).
func goodsRejectError(err error, itemName string) error {
	if errors.Is(err, agent.ErrGoodsNotFound) {
		return agent.NewBusinessError(fmt.Sprintf(
			"Barang '%s' tidak ada di master goods. Daftarkan dulu: \"tambah barang %s satuan [satuan]\" (contoh: tambah barang %s satuan pcs)", itemName, itemName, itemName))
	}
	return fmt.Errorf("resolve goods: %w", err)
}
