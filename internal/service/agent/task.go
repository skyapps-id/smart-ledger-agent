package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type ctxKey struct{}

// Task adalah jejak eksekusi satu pesan masuk (satu pesan = satu task).
// Webhook membuat TaskID, orchestrator menyematkan task ke context; setiap
// sub-agent (dan reply helper) yang mengerjakan pesan itu mencatat langkahnya
// ke task yang sama. Setiap langkah langsung ditulis ke logger, dan di akhir
// orchestrator mencetak ringkasan: trace singkat, total biaya LLM, dan durasi.
//
// Semua method nil-safe: kode yang berjalan tanpa task (mis. unit test)
// tetap aman dipanggil.
type Task struct {
	ID    string
	Start time.Time

	log   *slog.Logger
	mu    sync.Mutex
	steps []Step
	cost  float64
}

// Step merekam satu unit kerja dalam task.
type Step struct {
	Agent   string        // pemilik langkah, mis. "transaction", "*stock.stockAgent"
	Action  string        // jenis langkah, mis. "llm.classify_intent", "llm.extract", "persist", "reply"
	Detail  string        // keterangan singkat, mis. tipe ekstraksi / action
	CostUSD float64       // biaya LLM bila langkah ini memanggil LLM
	Err     string        // kosong bila sukses
	Took    time.Duration // durasi langkah
}

// NewTaskID membuat ID task pendek (16 karakter hex) untuk korelasi log.
func NewTaskID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// NewTask membuat task dengan ID baru dan menyematkannya ke context.
// Logger opsional; nil berarti slog.Default().
func NewTask(ctx context.Context, log *slog.Logger) (context.Context, *Task) {
	return NewTaskWithID(ctx, log, "")
}

// NewTaskWithID seperti NewTask tapi memakai ID yang sudah ada (mis. dibuat
// di webhook handler); ID kosong berarti generate baru.
func NewTaskWithID(ctx context.Context, log *slog.Logger, id string) (context.Context, *Task) {
	if id == "" {
		id = NewTaskID()
	}
	if log == nil {
		log = slog.Default()
	}
	t := &Task{ID: id, Start: time.Now(), log: log}
	return context.WithValue(ctx, ctxKey{}, t), t
}

// TaskFromContext mengembalikan task aktif; nil bila tidak ada.
func TaskFromContext(ctx context.Context) *Task {
	t, _ := ctx.Value(ctxKey{}).(*Task)
	return t
}

// LoggerWithTask menambahkan atribut "task" ke logger bila ada task di ctx.
func LoggerWithTask(log *slog.Logger, ctx context.Context) *slog.Logger {
	if t := TaskFromContext(ctx); t != nil {
		return log.With("task", t.ID)
	}
	return log
}

// taskIDFromContext mengembalikan ID task aktif; "" bila tidak ada.
func taskIDFromContext(ctx context.Context) string {
	if t := TaskFromContext(ctx); t != nil {
		return t.ID
	}
	return ""
}

// AddStep mencatat satu langkah kerja ke task dan langsung menuliskannya
// ke logger (real-time monitoring); nil-safe.
func (t *Task) AddStep(agentName, action, detail string, costUSD float64, err error, took time.Duration) {
	if t == nil {
		return
	}
	s := Step{Agent: agentName, Action: action, Detail: detail, CostUSD: costUSD, Took: took}
	if err != nil {
		s.Err = err.Error()
	}
	t.mu.Lock()
	t.steps = append(t.steps, s)
	t.cost += costUSD
	n := len(t.steps)
	t.mu.Unlock()

	t.log.Info("task step",
		"task", t.ID, "step", n, "agent", s.Agent, "action", s.Action,
		"detail", s.Detail, "cost_usd", s.CostUSD, "took", s.Took, "err", s.Err)
}

// Steps mengembalikan salinan seluruh langkah yang tercatat; nil-safe.
func (t *Task) Steps() []Step {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Step, len(t.steps))
	copy(out, t.steps)
	return out
}

// Cost mengembalikan total biaya LLM yang tercatat di semua langkah; nil-safe.
func (t *Task) Cost() float64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cost
}

// Trace mengembalikan ringkasan satu-baris seluruh langkah, mis.
// "orchestrator/llm.classify_intent(record_transaction) -> transaction/llm.extract(EXPENSE)".
func (t *Task) Trace() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	parts := make([]string, 0, len(t.steps))
	for _, s := range t.steps {
		p := s.Agent + "/" + s.Action
		if s.Detail != "" {
			p += "(" + s.Detail + ")"
		}
		if s.Err != "" {
			p += "[ERR: " + s.Err + "]"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " -> ")
}
