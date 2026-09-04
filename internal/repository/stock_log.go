package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	repomodel "smart-ledger-agent/internal/repository/model"
)

// StockLogRepository abstraksi penyimpanan riwayat perubahan stok.
type StockLogRepository interface {
	WithTx(tx *gorm.DB) StockLogRepository
	Create(ctx context.Context, log *domain.StockLog) error
	MovementsByChat(ctx context.Context, chatID string, from, to time.Time) ([]repomodel.StockMovement, error)
}

type stockLogRepo struct{ db *gorm.DB }

func NewStockLogRepository(db *gorm.DB) StockLogRepository {
	return &stockLogRepo{db: db}
}

func (r *stockLogRepo) WithTx(tx *gorm.DB) StockLogRepository {
	return &stockLogRepo{db: tx}
}

func (r *stockLogRepo) Create(ctx context.Context, log *domain.StockLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

// MovementsByChat mengembalikan riwayat perubahan stok pada chat (ledger)
// dalam rentang [from, to] (IN maupun OUT), diurutkan terbaru lebih dulu.
// Nama barang & satuan diambil lewat join inventory → goods (relasi id).
func (r *stockLogRepo) MovementsByChat(ctx context.Context, chatID string, from, to time.Time) ([]repomodel.StockMovement, error) {
	var rows []repomodel.StockMovement
	err := r.db.WithContext(ctx).
		Table("stock_logs AS sl").
		Select("g.name AS item_name, i.unit AS unit, sl.change_type AS change_type, sl.quantity AS quantity, sl.notes AS notes, sl.created_at AS created_at").
		Joins("JOIN inventory AS i ON i.id = sl.inventory_id").
		Joins("JOIN goods AS g ON g.id = i.goods_id").
		Where("i.chat_id = ? AND sl.created_at >= ? AND sl.created_at <= ?", chatID, from, to).
		Order("sl.created_at DESC").
		Scan(&rows).Error
	return rows, err
}
