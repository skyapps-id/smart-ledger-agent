// Package repository menyediakan akses data untuk consumption cycles.
package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
)

// ConsumptionCycleRepository abstraksi akses data consumption cycles.
type ConsumptionCycleRepository interface {
	WithTx(tx *gorm.DB) ConsumptionCycleRepository
	Create(ctx context.Context, cycle *domain.ConsumptionCycle) error
	GetByID(ctx context.Context, id int64) (*domain.ConsumptionCycle, error)
	GetActiveByItem(ctx context.Context, chatID, itemName string) (*domain.ConsumptionCycle, error)
	GetActiveByItemAndBatch(ctx context.Context, chatID, itemName, batchNumber string) (*domain.ConsumptionCycle, error)
	Update(ctx context.Context, cycle *domain.ConsumptionCycle) error
	ListByChat(ctx context.Context, chatID string, limit int) ([]domain.ConsumptionCycle, error)
	ListByDateRange(ctx context.Context, chatID, itemName string, from, to time.Time) ([]domain.ConsumptionCycle, error)
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
	return r.db.WithContext(ctx).Create(cycle).Error
}

func (r *consumptionCycleRepo) GetByID(ctx context.Context, id int64) (*domain.ConsumptionCycle, error) {
	var cycle domain.ConsumptionCycle
	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&cycle).Error
	if err != nil {
		return nil, err
	}
	return &cycle, nil
}

func (r *consumptionCycleRepo) GetActiveByItem(ctx context.Context, chatID, itemName string) (*domain.ConsumptionCycle, error) {
	var cycle domain.ConsumptionCycle
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND item_name = ? AND status = ?", chatID, itemName, domain.ConsumptionCycleActive).
		Order("start_date DESC").
		First(&cycle).Error
	if err != nil {
		return nil, err
	}
	return &cycle, nil
}

func (r *consumptionCycleRepo) GetActiveByItemAndBatch(ctx context.Context, chatID, itemName, batchNumber string) (*domain.ConsumptionCycle, error) {
	var cycle domain.ConsumptionCycle
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND item_name = ? AND batch_number = ? AND status = ?", chatID, itemName, batchNumber, domain.ConsumptionCycleActive).
		Order("start_date DESC").
		First(&cycle).Error
	if err != nil {
		return nil, err
	}
	return &cycle, nil
}

func (r *consumptionCycleRepo) Update(ctx context.Context, cycle *domain.ConsumptionCycle) error {
	return r.db.WithContext(ctx).Save(cycle).Error
}

func (r *consumptionCycleRepo) ListByChat(ctx context.Context, chatID string, limit int) ([]domain.ConsumptionCycle, error) {
	var cycles []domain.ConsumptionCycle
	query := r.db.WithContext(ctx).
		Where("chat_id = ?", chatID).
		Order("start_date DESC")
	
	if limit > 0 {
		query = query.Limit(limit)
	}
	
	err := query.Find(&cycles).Error
	return cycles, err
}

func (r *consumptionCycleRepo) ListByDateRange(ctx context.Context, chatID, itemName string, from, to time.Time) ([]domain.ConsumptionCycle, error) {
	var cycles []domain.ConsumptionCycle
	query := r.db.WithContext(ctx).
		Where("chat_id = ?", chatID).
		Order("start_date DESC")
	
	if itemName != "" {
		query = query.Where("item_name = ?", itemName)
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
			"status":    domain.ConsumptionCycleCompleted,
			"end_date":  endDate,
		}).Error
}
