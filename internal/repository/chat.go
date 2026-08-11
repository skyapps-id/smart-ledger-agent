package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"smart-ledger-agent/internal/domain"
)

// ChatRepository abstraksi data onboarding per chat (pemilik ledger).
type ChatRepository interface {
	WithTx(tx *gorm.DB) ChatRepository
	GetOrCreate(ctx context.Context, chatID string) (*domain.Chat, error)
	MarkInitialized(ctx context.Context, chatID string) error
}

type chatRepo struct{ db *gorm.DB }

func NewChatRepository(db *gorm.DB) ChatRepository {
	return &chatRepo{db: db}
}

func (r *chatRepo) WithTx(tx *gorm.DB) ChatRepository {
	return &chatRepo{db: tx}
}

// GetOrCreate mengembalikan record chat, membuat baru bila belum ada.
func (r *chatRepo) GetOrCreate(ctx context.Context, chatID string) (*domain.Chat, error) {
	var c domain.Chat
	c.ChatID = chatID

	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "chat_id"}},
		DoNothing: true,
	}).Create(&c).Error
	if err != nil {
		return nil, err
	}

	if err := r.db.WithContext(ctx).Where("chat_id = ?", chatID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *chatRepo) MarkInitialized(ctx context.Context, chatID string) error {
	return r.db.WithContext(ctx).
		Model(&domain.Chat{}).
		Where("chat_id = ?", chatID).
		Update("initialized", true).Error
}
