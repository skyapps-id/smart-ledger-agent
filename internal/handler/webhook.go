// Package handler berisi HTTP handler berbasis Echo.
package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/handler/model"
	"smart-ledger-agent/internal/service/agent"
)

// MessageQueue abstraksi antrean pesan (diimplementasikan *worker.Pool).
type MessageQueue interface {
	Enqueue(msg entity.IncomingMessage) bool
}

// WebhookHandler menangani callback WAHA untuk pesan masuk.
type WebhookHandler struct {
	queue MessageQueue
	token string
	log   *slog.Logger
}

// NewWebhook membuat handler webhook.
func NewWebhook(queue MessageQueue, token string, logger *slog.Logger) *WebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{queue: queue, token: token, log: logger}
}

// Handle menerima webhook WAHA, memvalidasi, lalu memasukkan pesan ke antrean.
// Selalu membalas 200 OK dengan cepat (< 50ms) untuk mencegah retry WAHA.
// Setiap request yang valid mendapat TaskID (header X-Task-ID) yang dibawa
// sampai balasan, sehingga satu pesan bisa dipantau lewat satu ID di log.
func (h *WebhookHandler) Handle(c echo.Context) error {
	taskID := agent.NewTaskID()
	log := h.log.With("task", taskID)
	c.Response().Header().Set("X-Task-ID", taskID)

	if !h.validateToken(c) {
		log.Warn("webhook: token tidak cocok")
		return c.JSON(http.StatusUnauthorized, echo.Map{"error": "unauthorized"})
	}

	// Baca raw body untuk debugging bentuk payload WAHA.
	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "bad request"})
	}
	log.Info("webhook masuk", "raw", string(raw))

	var p model.WahaPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.Error("webhook: gagal decode JSON", "err", err)
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "bad request"})
	}

	log.Info("webhook parsed",
		"event", p.Event,
		"from", p.Payload.From,
		"author", p.Payload.Author,
		"fromMe", p.Payload.FromMe,
		"type", p.Payload.Type,
		"alt", p.Payload.Data.Key.RemoteJidAlt,
		"mentions", p.Payload.MentionedIds,
		"body", p.Payload.Body,
	)

	// Hanya proses pesan masuk dari pengguna (bukan pesan sendiri).
	if p.Event != "message" || p.Payload.FromMe {
		log.Info("webhook: pesan di-ignore", "event", p.Event, "fromMe", p.Payload.FromMe)
		return c.JSON(http.StatusOK, echo.Map{"status": "ignored"})
	}

	// chatID untuk membalas = "from" apa adanya (WAHA tahu meresolve-nya,
	// termasuk format @lid / @g.us pada engine NOWEB).
	chatID := strings.TrimSpace(p.Payload.From)
	text := stripBotMention(p.Payload.Body, p)
	if chatID == "" || text == "" {
		log.Info("webhook: di-ignore (chatID/body kosong)")
		return c.JSON(http.StatusOK, echo.Map{"status": "ignored"})
	}

	// Di group: hanya respon saat bot di-@mention (anti-spam).
	isGroup := strings.HasSuffix(chatID, "@g.us")
	if isGroup && !isBotMentioned(p) {
		log.Info("webhook: group tanpa mention, di-ignore", "chat", chatID)
		return c.JSON(http.StatusOK, echo.Map{"status": "ignored"})
	}

	msg := entity.IncomingMessage{
		UserPhone:   extractSender(p),
		ChatID:      chatID,
		Text:        text,
		TaskID:      taskID,
		SessionName: p.Session,
		BotID:       p.Me.ID,
		BotLid:      p.Me.Lid,
	}

	if !h.queue.Enqueue(msg) {
		log.Warn("webhook: antrean penuh", "chat", msg.ChatID, "sender", msg.UserPhone)
		// Tetap 200 agar WAHA tidak retry endlessly.
	} else {
		log.Info("webhook: pesan masuk antrean worker", "chat", msg.ChatID, "sender", msg.UserPhone)
	}

	return c.JSON(http.StatusOK, echo.Map{"status": "queued"})
}

func (h *WebhookHandler) validateToken(c echo.Context) bool {
	if h.token == "" {
		return true
	}
	return c.QueryParam("token") == h.token || c.Request().Header.Get("X-Webhook-Token") == h.token
}

// isBotMentioned melaporkan apakah bot (me.id atau me.lid) disebut di mention.
// NOWEB menaruh mention di nested contextInfo, jadi cek dua-duanya.
func isBotMentioned(p model.WahaPayload) bool {
	botIDs := make([]string, 0, 2)
	if p.Me.ID != "" {
		botIDs = append(botIDs, p.Me.ID)
	}
	if p.Me.Lid != "" {
		botIDs = append(botIDs, p.Me.Lid)
	}
	for _, mentioned := range allMentions(p) {
		for _, bot := range botIDs {
			if mentioned == bot {
				return true
			}
		}
	}
	return false
}

// allMentions menggabungkan mention dari top-level maupun nested contextInfo.
func allMentions(p model.WahaPayload) []string {
	out := append([]string{}, p.Payload.MentionedIds...)
	out = append(out, p.Payload.Data.Message.ExtendedTextMessage.ContextInfo.MentionedJid...)
	return out
}

// stripBotMention menghapus token "@<id>" milik bot (baik versi @c.us maupun
// @lid) dari body pesan group, lalu merapikan whitespace. Wajib dilakukan di
// webhook karena p.Me.ID / p.Me.Lid hanya tersedia di sini; tanpa ini body
// seperti "@159948994543807 init" akan lolos ke service utuh dan tidak cocok
// dengan command matcher (isInitCommand, isHelpCommand, dst).
func stripBotMention(body string, p model.WahaPayload) string {
	for _, id := range []string{p.Me.ID, p.Me.Lid} {
		if id == "" {
			continue
		}
		if at := strings.Index(id, "@"); at != -1 {
			body = strings.ReplaceAll(body, "@"+id[:at], "")
		}
	}
	return strings.Join(strings.Fields(body), " ")
}

// extractSender mengambil nomor pengirim asli (628xxx) untuk sender_phone
// (audit trail). Bukan partition key — partition key adalah chat_id.
// - Group: participantAlt (nomor asli) -> author -> participant.
// - Privat: remoteJidAlt (LID) -> from.
func extractSender(p model.WahaPayload) string {
	if strings.HasSuffix(p.Payload.From, "@g.us") {
		if alt := p.Payload.Data.Key.ParticipantAlt; alt != "" {
			return normalisePhone(alt)
		}
		for _, s := range []string{p.Payload.Author, p.Payload.Participant, p.Payload.Data.Key.Participant} {
			if s != "" {
				return normalisePhone(s)
			}
		}
		return normalisePhone(p.Payload.From)
	}
	if alt := p.Payload.Data.Key.RemoteJidAlt; alt != "" {
		return normalisePhone(alt)
	}
	return normalisePhone(p.Payload.From)
}

// normalisePhone mengubah "62812xxx@c.us" menjadi "62812xxx".
func normalisePhone(chatID string) string {
	if at := strings.Index(chatID, "@"); at != -1 {
		return chatID[:at]
	}
	return chatID
}
