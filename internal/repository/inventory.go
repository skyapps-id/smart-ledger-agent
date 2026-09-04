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

// InventoryRepository abstraksi penyimpanan master stok.
type InventoryRepository interface {
	WithTx(tx *gorm.DB) InventoryRepository
	GetByChatItem(ctx context.Context, chatID, itemName string) (*domain.Inventory, error)
	AddStock(ctx context.Context, chatID, itemName string, qty float64, unit string) (*domain.Inventory, error)
	DecreaseStock(ctx context.Context, id int64, qty float64) error
	UpdateContent(ctx context.Context, id int64, contentSize float64, contentUnit string) error
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

func (r *inventoryRepo) GetByChatItem(ctx context.Context, chatID, itemName string) (*domain.Inventory, error) {
	var inv domain.Inventory
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND item_name = ?", chatID, itemName).
		First(&inv).Error
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// AddStock melakukan upsert: menambah stok bila barang sudah ada,
// atau membuat entri baru bila belum. Mengembalikan baris hasil.
func (r *inventoryRepo) AddStock(ctx context.Context, chatID, itemName string, qty float64, unit string) (*domain.Inventory, error) {
	inv := domain.Inventory{
		ChatID:   chatID,
		ItemName: itemName,
		StockQty: qty,
		Unit:     unit,
	}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "chat_id"}, {Name: "item_name"}},
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
	fresh, err := r.GetByChatItem(ctx, chatID, itemName)
	if err != nil {
		return nil, err
	}
	return fresh, nil
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

// UpdateContent menyimpan isi per kemasan (faktor konversi yang dipelajari
// dari jawaban user) pada entri inventaris.
func (r *inventoryRepo) UpdateContent(ctx context.Context, id int64, contentSize float64, contentUnit string) error {
	return r.db.WithContext(ctx).Model(&domain.Inventory{}).
		Where("id = ?", id).
		Updates(map[string]any{"content_size": contentSize, "content_unit": contentUnit}).Error
}

// ListByChat mengembalikan seluruh entri inventaris pada chat (ledger).
func (r *inventoryRepo) ListByChat(ctx context.Context, chatID string) ([]domain.Inventory, error) {
	var items []domain.Inventory
	err := r.db.WithContext(ctx).
		Where("chat_id = ?", chatID).
		Order("item_name ASC").
		Find(&items).Error
	return items, err
}

// SearchByName mencari inventory items berdasarkan keyword menggunakan ILIKE.
// Maksimal 5 hasil untuk menghemat tokens di LLM context.
func (r *inventoryRepo) SearchByName(ctx context.Context, chatID, keyword string) ([]domain.Inventory, error) {
	var items []domain.Inventory
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND item_name ILIKE ?", chatID, "%"+keyword+"%").
		Order("item_name ASC").
		Limit(5).
		Find(&items).Error
	return items, err
}

// GetCategorySummary mengelompokkan inventory items ke kategori sederhana
// untuk display yang hemat tokens di WhatsApp reply.
// Kategorisasi berdasarkan keyword analysis pada item names.
func (r *inventoryRepo) GetCategorySummary(ctx context.Context, chatID string) ([]CategorySummary, error) {
	// Load all items for categorization
	var items []domain.Inventory
	err := r.db.WithContext(ctx).
		Where("chat_id = ?", chatID).
		Order("item_name ASC").
		Find(&items).Error
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

	// Categorize items
	categorized := make(map[string][]string)
	for _, item := range items {
		lowerName := strings.ToLower(item.ItemName)
		itemCategorized := false

		for category, keywords := range categories {
			if category == "LAINNYA" {
				continue // Skip "LAINNYA" for now
			}

			for _, keyword := range keywords {
				if strings.Contains(lowerName, keyword) {
					categorized[category] = append(categorized[category], item.ItemName)
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
			categorized["LAINNYA"] = append(categorized["LAINNYA"], item.ItemName)
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
