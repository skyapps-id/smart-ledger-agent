package orchestrator

import (
	"context"
	"log/slog"
	"testing"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/sender"
	"smart-ledger-agent/internal/service/agent"
)

// ── Mocks untuk orchestrator dispatch test ──

type mockOrchIntent struct {
	action domain.ServiceAction
}

func (m *mockOrchIntent) ClassifyIntent(ctx context.Context, systemPrompt, rawText, sessionID string) (domain.ServiceAction, llm.Usage, error) {
	if systemPrompt == "" {
		panic("system prompt wajib dikirim oleh pemilik prompt")
	}
	return m.action, llm.Usage{CostUSD: 0.0001}, nil
}

type mockOrchChatRepo struct{}

func (m *mockOrchChatRepo) WithTx(tx *gorm.DB) repository.ChatRepository { return m }

func (m *mockOrchChatRepo) GetOrCreate(ctx context.Context, chatID string) (*domain.Chat, error) {
	return &domain.Chat{ChatID: chatID, Initialized: true}, nil
}

func (m *mockOrchChatRepo) MarkInitialized(ctx context.Context, chatID, name string) error {
	return nil
}

type mockOrchSender struct {
	messages []sender.Message
}

func (m *mockOrchSender) Enqueue(msg sender.Message) bool {
	m.messages = append(m.messages, msg)
	return true
}

type fakeSubAgent struct {
	actions  []string
	prompt   string
	handled  []agent.Request
	handleFn func(ctx context.Context, req agent.Request) error
}

func (f *fakeSubAgent) Actions() []string    { return f.actions }
func (f *fakeSubAgent) SystemPrompt() string { return f.prompt }
func (f *fakeSubAgent) Handle(ctx context.Context, req agent.Request) error {
	f.handled = append(f.handled, req)
	if f.handleFn != nil {
		return f.handleFn(ctx, req)
	}
	return nil
}

func newTestOrchestrator(action domain.ServiceAction, agents ...agent.SubAgent) *Orchestrator {
	return New(agents, &mockOrchChatRepo{}, &mockOrchIntent{action: action}, &mockOrchSender{}, nil, slog.Default())
}

func TestOrchestratorDispatch(t *testing.T) {
	// agents membuat set agent fresh per subtest (handled tidak terakumulasi).
	type keyAgent struct {
		key string
		ag  *fakeSubAgent
	}
	newAgents := func() []keyAgent {
		return []keyAgent{
			{"stock", &fakeSubAgent{actions: []string{domain.ActionGetStock}}},
			{"report", &fakeSubAgent{actions: []string{domain.ActionGetReport}}},
			{"consumption", &fakeSubAgent{actions: []string{domain.ActionConsumption}}},
			{"txn", &fakeSubAgent{actions: []string{domain.ActionRecordTransaction}}},
			{"system", &fakeSubAgent{actions: []string{domain.ActionInit, domain.ActionHelp, domain.ActionInfo, domain.ActionNone}}},
		}
	}

	cases := []struct {
		name   string
		action string
		want   string
	}{
		{"stock ke sub-agent stok", domain.ActionGetStock, "stock"},
		{"report ke sub-agent laporan", domain.ActionGetReport, "report"},
		{"consumption ke sub-agent konsumsi", domain.ActionConsumption, "consumption"},
		{"transaksi ke sub-agent transaksi", domain.ActionRecordTransaction, "txn"},
		{"init ke system agent", domain.ActionInit, "system"},
		{"help ke system agent", domain.ActionHelp, "system"},
		{"info ke system agent", domain.ActionInfo, "system"},
		{"none ke system agent", domain.ActionNone, "system"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kas := newAgents()
			var want *fakeSubAgent
			agents := make([]agent.SubAgent, 0, len(kas))
			for _, ka := range kas {
				agents = append(agents, ka.ag)
				if ka.key == tc.want {
					want = ka.ag
				}
			}

			o := newTestOrchestrator(domain.ServiceAction{Action: tc.action}, agents...)
			err := o.Process(context.Background(), entity.IncomingMessage{ChatID: "c1", Text: "test"})
			if err != nil {
				t.Fatalf("Process error: %v", err)
			}
			if len(want.handled) != 1 {
				t.Fatalf("agent %s tidak menerima request", tc.action)
			}
			if want.handled[0].Action.Action != tc.action {
				t.Errorf("expected action %s, got %s", tc.action, want.handled[0].Action.Action)
			}
		})
	}
}

func TestOrchestratorUnknownActionReplies(t *testing.T) {
	stock := &fakeSubAgent{actions: []string{domain.ActionGetStock}}
	o := newTestOrchestrator(domain.ServiceAction{Action: "tidak_ada"}, stock)

	err := o.Process(context.Background(), entity.IncomingMessage{ChatID: "c1", Text: "test"})
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if len(stock.handled) != 0 {
		t.Error("sub-agent tidak seharusnya menerima action yang tidak di-handle")
	}
	senderMock := o.sender.(*mockOrchSender)
	if len(senderMock.messages) != 1 {
		t.Fatalf("expected 1 fallback reply, got %d", len(senderMock.messages))
	}
}

func TestOrchestratorPassesIntentCost(t *testing.T) {
	var captured agent.Request
	txn := &fakeSubAgent{
		actions: []string{domain.ActionRecordTransaction},
		handleFn: func(ctx context.Context, req agent.Request) error {
			captured = req
			return nil
		},
	}
	o := newTestOrchestrator(domain.ServiceAction{Action: domain.ActionRecordTransaction}, txn)

	_ = o.Process(context.Background(), entity.IncomingMessage{ChatID: "c1", Text: "beli kopi 15rb"})

	// Mock intent selalu mengembalikan cost 0.0001 — harus diteruskan ke sub-agent.
	if captured.IntentCost != 0.0001 {
		t.Errorf("expected IntentCost 0.0001, got %v", captured.IntentCost)
	}
	if captured.Message.ChatID != "c1" {
		t.Errorf("expected ChatID c1, got %s", captured.Message.ChatID)
	}
}
