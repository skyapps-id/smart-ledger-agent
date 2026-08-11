// Package service berisi orkestrasi business logic: ekstraksi LLM,
// persistensi transaksional ke DB, dan pengiriman balasan WhatsApp.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/waha"
)

// Agent adalah orchestrator utama (RFC §4.1 langkah 4-6).
type Agent struct {
	db       *gorm.DB
	chatRepo repository.ChatRepository
	txnRepo  repository.TransactionRepository
	invRepo  repository.InventoryRepository
	logRepo  repository.StockLogRepository
	llm      llm.Extractor
	waha     waha.Sender
	log      *slog.Logger
}

// NewAgent membuat agent baru dengan dependency injection.
func NewAgent(
	db *gorm.DB,
	chatRepo repository.ChatRepository,
	txnRepo repository.TransactionRepository,
	invRepo repository.InventoryRepository,
	logRepo repository.StockLogRepository,
	extractor llm.Extractor,
	sender waha.Sender,
	logger *slog.Logger,
) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	return &Agent{
		db:       db,
		chatRepo: chatRepo,
		txnRepo:  txnRepo,
		invRepo:  invRepo,
		logRepo:  logRepo,
		llm:      extractor,
		waha:     sender,
		log:      logger,
	}
}

// Process menjalankan pipeline penuh untuk satu pesan masuk.
// Setiap jalur mengirim balasan ke pengguna via WAHA.
func (a *Agent) Process(ctx context.Context, msg entity.IncomingMessage) error {
	a.log.InfoContext(ctx, "memproses pesan", "chat", msg.ChatID, "sender", msg.UserPhone, "text", msg.Text)

	chat, err := a.chatRepo.GetOrCreate(ctx, msg.ChatID)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal get/create chat", "err", err)
		return a.reply(ctx, msg.ChatID, "Maaf, terjadi kendala. Coba lagi nanti.")
	}

	// 1. Init eksplisit: aktifkan ledger chat (idempoten). Bila disertai nama,
	// nama ledger di-set (mendukung pemberian nama saat init pertama maupun
	// rename melalui re-init: `init <nama baru>`).
	if ok, name := parseInitCommand(msg.Text); ok {
		if !chat.Initialized {
			if err := a.chatRepo.MarkInitialized(ctx, msg.ChatID, name); err != nil {
				a.log.ErrorContext(ctx, "gagal mark init", "err", err)
			}
			a.log.InfoContext(ctx, "chat melakukan init", "chat", msg.ChatID, "name", name)
			return a.reply(ctx, msg.ChatID, initReply(name))
		}
		// Sudah init: update nama bila diberikan, kalau tidak cukup balas status.
		if name != "" {
			if err := a.chatRepo.MarkInitialized(ctx, msg.ChatID, name); err != nil {
				a.log.ErrorContext(ctx, "gagal rename ledger", "err", err)
			}
			a.log.InfoContext(ctx, "ledger di-rename", "chat", msg.ChatID, "name", name)
			return a.reply(ctx, msg.ChatID, fmt.Sprintf("Nama ledger diperbarui: %s", name))
		}
		return a.reply(ctx, msg.ChatID, "Akun sudah aktif. Ketik \"bantuan\" untuk format.")
	}

	// 1b. Command info (diagnostic): selalu tersedia, bahkan pre-init.
	if isInfoCommand(msg.Text) {
		return a.handleInfo(ctx, msg, chat)
	}

	// 2. Pre-init gate: semua pesan lain ditolak sampai chat di-init.
	if !chat.Initialized {
		return a.reply(ctx, msg.ChatID, PreInitMessage)
	}

	// 3. Bantuan format (post-init).
	if isHelpCommand(msg.Text) {
		return a.reply(ctx, msg.ChatID, OnboardingTemplate)
	}

	// 4. Path laporan: bila pesan adalah pertanyaan, baca DB & format jawaban.
	if isReportQuery(msg.Text) {
		return a.handleReport(ctx, msg)
	}

	// 5. Path pencatatan: ekstraksi LLM -> persist.
	ext, err := a.llm.Extract(ctx, msg.Text)
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
	txn := &domain.Transaction{
		ChatID:      msg.ChatID,
		SenderPhone: msg.UserPhone,
		Type:        domain.TransactionIncome,
		Category:    ext.Category,
		ItemName:    ext.ItemName,
		Amount:      ext.Amount,
		RawPayload:  msg.Text,
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
		txn := &domain.Transaction{
			ChatID:      msg.ChatID,
			SenderPhone: msg.UserPhone,
			Type:        domain.TransactionExpense,
			Category:    ext.Category,
			ItemName:    ext.ItemName,
			Amount:      ext.Amount,
			RawPayload:  msg.Text,
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
		return fmt.Sprintf(
			"Pengeluaran tercatat: %s x%g %s = Rp%s (%s). Stok saat ini: %g %s.",
			ext.ItemName, ext.Quantity, ext.Unit,
			formatRupiah(ext.Amount), ext.Category,
			inv.StockQty, inv.Unit,
		), nil
	}
	return fmt.Sprintf(
		"Pengeluaran tercatat: %s sebesar Rp%s (%s).",
		ext.ItemName, formatRupiah(ext.Amount), ext.Category,
	), nil
}

// handleConsumption: kurangi stok + log OUT (RFC §7.2). Tanpa record uang.
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
		if err := a.invRepo.WithTx(tx).DecreaseStock(ctx, inv.ID, ext.Quantity); err != nil {
			return err
		}
		log := &domain.StockLog{
			InventoryID: inv.ID,
			ChangeType:  domain.StockOut,
			Quantity:    ext.Quantity,
			Notes:       ext.Notes,
		}
		return a.logRepo.WithTx(tx).Create(ctx, log)
	})
	if err != nil {
		if errors.Is(err, repository.ErrInsufficientStock) {
			return "", &businessError{
				msg: fmt.Sprintf("Stok %s habis/tidak cukup saat pemakaian.", ext.ItemName),
			}
		}
		return "", fmt.Errorf("kurangi stok: %w", err)
	}

	// Sisa stok diperkirakan (best-effort untuk pesan).
	remaining := inv.StockQty - ext.Quantity
	return fmt.Sprintf(
		"Pemakaian tercatat: %s -%g %s. Sisa stok: %g %s.",
		ext.ItemName, ext.Quantity, ext.Unit, remaining, inv.Unit,
	), nil
}

// initReply memilih template konfirmasi init sesuai ada/tidak-nya nama ledger.
func initReply(name string) string {
	if name == "" {
		return InitSuccessMessage
	}
	return fmt.Sprintf(InitSuccessNamedMessage, name)
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

// reply membungkus pengiriman WAHA dengan logging.
func (a *Agent) reply(ctx context.Context, chatID, text string) error {
	if err := a.waha.SendText(ctx, chatID, text); err != nil {
		a.log.ErrorContext(ctx, "gagal mengirim balasan WAHA", "err", err)
		return err
	}
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
