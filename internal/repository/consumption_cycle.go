// Package repository menyediakan akses data untuk consumption cycles.
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"smart-ledger-agent/internal/domain"
)

// ConsumptionCycleRepository abstraksi akses data consumption cycles.
// Relasi ke barang via goods_id; semua read meng-preload relasi goods.
type ConsumptionCycleRepository interface {
	WithTx(tx *gorm.DB) ConsumptionCycleRepository
	Create(ctx context.Context, cycle *domain.ConsumptionCycle) error
	GetByID(ctx context.Context, id int64) (*domain.ConsumptionCycle, error)
	GetActiveByGoods(ctx context.Context, chatID string, goodsID int64) (*domain.ConsumptionCycle, error)
	ListActiveByGoods(ctx context.Context, chatID string, goodsID int64) ([]domain.ConsumptionCycle, error)
	GetActiveByGoodsAndBatch(ctx context.Context, chatID string, goodsID int64, batchNumber string) (*domain.ConsumptionCycle, error)
	Update(ctx context.Context, cycle *domain.ConsumptionCycle) error
	ListByChat(ctx context.Context, chatID string, limit int) ([]domain.ConsumptionCycle, error)
	ListByDateRange(ctx context.Context, chatID string, goodsID int64, from, to time.Time) ([]domain.ConsumptionCycle, error)
	CompleteCycle(ctx context.Context, id int64, endDate time.Time) error
}

type consumptionCycleRepo struct{ db *gorm.DB }

func NewConsumptionCycleRepository(db *gorm.DB) ConsumptionCycleRepository {
	return &consumptionCycleRepo{db: db}
}

func (r *consumptionCycleRepo) WithTx(tx *gorm.DB) ConsumptionCycleRepository {
	return &consumptionCycleRepo{db: tx}
}

func (r *consumptionCycleRepo) Create(ctx context.Context, cycle *domain.ConsumptionCycle) error {
	// Omit associations: relasi Good hanya bacaan (preload), bukan ditulis
	// dari cycle — master goods dikelola GoodsRepository.
	return r.db.WithContext(ctx).Omit(clause.Associations).Create(cycle).Error
}

func (r *consumptionCycleRepo) GetByID(ctx context.Context, id int64) (*domain.ConsumptionCycle, error) {
	var cycle domain.ConsumptionCycle
	err := r.db.WithContext(ctx).
		Preload("Good").
		Where("id = ?", id).
		First(&cycle).Error
	if err != nil {
		return nil, err
	}
	return &cycle, nil
}

func (r *consumptionCycleRepo) GetActiveByGoods(ctx context.Context, chatID string, goodsID int64) (*domain.ConsumptionCycle, error) {
	var cycle domain.ConsumptionCycle
	err := r.db.WithContext(ctx).
		Preload("Good").
		Where("chat_id = ? AND goods_id = ? AND status = ?", chatID, goodsID, domain.ConsumptionCycleActive).
		Order("start_date DESC").
		First(&cycle).Error
	if err != nil {
		return nil, err
	}
	return &cycle, nil
}

// ListActiveByGoods mengembalikan SEMUA cycle aktif untuk satu barang —
// dipakai agent untuk meminta konfirmasi batch bila ada lebih dari satu.
func (r *consumptionCycleRepo) ListActiveByGoods(ctx context.Context, chatID string, goodsID int64) ([]domain.ConsumptionCycle, error) {
	var cycles []domain.ConsumptionCycle
	err := r.db.WithContext(ctx).
		Preload("Good").
		Where("chat_id = ? AND goods_id = ? AND status = ?", chatID, goodsID, domain.ConsumptionCycleActive).
		Order("start_date DESC").
		Find(&cycles).Error
	return cycles, err
}

func (r *consumptionCycleRepo) GetActiveByGoodsAndBatch(ctx context.Context, chatID string, goodsID int64, batchNumber string) (*domain.ConsumptionCycle, error) {
	var cycle domain.ConsumptionCycle
	err := r.db.WithContext(ctx).
		Preload("Good").
		Where("chat_id = ? AND goods_id = ? AND batch_number = ? AND status = ?", chatID, goodsID, batchNumber, domain.ConsumptionCycleActive).
		Order("start_date DESC").
		First(&cycle).Error
	if err != nil {
		return nil, err
	}
	return &cycle, nil
}

func (r *consumptionCycleRepo) Update(ctx context.Context, cycle *domain.ConsumptionCycle) error {
	return r.db.WithContext(ctx).Omit(clause.Associations).Save(cycle).Error
}

func (r *consumptionCycleRepo) ListByChat(ctx context.Context, chatID string, limit int) ([]domain.ConsumptionCycle, error) {
	var cycles []domain.ConsumptionCycle
	query := r.db.WithContext(ctx).
		Preload("Good").
		Where("chat_id = ?", chatID).
		Order("start_date DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	err := query.Find(&cycles).Error
	return cycles, err
}

func (r *consumptionCycleRepo) ListByDateRange(ctx context.Context, chatID string, goodsID int64, from, to time.Time) ([]domain.ConsumptionCycle, error) {
	var cycles []domain.ConsumptionCycle
	query := r.db.WithContext(ctx).
		Preload("Good").
		Where("chat_id = ?", chatID).
		Order("start_date DESC")

	if goodsID > 0 {
		query = query.Where("goods_id = ?", goodsID)
	}

	if !from.IsZero() {
		query = query.Where("start_date >= ?", from)
	}

	if !to.IsZero() {
		query = query.Where("start_date <= ?", to)
	}

	err := query.Find(&cycles).Error
	return cycles, err
}

func (r *consumptionCycleRepo) CompleteCycle(ctx context.Context, id int64, endDate time.Time) error {
	return r.db.WithContext(ctx).
		Model(&domain.ConsumptionCycle{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":   domain.ConsumptionCycleCompleted,
			"end_date": endDate,
		}).Error
}
