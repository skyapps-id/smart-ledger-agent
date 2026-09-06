package goods

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/sender"
	"smart-ledger-agent/internal/service/agent"
)

type mockSender struct{ msgs []string }

func (s *mockSender) Enqueue(msg sender.Message) bool {
	s.msgs = append(s.msgs, msg.Text)
	return true
}

func setupGoodsAgentTest(t *testing.T) (*goodsAgent, *gorm.DB, *mockSender, *agent.PendingConfirms) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Good{}, &domain.Inventory{}))

	senderMock := &mockSender{}
	pending := agent.NewPendingConfirms()
	ag := &goodsAgent{
		db:        db,
		goodsRepo: repository.NewGoodsRepository(db),
		invRepo:   repository.NewInventoryRepository(db),
		pending:   pending,
		sender:    senderMock,
		log:       slog.Default(),
	}
	return ag, db, senderMock, pending
}

func chatInitialized() *domain.Chat {
	return &domain.Chat{ChatID: "c1", Initialized: true}
}

func incoming(text string) entity.IncomingMessage {
	return entity.IncomingMessage{ChatID: "c1", Text: text}
}

func TestGoodsListEmpty(t *testing.T) {
	ag, _, senderMock, _ := setupGoodsAgentTest(t)
	err := ag.Handle(context.Background(), agent.Request{
		Message: incoming("master barang"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{"goods_action": "list"}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "Belum ada barang")
}

func TestGoodsListAndInfo(t *testing.T) {
	ag, _, senderMock, _ := setupGoodsAgentTest(t)
	ctx := context.Background()

	galon, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "galon air", "galon")
	require.NoError(t, err)
	require.NoError(t, ag.goodsRepo.UpdateConversion(ctx, galon.ID, "lt", 15))
	_, err = ag.goodsRepo.GetOrCreateByName(ctx, "c1", "beras 5kg", "kg")
	require.NoError(t, err)
	// Stok terkait ikut tampil di info.
	_, err = ag.invRepo.AddStock(ctx, "c1", galon.ID, 2, "galon")
	require.NoError(t, err)

	err = ag.Handle(ctx, agent.Request{
		Message: incoming("master barang"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{"goods_action": "list"}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "Master Barang (2)")
	assert.Contains(t, senderMock.msgs[0], "galon air (1 galon = 15 lt)")
	assert.Contains(t, senderMock.msgs[0], "beras 5kg (kg)")

	err = ag.Handle(ctx, agent.Request{
		Message: incoming("info barang galon air"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "info", "item_name": "galon air",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 2)
	assert.Contains(t, senderMock.msgs[1], "galon air")
	assert.Contains(t, senderMock.msgs[1], "1 galon = 15 lt")
	assert.Contains(t, senderMock.msgs[1], "Stok    : 2 galon")
}

func TestGoodsSetFactor(t *testing.T) {
	ag, db, senderMock, _ := setupGoodsAgentTest(t)
	ctx := context.Background()

	galon, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "galon air", "galon")
	require.NoError(t, err)

	err = ag.Handle(ctx, agent.Request{
		Message: incoming("set 1 galon air 15lt"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "set_factor", "item_name": "galon air", "factor_qty": 15.0, "factor_unit": "lt",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "1 galon = 15 lt")

	var fresh domain.Good
	require.NoError(t, db.First(&fresh, galon.ID).Error)
	assert.Equal(t, float64(15), fresh.FactorUom)
	assert.Equal(t, "lt", fresh.ConversionUom)
}

func TestGoodsSetUom(t *testing.T) {
	ag, db, senderMock, _ := setupGoodsAgentTest(t)
	ctx := context.Background()

	beras, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "beras 5kg", "sak")
	require.NoError(t, err)

	err = ag.Handle(ctx, agent.Request{
		Message: incoming("set satuan beras 5kg jadi kg"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "set_uom", "item_name": "beras 5kg", "unit": "kg",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "diubah ke kg")

	var fresh domain.Good
	require.NoError(t, db.First(&fresh, beras.ID).Error)
	assert.Equal(t, "kg", fresh.Uom)
}

func TestGoodsAmbiguousChoice(t *testing.T) {
	ag, _, senderMock, pending := setupGoodsAgentTest(t)
	ctx := context.Background()

	for _, name := range []string{"susu bmt 200g", "susu bmt 400g"} {
		_, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", name, "pcs")
		require.NoError(t, err)
	}

	err := ag.Handle(ctx, agent.Request{
		Message: incoming("info barang susu bmt"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "info", "item_name": "susu bmt",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "pilih nomornya")

	// Jawaban "1" di-resolve pending → dispatch ulang dengan item_name persis.
	action, _, ok := pending.Resolve("c1", "1")
	require.True(t, ok)
	assert.Equal(t, domain.ActionGoods, action.Action)
	assert.Equal(t, "susu bmt 200g", action.Params["item_name"])

	err = ag.Handle(ctx, agent.Request{
		Message: incoming("info barang susu bmt 200g"), Chat: chatInitialized(),
		Action: action,
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 2)
	assert.Contains(t, senderMock.msgs[1], "susu bmt 200g")
	assert.True(t, strings.Contains(senderMock.msgs[1], "Kode"))
}

func TestGoodsAddNewWithConversion(t *testing.T) {
	ag, db, senderMock, _ := setupGoodsAgentTest(t)
	ctx := context.Background()

	err := ag.Handle(ctx, agent.Request{
		Message: incoming("tambah barang galon air satuan galon, 1 galon = 15lt"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "add", "item_name": "galon air", "unit": "galon",
			"factor_qty": 15.0, "factor_unit": "lt",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "ditambahkan")
	assert.Contains(t, senderMock.msgs[0], "Satuan : galon")
	assert.Contains(t, senderMock.msgs[0], "1 galon = 15 lt")

	var g domain.Good
	require.NoError(t, db.Where("chat_id = ? AND name = ?", "c1", "galon air").First(&g).Error)
	assert.Equal(t, "galon", g.Uom)
	assert.Equal(t, "lt", g.ConversionUom)
	assert.Equal(t, float64(15), g.FactorUom)

	// Add ulang nama sama = update (idempoten), bukan duplikat.
	err = ag.Handle(ctx, agent.Request{
		Message: incoming("tambah barang galon air satuan galon, 1 galon = 19lt"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "add", "item_name": "galon air", "unit": "galon",
			"factor_qty": 19.0, "factor_unit": "lt",
		}},
	})
	require.NoError(t, err)
	assert.Contains(t, senderMock.msgs[1], "diperbarui")
	assert.Contains(t, senderMock.msgs[1], "1 galon = 19 lt")

	var count int64
	require.NoError(t, db.Model(&domain.Good{}).Where("name = ?", "galon air").Count(&count).Error)
	assert.Equal(t, int64(1), count)

	var fresh domain.Good
	require.NoError(t, db.Where("chat_id = ? AND name = ?", "c1", "galon air").First(&fresh).Error)
	assert.Equal(t, float64(19), fresh.FactorUom)
}

func TestGoodsAddWithCategory(t *testing.T) {
	ag, db, senderMock, _ := setupGoodsAgentTest(t)
	ctx := context.Background()

	err := ag.Handle(ctx, agent.Request{
		Message: incoming("tambah barang galon air satuan galon kategori MINUMAN"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "add", "item_name": "galon air", "unit": "galon", "category": "minuman",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "Kategori: MINUMAN")

	var g domain.Good
	require.NoError(t, db.Where("chat_id = ? AND name = ?", "c1", "galon air").First(&g).Error)
	assert.Equal(t, "MINUMAN", g.Category)
}

func TestGoodsSetCategory(t *testing.T) {
	ag, db, senderMock, _ := setupGoodsAgentTest(t)
	ctx := context.Background()

	galon, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "galon air", "galon")
	require.NoError(t, err)

	err = ag.Handle(ctx, agent.Request{
		Message: incoming("set kategori galon air jadi minuman"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "set_category", "item_name": "galon air", "category": "minuman",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "diubah ke MINUMAN")

	var fresh domain.Good
	require.NoError(t, db.First(&fresh, galon.ID).Error)
	assert.Equal(t, "MINUMAN", fresh.Category)
}

func TestGoodsListShowsCategory(t *testing.T) {
	ag, _, senderMock, _ := setupGoodsAgentTest(t)
	ctx := context.Background()

	galon, err := ag.goodsRepo.GetOrCreateByName(ctx, "c1", "galon air", "galon")
	require.NoError(t, err)
	require.NoError(t, ag.goodsRepo.UpdateCategory(ctx, galon.ID, "MINUMAN"))

	err = ag.Handle(ctx, agent.Request{
		Message: incoming("master barang"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{"goods_action": "list"}},
	})
	require.NoError(t, err)
	assert.Contains(t, senderMock.msgs[0], "[MINUMAN] galon air")
}

func TestGoodsNotFound(t *testing.T) {
	ag, _, senderMock, _ := setupGoodsAgentTest(t)
	err := ag.Handle(context.Background(), agent.Request{
		Message: incoming("info barang kecap"), Chat: chatInitialized(),
		Action: domain.ServiceAction{Action: domain.ActionGoods, Params: map[string]interface{}{
			"goods_action": "info", "item_name": "kecap",
		}},
	})
	require.NoError(t, err)
	require.Len(t, senderMock.msgs, 1)
	assert.Contains(t, senderMock.msgs[0], "belum ada di master")
}
