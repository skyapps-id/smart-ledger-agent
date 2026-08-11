package repository

import (
	"context"
	"errors"

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
	ListByChat(ctx context.Context, chatID string) ([]domain.Inventory, error)
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

// ListByChat mengembalikan seluruh entri inventaris pada chat (ledger).
func (r *inventoryRepo) ListByChat(ctx context.Context, chatID string) ([]domain.Inventory, error) {
	var items []domain.Inventory
	err := r.db.WithContext(ctx).
		Where("chat_id = ?", chatID).
		Order("item_name ASC").
		Find(&items).Error
	return items, err
}
