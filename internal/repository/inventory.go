package repository

import (
	"context"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"smart-ledger-agent/internal/domain"
)

// ErrInsufficientStock dikembalikan saat stok tidak mencukupi untuk pemakaian.
var ErrInsufficientStock = errors.New("stok tidak mencukupi")

// InventoryRepository abstraksi penyimpanan master stok. Relasi ke barang
// via goods_id; pencarian by-name di-resolve lewat join ke master goods.
type InventoryRepository interface {
	WithTx(tx *gorm.DB) InventoryRepository
	GetByChatGoods(ctx context.Context, chatID string, goodsID int64) (*domain.Inventory, error)
	AddStock(ctx context.Context, chatID string, goodsID int64, qty float64, unit string) (*domain.Inventory, error)
	DecreaseStock(ctx context.Context, id int64, qty float64) error
	ListByChat(ctx context.Context, chatID string) ([]domain.Inventory, error)
	SearchByName(ctx context.Context, chatID, keyword string) ([]domain.Inventory, error)
	GetCategorySummary(ctx context.Context, chatID string) ([]CategorySummary, error)
}

// CategorySummary merepresentasikan summary inventory per kategori sederhana.
type CategorySummary struct {
	Category string `json:"category"`
	Count    int64  `json:"count"`
	Example  string `json:"example"` // Contoh item dari kategori ini
}

type inventoryRepo struct{ db *gorm.DB }

func NewInventoryRepository(db *gorm.DB) InventoryRepository {
	return &inventoryRepo{db: db}
}

func (r *inventoryRepo) WithTx(tx *gorm.DB) InventoryRepository {
	return &inventoryRepo{db: tx}
}

func (r *inventoryRepo) GetByChatGoods(ctx context.Context, chatID string, goodsID int64) (*domain.Inventory, error) {
	var inv domain.Inventory
	err := r.db.WithContext(ctx).
		Preload("Good").
		Where("chat_id = ? AND goods_id = ?", chatID, goodsID).
		First(&inv).Error
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// AddStock melakukan upsert: menambah stok bila barang sudah ada di chat,
// atau membuat entri baru bila belum. Conflict key = (chat_id, goods_id).
func (r *inventoryRepo) AddStock(ctx context.Context, chatID string, goodsID int64, qty float64, unit string) (*domain.Inventory, error) {
	inv := domain.Inventory{
		ChatID:   chatID,
		GoodsID:  goodsID,
		StockQty: qty,
		Unit:     unit,
	}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "chat_id"}, {Name: "goods_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"stock_qty": gorm.Expr("inventory.stock_qty + ?", qty),
				"unit":      unit,
			}),
		}).Create(&inv).Error
	})
	if err != nil {
		return nil, err
	}

	// Ambil nilai terbaru setelah upsert agar ID & stock_qty akurat.
	return r.GetByChatGoods(ctx, chatID, goodsID)
}

// DecreaseStock mengurangi stok secara atomik. Mengembalikan
// ErrInsufficientStock bila stok tidak mencukupi.
func (r *inventoryRepo) DecreaseStock(ctx context.Context, id int64, qty float64) error {
	res := r.db.WithContext(ctx).
		Model(&domain.Inventory{}).
		Where("id = ? AND stock_qty >= ?", id, qty).
		Update("stock_qty", gorm.Expr("stock_qty - ?", qty))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrInsufficientStock
	}
	return nil
}

// ListByChat mengembalikan seluruh entri inventaris pada chat (ledger),
// diurutkan berdasar nama barang di master goods.
func (r *inventoryRepo) ListByChat(ctx context.Context, chatID string) ([]domain.Inventory, error) {
	var items []domain.Inventory
	err := r.db.WithContext(ctx).
		Preload("Good").
		Joins("JOIN goods ON goods.id = inventory.goods_id").
		Where("inventory.chat_id = ?", chatID).
		Order("goods.name ASC").
		Find(&items).Error
	return items, err
}

// SearchByName mencari inventory items berdasarkan NAMA BARANG di master
// goods (case-insensitive join; LOWER LIKE portabel lintas dialek).
// Maksimal 5 hasil untuk menghemat tokens di LLM context.
func (r *inventoryRepo) SearchByName(ctx context.Context, chatID, keyword string) ([]domain.Inventory, error) {
	var items []domain.Inventory
	err := r.db.WithContext(ctx).
		Preload("Good").
		Joins("JOIN goods ON goods.id = inventory.goods_id").
		Where("inventory.chat_id = ? AND LOWER(goods.name) LIKE LOWER(?)", chatID, "%"+keyword+"%").
		Order("goods.name ASC").
		Limit(5).
		Find(&items).Error
	return items, err
}

// GetCategorySummary mengelompokkan inventory items ke kategori sederhana
// untuk display yang hemat tokens di WhatsApp reply.
// Kategorisasi berdasarkan keyword analysis pada nama barang di goods.
func (r *inventoryRepo) GetCategorySummary(ctx context.Context, chatID string) ([]CategorySummary, error) {
	// Load all items for categorization
	items, err := r.ListByChat(ctx, chatID)
	if err != nil {
		return nil, err
	}

	// Simple categorization based on keywords
	categories := map[string][]string{
		"MINUMAN":   {"susu", "kopi", "teh", "air", "minuman", "jus", "soda"},
		"SEMBAKO":   {"beras", "gula", "tepung", "mie", "bumbu", "minyak", "kacang", "cabe", "bawang"},
		"MAKAN":     {"roti", "biskuit", "snack", "keripik", "wafer", "coklat"},
		"HARI_HARI": {"sabun", "detergent", "tissue", "plastik", "pembersih", "shampo", "pasta gigi"},
		"POPUK":     {"popok", "diaper", "pampers"},
		"LAINNYA":   {},
	}

	// Categorize items: kategori kanonik di master goods MENANG; heuristic
	// keyword hanya fallback untuk barang yang belum punya kategori.
	categorized := make(map[string][]string)
	for _, item := range items {
		name := item.Name()
		if item.Good != nil && item.Good.Category != "" {
			categorized[item.Good.Category] = append(categorized[item.Good.Category], name)
			continue
		}
		lowerName := strings.ToLower(name)
		itemCategorized := false

		for category, keywords := range categories {
			if category == "LAINNYA" {
				continue // Skip "LAINNYA" for now
			}

			for _, keyword := range keywords {
				if strings.Contains(lowerName, keyword) {
					categorized[category] = append(categorized[category], name)
					itemCategorized = true
					break
				}
			}

			if itemCategorized {
				break
			}
		}

		// Kalau tidak match ke kategori manapun, masuk ke "LAINNYA"
		if !itemCategorized {
			categorized["LAINNYA"] = append(categorized["LAINNYA"], name)
		}
	}

	// Build summary
	var summary []CategorySummary
	for category, items := range categorized {
		if len(items) == 0 {
			continue
		}

		// Ambil contoh item pertama untuk display
		example := items[0]
		if len(example) > 20 {
			example = example[:20] + "..."
		}

		summary = append(summary, CategorySummary{
			Category: category,
			Count:    int64(len(items)),
			Example:  example,
		})
	}

	// Sort by count (descending)
	sort.Slice(summary, func(i, j int) bool {
		return summary[i].Count > summary[j].Count
	})

	return summary, nil
}
