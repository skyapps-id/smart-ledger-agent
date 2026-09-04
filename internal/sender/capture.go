package sender

import (
	"sync"
	"time"
)

// Capture membungkus Sender asli dan merekam balasan per task ID.
// Dipakai endpoint dev (/dev/message) untuk mengembalikan balasan bot
// langsung di response HTTP.
//
// Aturan pengiriman:
//   - Reply dari pesan webhook asli → diteruskan ke WAHA seperti biasa.
//   - Reply untuk task yang berasal dari /dev/message (punya waiter) →
//     hanya ditangkap, TIDAK diteruskan ke WAHA.
type Capture struct {
	next MessageEnqueuer

	mu      sync.Mutex
	waiters map[string]chan Message
}

// MessageEnqueuer adalah kemampuan antrean yang dibutuhkan Capture
// (dipenuhi *Sender).
type MessageEnqueuer interface {
	Enqueue(msg Message) bool
}

// NewCapture membungkus sender asli.
func NewCapture(next MessageEnqueuer) *Capture {
	return &Capture{
		next:    next,
		waiters: make(map[string]chan Message),
	}
}

// Enqueue menyampaikan pesan ke waiter yang menunggu task ID tersebut
// (pesan dev — tidak ke WAHA), atau meneruskan ke sender asli (pesan webhook).
func (c *Capture) Enqueue(msg Message) bool {
	c.mu.Lock()
	ch, isDev := c.waiters[msg.TaskID]
	if isDev {
		delete(c.waiters, msg.TaskID)
	}
	c.mu.Unlock()

	if isDev {
		// Pesan dari /dev/message: hanya untuk response HTTP,
		// tidak dikirim ke WhatsApp.
		ch <- msg
		return true
	}

	return c.next.Enqueue(msg)
}

// WaitReply menunggu balasan untuk taskID sampai timeout.
// Return (message, true) bila balasan tiba; (zero, false) bila timeout.
func (c *Capture) WaitReply(taskID string, timeout time.Duration) (Message, bool) {
	if taskID == "" {
		return Message{}, false
	}

	ch := make(chan Message, 1)
	c.mu.Lock()
	c.waiters[taskID] = ch
	c.mu.Unlock()

	select {
	case msg := <-ch:
		return msg, true
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.waiters, taskID)
		c.mu.Unlock()
		return Message{}, false
	}
}
