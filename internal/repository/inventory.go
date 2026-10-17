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
	ListByCategory(ctx context.Context, chatID, category string) ([]domain.Inventory, error)
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

// categoryKeywords adalah heuristic kategori untuk barang yang belum punya
// kategori di master. Slice (bukan map) agar urutan cek deterministik:
// "kopi susu" selalu jatuh ke kategori yang sama di ringkasan maupun daftar.
var categoryKeywords = []struct {
	category string
	keywords []string
}{
	{"MINUMAN", []string{"susu", "kopi", "teh", "air", "minuman", "jus", "soda"}},
	{"SEMBAKO", []string{"beras", "gula", "tepung", "mie", "bumbu", "minyak", "kacang", "cabe", "bawang"}},
	{"MAKAN", []string{"roti", "biskuit", "snack", "keripik", "wafer", "coklat"}},
	{"HARI_HARI", []string{"sabun", "detergent", "tissue", "plastik", "pembersih", "shampo", "pasta gigi"}},
	{"POPUK", []string{"popok", "diaper", "pampers"}},
}

// itemCategory menentukan kategori satu item: kategori kanonik di master
// goods MENANG; heuristic keyword hanya fallback, sisanya "LAINNYA".
func itemCategory(item domain.Inventory) string {
	if item.Good != nil && item.Good.Category != "" {
		return item.Good.Category
	}
	lowerName := strings.ToLower(item.Name())
	for _, c := range categoryKeywords {
		for _, keyword := range c.keywords {
			if strings.Contains(lowerName, keyword) {
				return c.category
			}
		}
	}
	return "LAINNYA"
}

// ListByCategory mengembalikan seluruh inventory pada kategori tertentu
// (case-insensitive; spasi dianggap "_", mis. "hari hari" → HARI_HARI).
// Memakai aturan kategori yang sama dengan GetCategorySummary agar
// "stok [kategori]" konsisten dengan ringkasan.
func (r *inventoryRepo) ListByCategory(ctx context.Context, chatID, category string) ([]domain.Inventory, error) {
	items, err := r.ListByChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	want := normalizeCategory(category)
	var matched []domain.Inventory
	for _, item := range items {
		if normalizeCategory(itemCategory(item)) == want {
			matched = append(matched, item)
		}
	}
	return matched, nil
}

// normalizeCategory menyamakan penulisan kategori untuk perbandingan.
func normalizeCategory(category string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(category)), " ", "_")
}

// GetCategorySummary mengelompokkan inventory items ke kategori sederhana
// untuk display yang hemat tokens di WhatsApp reply (lihat itemCategory).
func (r *inventoryRepo) GetCategorySummary(ctx context.Context, chatID string) ([]CategorySummary, error) {
	items, err := r.ListByChat(ctx, chatID)
	if err != nil {
		return nil, err
	}

	categorized := make(map[string][]string)
	for _, item := range items {
		category := itemCategory(item)
		categorized[category] = append(categorized[category], item.Name())
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

	// Sort by count (descending); seri → nama kategori agar urutan stabil.
	sort.Slice(summary, func(i, j int) bool {
		if summary[i].Count != summary[j].Count {
			return summary[i].Count > summary[j].Count
		}
		return summary[i].Category < summary[j].Category
	})

	return summary, nil
}
