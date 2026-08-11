// Package worker menjalankan pemrosesan pesan secara asynchronous
// dengan worker pool + retry/backoff (RFC §4.1 langkah 3 & §8.1).
package worker

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"

	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
)

// Processor adalah dependency worker (umumnya *service.Agent).
type Processor interface {
	Process(ctx context.Context, msg entity.IncomingMessage) error
}

// Job merepresentasikan satu unit pekerjaan dalam antrean.
type Job = entity.IncomingMessage

// Pool adalah antrean pekerja asynchronous.
type Pool struct {
	processor Processor
	jobs      chan Job
	maxRetry  int
	log       *slog.Logger
	wg        sync.WaitGroup
	quit      chan struct{}
}

// Config konfigurasi worker pool.
type Config struct {
	Concurrency int
	QueueSize   int
	MaxRetries  int
}

// New membuat dan menjalankan worker pool.
func New(cfg Config, processor Processor, logger *slog.Logger) *Pool {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Pool{
		processor: processor,
		jobs:      make(chan Job, cfg.QueueSize),
		maxRetry:  cfg.MaxRetries,
		log:       logger,
		quit:      make(chan struct{}),
	}

	for i := 0; i < cfg.Concurrency; i++ {
		p.wg.Add(1)
		go p.run()
	}
	logger.Info("worker pool siap", "concurrency", cfg.Concurrency, "queue", cfg.QueueSize)
	return p
}

// Enqueue memasukkan pesan ke antrean. Mengembalikan false bila antrean penuh
// (WAHA tetap menerima 200 OK; pesan dianggap drop).
func (p *Pool) Enqueue(msg entity.IncomingMessage) bool {
	select {
	case p.jobs <- msg:
		return true
	default:
		p.log.Warn("antrean penuh, pesan di-drop", "chat", msg.ChatID, "sender", msg.UserPhone)
		return false
	}
}

func (p *Pool) run() {
	defer p.wg.Done()
	for msg := range p.jobs {
		p.processWithRetry(msg)
	}
}

// processWithRetry menjalankan job dengan exponential backoff untuk
// error yang retryable (mis. HTTP 429 dari OpenRouter).
func (p *Pool) processWithRetry(msg entity.IncomingMessage) {
	const baseDelay = 2 * time.Second

	var lastErr error
	for attempt := 0; attempt <= p.maxRetry; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err := p.processor.Process(ctx, msg)
		cancel()

		if err == nil {
			return
		}
		lastErr = err

		if !llm.IsRetryable(err) {
			p.log.Error("job gagal (tidak retryable)", "err", err, "chat", msg.ChatID, "sender", msg.UserPhone)
			return
		}

		delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
		p.log.Warn("job retryable, menjadwalkan ulang",
			"attempt", attempt+1, "delay", delay, "err", err)

		select {
		case <-p.quit:
			return
		case <-time.After(delay):
		}
	}
	p.log.Error("job gagal setelah retry habis", "err", lastErr, "chat", msg.ChatID, "sender", msg.UserPhone)
}

// Shutdown menghentikan worker pool secara graceful (tunggu sisa job selesai
// atau sampai ctx selesai).
func (p *Pool) Shutdown(ctx context.Context) {
	close(p.jobs)
	close(p.quit)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.log.Info("worker pool berhenti dengan bersih")
	case <-ctx.Done():
		p.log.Warn("worker pool dihentikan paksa")
	}
}
