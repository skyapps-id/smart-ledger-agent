package handler

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/sender"
	"smart-ledger-agent/internal/service/agent"
)

// replyTimeout adalah batas menunggu balasan bot di endpoint dev.
// Pipeline LLM butuh beberapa detik (klasifikasi + ekstraksi), jadi jangan
// terlalu pendek.
const replyTimeout = 45 * time.Second

// ReplyWaiter mengembalikan balasan bot untuk satu task ID (dipenuhi
// *sender.Capture).
type ReplyWaiter interface {
	WaitReply(taskID string, timeout time.Duration) (sender.Message, bool)
}

// DevHandler menyediakan endpoint test untuk menyuntik pesan langsung ke
// pipeline (worker pool) tanpa WAHA. Hanya diregistrasi saat dev mode aktif
// (APP_ENV=development atau APP_DEV_MODE=true) — jangan pernah di production.
//
// Contoh:
//
//	curl -s -X POST localhost:8080/dev/message \
//	  -H 'Content-Type: application/json' \
//	  -d '{"chat_id":"628123456789@c.us","text":"Beli beras 1kg 100k"}'
//
// Response menunggu pipeline selesai lalu mengembalikan balasan bot:
//
//	{"status":"replied","task_id":"...","reply":{"chat_id":"...","text":"Pengeluaran tercatat: ..."}}
type DevHandler struct {
	queue  MessageQueue
	waiter ReplyWaiter // opsional; nil = balasan hanya di log
	log    *slog.Logger
}

// NewDevMessage membuat handler pesan test. waiter boleh nil.
func NewDevMessage(queue MessageQueue, waiter ReplyWaiter, logger *slog.Logger) *DevHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &DevHandler{queue: queue, waiter: waiter, log: logger}
}

type devMessageRequest struct {
	ChatID      string `json:"chat_id"`
	Text        string `json:"text"`
	SenderPhone string `json:"sender_phone"` // opsional; default = chat_id
}

// Handle menangani POST /dev/message.
func (h *DevHandler) Handle(c echo.Context) error {
	taskID := agent.NewTaskID()
	log := h.log.With("task", taskID)
	c.Response().Header().Set("X-Task-ID", taskID)

	var req devMessageRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "bad request"})
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.Text = strings.TrimSpace(req.Text)
	if req.ChatID == "" || req.Text == "" {
		return c.JSON(http.StatusBadRequest, echo.Map{
			"error": "chat_id dan text wajib diisi",
		})
	}
	if req.SenderPhone == "" {
		req.SenderPhone = req.ChatID
	}

	msg := entity.IncomingMessage{
		UserPhone:   req.SenderPhone,
		ChatID:      req.ChatID,
		Text:        req.Text,
		TaskID:      taskID,
		SessionName: "dev-test",
	}

	if !h.queue.Enqueue(msg) {
		log.Warn("dev: antrean penuh", "chat", msg.ChatID)
		return c.JSON(http.StatusServiceUnavailable, echo.Map{
			"error": "antrean penuh, coba lagi",
		})
	}
	log.Info("dev: pesan test masuk antrean worker", "chat", msg.ChatID, "text", msg.Text)

	// Tanpa waiter: perilaku fire-and-forget (balasan hanya di log).
	if h.waiter == nil {
		return c.JSON(http.StatusOK, echo.Map{
			"status":  "queued",
			"task_id": taskID,
			"chat_id": msg.ChatID,
		})
	}

	// Tunggu balasan bot lalu kembalikan di response.
	if reply, ok := h.waiter.WaitReply(taskID, replyTimeout); ok {
		log.Info("dev: balasan bot ditangkap", "chat", msg.ChatID, "reply", reply.Text)
		return c.JSON(http.StatusOK, echo.Map{
			"status":  "replied",
			"task_id": taskID,
			"chat_id": msg.ChatID,
			"reply": echo.Map{
				"chat_id": reply.ChatID,
				"text":    reply.Text,
			},
		})
	}

	return c.JSON(http.StatusAccepted, echo.Map{
		"status":  "timeout",
		"task_id": taskID,
		"chat_id": msg.ChatID,
		"hint":    "pipeline belum selesai dalam batas waktu — pantau log dengan task_id ini",
	})
}
