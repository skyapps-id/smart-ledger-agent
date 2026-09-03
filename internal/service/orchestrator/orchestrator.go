// Package orchestrator adalah router utama pipeline: klasifikasi intent via
// LLM, lalu dispatch ke sub-agent spesialis domain melalui kontrak
// agent.SubAgent.
//
// Orchestrator sengaja TIDAK tahu domain apa pun: seluruh sub-agent
// disuntikkan dari composition root (cmd/server) sebagai []agent.SubAgent.
// Menambah domain baru = implementasi agent.SubAgent + append ke daftar
// agent di main — nol perubahan di package ini.
package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/service/agent"
)

// Orchestrator adalah router utama pipeline (RFC §4.1 langkah 4-6).
// Tugasnya tipis: klasifikasi intent via LLM, lalu dispatch ke sub-agent
// spesialis domain berdasarkan action hasil klasifikasi.
type Orchestrator struct {
	chatRepo repository.ChatRepository
	intent   llm.IntentExtractor
	// intentPrompt adalah system prompt milik orchestrator untuk
	// klasifikasi intent (lihat prompt.go).
	intentPrompt string
	agents       map[string]agent.SubAgent
	sender       agent.MessageSender
	// pending menyimpan konfirmasi menunggu jawaban user (mis. pilihan
	// batch bernomor). Boleh nil (fitur mati).
	pending *agent.PendingConfirms
	log     *slog.Logger
}

// New membuat orchestrator dari daftar sub-agent yang disuntikkan pemanggil.
// Setiap agent mendeklarasikan action yang ditangani lewat Actions(); di sini
// cukup dibangun registry action → agent. Agent tanpa system prompt
// diperingatkan sebagai pelanggaran kontrak skill.
func New(
	agents []agent.SubAgent,
	chatRepo repository.ChatRepository,
	intent llm.IntentExtractor,
	sender agent.MessageSender,
	pending *agent.PendingConfirms,
	logger *slog.Logger,
) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}

	registry := make(map[string]agent.SubAgent, len(agents))
	for _, ag := range agents {
		// Kontrak: setiap sub-agent wajib membawa system prompt skill-nya.
		if ag.SystemPrompt() == "" {
			logger.Warn("sub-agent tanpa system prompt", "agent", fmt.Sprintf("%T", ag))
		}
		for _, action := range ag.Actions() {
			registry[action] = ag
		}
	}

	return &Orchestrator{
		chatRepo:     chatRepo,
		intent:       intent,
		intentPrompt: intentSystemPrompt,
		agents:       registry,
		sender:       sender,
		pending:      pending,
		log:          logger,
	}
}

// Process menjalankan pipeline satu pesan masuk: klasifikasi intent LLM
// lalu dispatch ke sub-agent yang menangani action tersebut.
// Satu pesan = satu Task: ID-nya dioper via context sehingga seluruh log
// sub-agent bisa dikorelasi, dan ringkasannya dicetak di akhir proses.
func (o *Orchestrator) Process(ctx context.Context, msg entity.IncomingMessage) (err error) {
	// TaskID dibuat webhook handler; di sini tinggal dipakai (atau generate
	// baru bila pesan tidak lewat webhook, mis. test).
	ctx, task := agent.NewTaskWithID(ctx, o.log, msg.TaskID)
	log := agent.LoggerWithTask(o.log, ctx)
	log.InfoContext(ctx, "memproses pesan", "chat", msg.ChatID, "sender", msg.UserPhone, "text", msg.Text)
	defer func() {
		log.InfoContext(ctx, "task selesai",
			"steps", len(task.Steps()), "cost_total", task.Cost(),
			"durasi", time.Since(task.Start), "trace", task.Trace(), "err", err)
	}()

	chat, err := o.chatRepo.GetOrCreate(ctx, msg.ChatID)
	if err != nil {
		log.ErrorContext(ctx, "gagal get/create chat", "err", err)
		return agent.SendReply(ctx, log, o.sender, msg.ChatID, "Maaf, terjadi kendala. Coba lagi nanti.")
	}

	// Konfirmasi pending (mis. pilihan batch/ barang bernomor): pesan singkat
	// seperti "1" / "2" dijawab TANPA LLM hop — langsung dispatch action
	// pending dengan teks pesan asli (dibawa oleh konfirmasi).
	if o.pending != nil {
		if action, origText, ok := o.pending.Resolve(msg.ChatID, msg.Text); ok {
			if origText != "" {
				msg.Text = origText
			}
			log.InfoContext(ctx, "konfirmasi pending terjawab", "action", action.Action, "params", action.Params, "text", msg.Text)
			task.AddStep("orchestrator", "pending.confirm", action.Action, 0, nil, 0)
			return o.dispatch(ctx, log, task, msg, chat, action, 0)
		}
	}

	// LLM Intent Classification (pakai system prompt milik orchestrator).
	// TimeContext memberi tahun berjalan untuk contoh tanggal pendek ("11/08").
	t0 := time.Now()
	action, intentUsage, err := o.intent.ClassifyIntent(ctx, o.intentPrompt+llm.TimeContext(time.Now()), msg.Text, msg.ChatID)
	task.AddStep("orchestrator", "llm.classify_intent", action.Action, intentUsage.CostUSD, err, time.Since(t0))
	if err != nil {
		log.ErrorContext(ctx, "gagal klasifikasi intent", "err", err)
		return agent.SendReplyWithCost(ctx, log, o.sender, msg.ChatID, agent.LLMErrorMessage(err), 0)
	}

	log.InfoContext(ctx, "intent terklasifikasi", "action", action.Action, "params", action.Params)

	// Track intent classification cost
	intentCost := intentUsage.CostUSD

	return o.dispatch(ctx, log, task, msg, chat, action, intentCost)
}

// dispatch mengirimkan action hasil klasifikasi/konfirmasi ke sub-agent.
func (o *Orchestrator) dispatch(ctx context.Context, log *slog.Logger, task *agent.Task, msg entity.IncomingMessage, chat *domain.Chat, action domain.ServiceAction, intentCost float64) error {
	log.InfoContext(ctx, "dispatch ke sub-agent", "action", action.Action)

	sub, ok := o.agents[action.Action]
	if !ok {
		log.WarnContext(ctx, "action tidak dikenali", "action", action.Action)
		return agent.SendReplyWithCost(ctx, log, o.sender, msg.ChatID, "Maaf, gagal mengenali intent pesan.", intentCost)
	}

	t1 := time.Now()
	err := sub.Handle(ctx, agent.Request{
		Message:    msg,
		Chat:       chat,
		Action:     action,
		IntentCost: intentCost,
	})
	task.AddStep(fmt.Sprintf("%T", sub), "handle", action.Action, 0, err, time.Since(t1))
	return err
}
