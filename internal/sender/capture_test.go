package sender

import (
	"testing"
	"time"
)

// mockNext mencatat pesan yang diteruskan ke sender asli.
type mockNext struct {
	msgs []Message
	fail bool
}

func (m *mockNext) Enqueue(msg Message) bool {
	if m.fail {
		return false
	}
	m.msgs = append(m.msgs, msg)
	return true
}

func TestCaptureForwardsWhenNoWaiter(t *testing.T) {
	next := &mockNext{}
	c := NewCapture(next)

	ok := c.Enqueue(Message{ChatID: "a@c.us", Text: "halo", TaskID: "t1"})
	if !ok {
		t.Fatal("expected enqueue true")
	}
	if len(next.msgs) != 1 {
		t.Fatalf("pesan webhook harus diteruskan ke WAHA, got %d", len(next.msgs))
	}
}

func TestCaptureDevReplyNotForwarded(t *testing.T) {
	next := &mockNext{}
	c := NewCapture(next)

	go func() {
		// Simulasi /dev/message menunggu balasan.
		time.Sleep(10 * time.Millisecond)
		c.Enqueue(Message{ChatID: "a@c.us", Text: "Pengeluaran tercatat", TaskID: "dev-1"})
	}()

	msg, ok := c.WaitReply("dev-1", time.Second)
	if !ok {
		t.Fatal("expected reply captured")
	}
	if msg.Text != "Pengeluaran tercatat" {
		t.Errorf("unexpected reply: %s", msg.Text)
	}
	if len(next.msgs) != 0 {
		t.Errorf("reply dev TIDAK boleh diteruskan ke WAHA, got %d", len(next.msgs))
	}
}

func TestCaptureWaitReplyTimeout(t *testing.T) {
	next := &mockNext{}
	c := NewCapture(next)

	if _, ok := c.WaitReply("tidak-ada", 20*time.Millisecond); ok {
		t.Fatal("expected timeout")
	}

	// Setelah timeout, waiter dihapus: enqueue task yang sama
	// diperlakukan seperti pesan biasa (diteruskan ke WAHA).
	c.Enqueue(Message{ChatID: "a", Text: "x", TaskID: "tidak-ada"})
	if len(next.msgs) != 1 {
		t.Fatalf("setelah timeout harus forward ke WAHA, got %d", len(next.msgs))
	}
}

func TestCaptureEmptyTaskIDNotWaitable(t *testing.T) {
	c := NewCapture(&mockNext{})
	if _, ok := c.WaitReply("", time.Millisecond); ok {
		t.Fatal("taskID kosong tidak boleh waitable")
	}
}
