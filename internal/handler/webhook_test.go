package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// mockTyper merekam chat yang dinyalakan indikator "mengetik...".
type mockTyper struct{ started chan string }

func (m *mockTyper) StartTyping(ctx context.Context, chatID string) error {
	m.started <- chatID
	return nil
}

func webhookRequest(t *testing.T, h *WebhookHandler, body string) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/webhook?token=tkn", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	if err := h.Handle(e.NewContext(req, rec)); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
}

// TestWebhookStartsTypingOnQueuedMessage: pesan yang masuk antrean langsung
// menyalakan indikator mengetik agar user tahu pesannya sedang diproses.
func TestWebhookStartsTypingOnQueuedMessage(t *testing.T) {
	typer := &mockTyper{started: make(chan string, 1)}
	h := NewWebhook(&mockQueue{}, typer, "tkn", nil)

	webhookRequest(t, h, `{"event":"message","payload":{"from":"628123@c.us","body":"beli kopi 15rb"}}`)

	select {
	case chatID := <-typer.started:
		if chatID != "628123@c.us" {
			t.Errorf("typing di chat salah: %s", chatID)
		}
	case <-time.After(time.Second):
		t.Fatal("StartTyping tidak dipanggil")
	}
}

// TestWebhookNoTypingWhenIgnored: pesan yang di-ignore (fromMe) atau gagal
// masuk antrean tidak boleh menyalakan indikator mengetik.
func TestWebhookNoTypingWhenIgnored(t *testing.T) {
	cases := map[string]struct {
		queue *mockQueue
		body  string
	}{
		"fromMe":        {&mockQueue{}, `{"event":"message","payload":{"from":"628123@c.us","fromMe":true,"body":"halo"}}`},
		"antrean penuh": {&mockQueue{overflow: true}, `{"event":"message","payload":{"from":"628123@c.us","body":"halo"}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			typer := &mockTyper{started: make(chan string, 1)}
			webhookRequest(t, NewWebhook(tc.queue, typer, "tkn", nil), tc.body)

			select {
			case chatID := <-typer.started:
				t.Fatalf("StartTyping tidak boleh dipanggil, dapat %s", chatID)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}
