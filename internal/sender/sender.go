// Package sender menjalankan pengiriman pesan WhatsApp secara sequential
// dengan rate limiting untuk menghindari ban WhatsApp.
package sender

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// Message adalah pesan yang akan dikirim ke WhatsApp.
type Message struct {
	ChatID string
	Text   string
}

// Processor mengirim pesan WhatsApp ke WAHA.
type Processor interface {
	SendMessage(ctx context.Context, msg Message) error
}

// Sender adalah worker sequential untuk mengirim pesan WhatsApp.
type Sender struct {
	processor Processor
	messages  chan Message
	minDelay  time.Duration
	maxDelay  time.Duration
	log       *slog.Logger
	wg        sync.WaitGroup
	quit      chan struct{}
}

// Config konfigurasi sender.
type Config struct {
	QueueSize int
	MinDelay  time.Duration
	MaxDelay  time.Duration
}

// New membuat sender sequential.
func New(cfg Config, processor Processor, logger *slog.Logger) *Sender {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{
		processor: processor,
		messages:  make(chan Message, cfg.QueueSize),
		minDelay:  cfg.MinDelay,
		maxDelay:  cfg.MaxDelay,
		log:       logger,
		quit:      make(chan struct{}),
	}
}

// Start menjalankan worker sender.
func (s *Sender) Start() {
	s.wg.Add(1)
	go s.run()
	s.log.Info("waha sender dimulai", "minDelay", s.minDelay, "maxDelay", s.maxDelay)
}

// Enqueue memasukkan pesan ke antrean pengiriman.
// Mengembalikan false bila antrean penuh.
func (s *Sender) Enqueue(msg Message) bool {
	select {
	case s.messages <- msg:
		s.log.Info("pesan di-enqueue ke waha sender", "chat", msg.ChatID)
		return true
	default:
		s.log.Warn("waha sender antrean penuh, pesan di-drop", "chat", msg.ChatID)
		return false
	}
}

// run adalah loop utama worker sender (sequential processing).
func (s *Sender) run() {
	defer s.wg.Done()
	
	// Initial random delay
	time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
	
	for msg := range s.messages {
		// Calculate human-like delay
		delay := s.calculateDelay()
		
		s.log.Info("waha sender processing message", "delay", delay, "chat", msg.ChatID)
		
		// Apply delay before sending
		select {
		case <-time.After(delay):
		case <-s.quit:
			return
		}
		
		// Kirim pesan dengan timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := s.processor.SendMessage(ctx, msg)
		cancel()
		
		if err != nil {
			s.log.Error("gagal mengirim pesan ke waha", "err", err, "chat", msg.ChatID)
		} else {
			s.log.Info("pesan terkirim ke waha", "chat", msg.ChatID)
		}
	}
}

// calculateDelay menghitung delay acak antara minDelay dan maxDelay.
func (s *Sender) calculateDelay() time.Duration {
	if s.minDelay >= s.maxDelay {
		return s.minDelay
	}
	
	// Random delay between min and max
	// Both minDelay and maxDelay are already in correct time.Duration format
	delayRange := s.maxDelay - s.minDelay
	randomDelay := time.Duration(rand.Int63n(int64(delayRange)))
	return s.minDelay + randomDelay
}

// Shutdown menghentikan sender secara graceful.
func (s *Sender) Shutdown(ctx context.Context) {
	close(s.messages)
	close(s.quit)

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.log.Info("waha sender berhenti dengan bersih")
	case <-ctx.Done():
		s.log.Warn("waha sender dihentikan paksa")
	}
}