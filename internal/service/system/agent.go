package system

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/service/agent"
)

// systemAgent menangani aksi non-domain: init ledger, bantuan, info sesi,
// dan pesan tak dikenali (smalltalk).
type systemAgent struct {
	db *gorm.DB
	// systemPrompt adalah prompt skill agent ini (lihat prompt.go);
	// dipakai bila agent diberi LLM call sendiri.
	systemPrompt string
	chatRepo     repository.ChatRepository
	txnRepo      repository.TransactionRepository
	sender       agent.MessageSender
	log          *slog.Logger
}

func NewAgent(
	db *gorm.DB,
	chatRepo repository.ChatRepository,
	txnRepo repository.TransactionRepository,
	sender agent.MessageSender,
	logger *slog.Logger,
) agent.SubAgent {
	return &systemAgent{db: db, systemPrompt: systemAgentPrompt, chatRepo: chatRepo, txnRepo: txnRepo, sender: sender, log: logger}
}

func (a *systemAgent) Actions() []string {
	return []string{domain.ActionInit, domain.ActionHelp, domain.ActionInfo, domain.ActionNone}
}

// SystemPrompt mengembalikan prompt skill milik agent ini.
func (a *systemAgent) SystemPrompt() string { return a.systemPrompt }

func (a *systemAgent) Handle(ctx context.Context, req agent.Request) error {
	msg := req.Message
	chat := req.Chat
	intentCost := req.IntentCost

	switch req.Action.Action {
	case domain.ActionInit:
		return a.handleInitAction(ctx, msg, chat, req.Action.Params, intentCost)

	case domain.ActionHelp:
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.OnboardingTemplate, intentCost)

	case domain.ActionInfo:
		return a.handleInfo(ctx, msg, chat, intentCost)

	case domain.ActionNone:
		a.log.InfoContext(ctx, "pesan tidak dikenali diabaikan", "text", msg.Text)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.SmallTalkMessage, intentCost)

	default:
		a.log.WarnContext(ctx, "action tidak dikenali", "action", req.Action.Action)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengenali intent pesan.", intentCost)
	}
}

// initReply memilih template konfirmasi init sesuai ada/tidak-nya nama ledger.
func initReply(name string) string {
	if name == "" {
		return agent.InitSuccessMessage
	}
	return fmt.Sprintf(agent.InitSuccessNamedMessage, name)
}

// handleInitAction menangani action init (aktivasi ledger).
func (a *systemAgent) handleInitAction(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, params map[string]interface{}, intentCost float64) error {
	a.log.InfoContext(ctx, "handler: init", "params", params)
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
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, initReply(ledgerName), intentCost)
	}

	// Sudah init: update nama bila diberikan, kalau tidak cukup balas status.
	if ledgerName != "" {
		if err := a.chatRepo.MarkInitialized(ctx, msg.ChatID, ledgerName); err != nil {
			a.log.ErrorContext(ctx, "gagal rename ledger", "err", err)
		}
		a.log.InfoContext(ctx, "ledger di-rename", "chat", msg.ChatID, "name", ledgerName)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Nama ledger diperbarui: %s", ledgerName), intentCost)
	}
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Akun sudah aktif. Ketik \"bantuan\" untuk format.", intentCost)
}

// handleInfo merangkai pesan metadata sesi/chat untuk diagnostic.
// Selalu tersedia (pre-init maupun post-init).
func (a *systemAgent) handleInfo(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, intentCost float64) error {
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
	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, b.String(), intentCost)
}
