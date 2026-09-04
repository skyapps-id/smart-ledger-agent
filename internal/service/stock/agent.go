package stock

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/service/agent"
)

// stockAgent menangani query stok/inventaris (get_stock).
type stockAgent struct {
	db *gorm.DB
	// systemPrompt adalah prompt skill agent ini (lihat prompt.go);
	// dipakai bila agent diberi LLM call sendiri.
	systemPrompt string
	goodsRepo    repository.GoodsRepository
	invRepo      repository.InventoryRepository
	txnRepo      repository.TransactionRepository
	sender       agent.MessageSender
	log          *slog.Logger
}

func NewAgent(
	db *gorm.DB,
	goodsRepo repository.GoodsRepository,
	invRepo repository.InventoryRepository,
	txnRepo repository.TransactionRepository,
	sender agent.MessageSender,
	logger *slog.Logger,
) agent.SubAgent {
	return &stockAgent{
		db:           db,
		systemPrompt: stockSystemPrompt,
		goodsRepo:    goodsRepo,
		invRepo:      invRepo,
		txnRepo:      txnRepo,
		sender:       sender,
		log:          logger,
	}
}

func (a *stockAgent) Actions() []string {
	return []string{domain.ActionGetStock}
}

// SystemPrompt mengembalikan prompt skill milik agent ini.
func (a *stockAgent) SystemPrompt() string { return a.systemPrompt }

func (a *stockAgent) Handle(ctx context.Context, req agent.Request) error {
	return a.handleGetStock(ctx, req.Message, req.Chat, req.Action.Params, req.IntentCost)
}

// handleGetStock menangani action get_stock (query stok/inventory).
func (a *stockAgent) handleGetStock(ctx context.Context, msg entity.IncomingMessage, chat *domain.Chat, params map[string]interface{}, intentCost float64) error {
	a.log.InfoContext(ctx, "handler: get_stock", "params", params)
	if !chat.Initialized {
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, agent.PreInitMessage, intentCost)
	}

	// Extract item filter dari params
	var itemFilter string
	if filter, ok := params["item_filter"].(string); ok {
		itemFilter = filter
	}

	// Jika ada item filter spesifik, gunakan search approach
	if itemFilter != "" {
		items, err := a.invRepo.WithTx(a.db).SearchByName(ctx, msg.ChatID, itemFilter)
		if err != nil {
			a.log.ErrorContext(ctx, "search inventory error", "err", err)
			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengambil data stok.", intentCost)
		}

		if len(items) == 0 {
			// Kalau search tidak ketemu, fallback ke manual filter dari semua items
			allItems, err := a.invRepo.WithTx(a.db).ListByChat(ctx, msg.ChatID)
			if err != nil {
				a.log.ErrorContext(ctx, "gagal query stok", "err", err)
				return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengambil data stok.", intentCost)
			}

			filteredItems := make([]domain.Inventory, 0)
			for _, item := range allItems {
				if strings.Contains(strings.ToLower(item.Name()), strings.ToLower(itemFilter)) {
					filteredItems = append(filteredItems, item)
				}
			}

			if len(filteredItems) == 0 {
				return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, fmt.Sprintf("Tidak ada item '%s' di inventaris.", itemFilter), intentCost)
			}

			return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
				formatStock(filteredItems, itemFilter, a.lastPurchases(ctx, msg.ChatID, filteredItems)), intentCost)
		}

		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID,
			formatStock(items, itemFilter, a.lastPurchases(ctx, msg.ChatID, items)), intentCost)
	}

	// General query "stok" → tampilkan summary kategori untuk hemat tokens
	summary, err := a.invRepo.WithTx(a.db).GetCategorySummary(ctx, msg.ChatID)
	if err != nil {
		a.log.ErrorContext(ctx, "gagal query summary", "err", err)
		return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, "Maaf, gagal mengambil data stok.", intentCost)
	}

	// Format summary untuk WhatsApp display
	var reply strings.Builder
	reply.WriteString("📦 Stok per Kategori:\n\n")

	totalItems := int64(0)
	for _, cat := range summary {
		totalItems += cat.Count
		reply.WriteString(fmt.Sprintf("• %s: %d item (contoh: %s)\n", cat.Category, cat.Count, cat.Example))
	}

	reply.WriteString(fmt.Sprintf("\nTotal: %d item", totalItems))
	reply.WriteString("\n\n💡 Ketik 'stok [kategori]' untuk detail (contoh: 'stok minuman')")

	return agent.SendReplyWithCost(ctx, a.log, a.sender, msg.ChatID, reply.String(), intentCost)
}

// lastPurchases mengambil pembelian (harga) terakhir tiap item dari
// riwayat transaksi (relasi goods_id) untuk ditampilkan di balasan stok.
// Key = nama barang (dipakai formatStock).
func (a *stockAgent) lastPurchases(ctx context.Context, chatID string, items []domain.Inventory) map[string]*domain.Transaction {
	last := make(map[string]*domain.Transaction, len(items))
	for _, it := range items {
		txn, err := a.txnRepo.WithTx(a.db).LastExpenseByGoods(ctx, chatID, it.GoodsID, 0, time.Now())
		if err != nil || txn == nil {
			continue
		}
		last[it.Name()] = txn
	}
	return last
}
