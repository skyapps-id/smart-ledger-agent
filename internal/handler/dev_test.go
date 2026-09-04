package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/sender"
)

type mockQueue struct {
	msgs     []entity.IncomingMessage
	overflow bool // true = antrean penuh (Enqueue selalu false)
}

func (m *mockQueue) Enqueue(msg entity.IncomingMessage) bool {
	if m.overflow {
		return false
	}
	m.msgs = append(m.msgs, msg)
	return true
}

func devRequest(t *testing.T, h *DevHandler, body string) (*httptest.ResponseRecorder, echo.Context) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/dev/message", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := h.Handle(c); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	return rec, c
}

func TestDevMessageQueuesIncoming(t *testing.T) {
	q := &mockQueue{}
	h := NewDevMessage(q, nil, nil)

	rec, _ := devRequest(t, h, `{"chat_id":"628123@c.us","text":"beli kopi 15rb","sender_phone":"628123"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(q.msgs) != 1 {
		t.Fatalf("expected 1 queued message, got %d", len(q.msgs))
	}
	msg := q.msgs[0]
	if msg.ChatID != "628123@c.us" || msg.Text != "beli kopi 15rb" {
		t.Errorf("pesan tidak sesuai: %+v", msg)
	}
	if msg.TaskID == "" {
		t.Error("TaskID harus di-generate")
	}
	if msg.SessionName != "dev-test" {
		t.Errorf("expected session dev-test, got %s", msg.SessionName)
	}
	if rec.Header().Get("X-Task-ID") != msg.TaskID {
		t.Errorf("header X-Task-ID (%s) harus sama dengan msg.TaskID (%s)", rec.Header().Get("X-Task-ID"), msg.TaskID)
	}
}

func TestDevMessageDefaultsSenderToChatID(t *testing.T) {
	q := &mockQueue{}
	h := NewDevMessage(q, nil, nil)

	rec, _ := devRequest(t, h, `{"chat_id":"628999@c.us","text":"stok"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := q.msgs[0].UserPhone; got != "628999@c.us" {
		t.Errorf("sender default harus chat_id, got %s", got)
	}
}

func TestDevMessageValidation(t *testing.T) {
	q := &mockQueue{}
	h := NewDevMessage(q, nil, nil)

	for _, body := range []string{`{}`, `{"chat_id":"x"}`, `{"text":"halo"}`, `bukan-json`} {
		rec, _ := devRequest(t, h, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: expected 400, got %d", body, rec.Code)
		}
	}
	if len(q.msgs) != 0 {
		t.Errorf("tidak boleh ada pesan masuk antrean, got %d", len(q.msgs))
	}
}

func TestDevMessageQueueFull(t *testing.T) {
	q := &mockQueue{overflow: true}
	h := NewDevMessage(q, nil, nil)

	rec, _ := devRequest(t, h, `{"chat_id":"x@c.us","text":"tes"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

// fakeWaiter mengembalikan balasan statis (untuk test jalur replied/timeout).
type fakeWaiter struct {
	msg   sender.Message
	ok    bool
	delay time.Duration
}

func (f *fakeWaiter) WaitReply(taskID string, timeout time.Duration) (sender.Message, bool) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.msg, f.ok
}

func TestDevMessageReturnsBotReply(t *testing.T) {
	q := &mockQueue{}
	w := &fakeWaiter{ok: true, msg: sender.Message{ChatID: "628123@c.us", Text: "Pengeluaran tercatat: kopi Rp15.000.", TaskID: "t"}}
	h := NewDevMessage(q, w, nil)

	rec, _ := devRequest(t, h, `{"chat_id":"628123@c.us","text":"beli kopi 15rb"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"replied"`) {
		t.Errorf("status harus replied, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Pengeluaran tercatat") {
		t.Errorf("response harus memuat balasan bot, got: %s", rec.Body.String())
	}
}

func TestDevMessageTimeoutWhenNoReply(t *testing.T) {
	q := &mockQueue{}
	w := &fakeWaiter{ok: false}
	h := NewDevMessage(q, w, nil)

	rec, _ := devRequest(t, h, `{"chat_id":"x@c.us","text":"tes"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"timeout"`) {
		t.Errorf("status harus timeout, got: %s", rec.Body.String())
	}
}
