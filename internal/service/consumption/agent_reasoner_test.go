package consumption

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/sender"
	"smart-ledger-agent/internal/service/agent"
)

type mockReasoner struct {
	resp llm.ConversionReasoning
	err  error
}

func (m *mockReasoner) ReasonConversion(ctx context.Context, systemPrompt, rawText, sessionID string) (llm.ConversionReasoning, llm.Usage, error) {
	return m.resp, llm.Usage{CostUSD: 0.001}, m.err
}

type mockSender struct{ msgs []string }

func (s *mockSender) Enqueue(msg sender.Message) bool {
	s.msgs = append(s.msgs, msg.Text)
	return true
}

func newReasonerTestAgent(t *testing.T, reasoner llm.ConversionReasoner) (*consumptionAgent, *gorm.DB, *mockSender, *agent.PendingConfirms) {
	db := setupCompleteFlowTestDB(t)
	invRepo := repository.NewInventoryRepository(db)
	logRepo := repository.NewStockLogRepository(db)
	cycleRepo := repository.NewConsumptionCycleRepository(db)
	svc := NewService(db, cycleRepo, slog.Default())
	senderMock := &mockSender{}
	pending := agent.NewPendingConfirms()

	ag := &consumptionAgent{
		db:                 db,
		invRepo:            invRepo,
		logRepo:            logRepo,
		consumptionService: svc,
		invCache:           cache.New(5, 10),
		pending:            pending,
		reasoner:           reasoner,
		sender:             senderMock,
		log:                slog.Default(),
	}
	return ag, db, senderMock, pending
}

func TestResolveAmbiguousConversionLLMConvert(t *testing.T) {
	reasoner := &mockReasoner{resp: llm.ConversionReasoning{Action: "convert", ContentQty: 15, ContentUnit: "lt"}}
	ag, db, senderMock, _ := newReasonerTestAgent(t, reasoner)
	ctx := context.Background()

	inv := &domain.Inventory{ChatID: "c1", ItemName: "le minerale galon", StockQty: 1, Unit: "galon"}
	require.NoError(t, db.Create(inv).Error)

	msg := entity.IncomingMessage{ChatID: "c1", Text: "Pakai le minerale 3lt"}
	stop, qty, unit, _, err := ag.resolveAmbiguousConversion(ctx, msg, inv, inv.ItemName, 3, "lt", "", 0)
	require.NoError(t, err)
	assert.False(t, stop, "harus lanjut tanpa bertanya")
	assert.Equal(t, 0.2, qty)
	assert.Equal(t, "galon", unit)
	assert.Empty(t, senderMock.msgs, "tidak ada pertanyaan terkirim")

	var fresh domain.Inventory
	require.NoError(t, db.First(&fresh, inv.ID).Error)
	assert.Equal(t, float64(15), fresh.ContentSize)
	assert.Equal(t, "lt", fresh.ContentUnit)
}

func TestResolveAmbiguousConversionLLMAsk(t *testing.T) {
	reasoner := &mockReasoner{resp: llm.ConversionReasoning{Action: "ask", Question: "Galonnya isi berapa liter kira-kira?"}}
	ag, db, senderMock, pending := newReasonerTestAgent(t, reasoner)
	ctx := context.Background()

	inv := &domain.Inventory{ChatID: "c1", ItemName: "le minerale galon", StockQty: 1, Unit: "galon"}
	require.NoError(t, db.Create(inv).Error)

	msg := entity.IncomingMessage{ChatID: "c1", Text: "Pakai le minerale 3lt"}
	stop, _, _, _, err := ag.resolveAmbiguousConversion(ctx, msg, inv, inv.ItemName, 3, "lt", "", 0)
	require.NoError(t, err)
	assert.True(t, stop)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "isi berapa liter")

	_, origText, ok := pending.Resolve("c1", "15lt")
	assert.True(t, ok)
	assert.Equal(t, "Pakai le minerale 3lt", origText)
}

func TestResolveAmbiguousConversionFallbackTemplate(t *testing.T) {
	ag, db, senderMock, _ := newReasonerTestAgent(t, nil) // tanpa reasoner → template
	ctx := context.Background()

	inv := &domain.Inventory{ChatID: "c1", ItemName: "le minerale galon", StockQty: 1, Unit: "galon"}
	require.NoError(t, db.Create(inv).Error)

	msg := entity.IncomingMessage{ChatID: "c1", Text: "Pakai le minerale 3lt"}
	stop, _, _, _, err := ag.resolveAmbiguousConversion(ctx, msg, inv, inv.ItemName, 3, "lt", "", 0)
	require.NoError(t, err)
	assert.True(t, stop)
	require.Len(t, senderMock.msgs, 1)
	assert.True(t, strings.Contains(senderMock.msgs[0], "1 galon setara berapa"))
}
