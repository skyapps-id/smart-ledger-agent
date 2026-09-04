package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/sender"
)

// ── Reply helpers ──

// SendReply mengirim pesan ke WhatsApp dengan logging.
func SendReply(ctx context.Context, log *slog.Logger, out MessageSender, chatID, text string) error {
	log = LoggerWithTask(log, ctx)
	msg := sender.Message{
		ChatID: chatID,
		Text:   text,
		TaskID: taskIDFromContext(ctx),
	}

	if !out.Enqueue(msg) {
		log.ErrorContext(ctx, "gagal meng-enqueue balasan", "chat", chatID)
		err := fmt.Errorf("gagal meng-enqueue balasan ke waha sender")
		TaskFromContext(ctx).AddStep("reply", "enqueue", chatID, 0, err, 0)
		return err
	}

	logReply(log, ctx, chatID, text, 0)
	TaskFromContext(ctx).AddStep("reply", "enqueue", chatID, 0, nil, 0)
	return nil
}

// SendReplyWithCost mengirim pesan dengan menambahkan biaya LLM di akhir pesan.
func SendReplyWithCost(ctx context.Context, log *slog.Logger, out MessageSender, chatID, text string, totalCost float64) error {
	log = LoggerWithTask(log, ctx)
	// Format cost to 6 decimal places (microdollar precision)
	costText := fmt.Sprintf("\n\n💰 AI cost: $%.6f", totalCost)
	finalText := text + costText

	msg := sender.Message{
		ChatID: chatID,
		Text:   finalText,
		TaskID: taskIDFromContext(ctx),
	}

	if !out.Enqueue(msg) {
		log.ErrorContext(ctx, "gagal meng-enqueue balasan", "chat", chatID)
		err := fmt.Errorf("gagal meng-enqueue balasan ke waha sender")
		TaskFromContext(ctx).AddStep("reply", "enqueue", chatID, 0, err, 0)
		return err
	}

	logReply(log, ctx, chatID, text, totalCost)
	TaskFromContext(ctx).AddStep("reply", "enqueue", chatID, 0, nil, 0)
	return nil
}

func logReply(log *slog.Logger, ctx context.Context, chatID, text string, cost float64) {
	preview := text
	if len(text) > 50 {
		preview = text[:50] + "..."
	}
	if cost > 0 {
		log.InfoContext(ctx, "balasan di-enqueue ke waha sender", "chat", chatID, "preview", preview, "cost", cost)
		return
	}
	log.InfoContext(ctx, "balasan di-enqueue ke waha sender", "chat", chatID, "preview", preview)
}

// ── Error helpers ──

// BusinessError menandai error yang sudah membawa pesan siap tampil ke user.
type BusinessError struct{ msg string }

// NewBusinessError membuat error bisnis dengan pesan siap tampil.
func NewBusinessError(msg string) error { return &BusinessError{msg: msg} }

func (e *BusinessError) Error() string { return e.msg }

// LLMErrorMessage mengembalikan pesan error yang sesuai berdasarkan tipe error.
// Dibedakan antara error infrastruktur (server LLM) dan error pemahaman pesan.
func LLMErrorMessage(err error) string {
	var reqErr *llm.RequestError
	if errors.Is(err, llm.ErrRateLimited) {
		return "⏳ Server AI sedang sibuk (rate limit). Coba lagi sebentar ya."
	}
	if errors.As(err, &reqErr) {
		return "📡 Gangguan koneksi ke server AI. Coba lagi sebentar ya."
	}
	if strings.Contains(err.Error(), "llm status") {
		return "⚠️ Server AI bermasalah. Coba lagi nanti ya."
	}
	return "Maaf, gagal memahami pesan. Coba kirim ulang ya."
}
