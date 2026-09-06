package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"smart-ledger-agent/internal/domain"
)

// GoodsRepository abstraksi penyimpanan master katalog barang PER CHAT
// (ledger). Sumber kebenaran nama barang & satuan (uom + faktor konversi),
// terisolasi antar chat.
type GoodsRepository interface {
	WithTx(tx *gorm.DB) GoodsRepository
	GetByCode(ctx context.Context, code string) (*domain.Good, error)
	GetByName(ctx context.Context, chatID, name string) (*domain.Good, error)
	// GetOrCreateByName meresolve nama barang (hasil ekstraksi LLM) ke row
	// goods pada chat; bila belum ada, dibuat otomatis dengan code hasil
	// slug nama.
	GetOrCreateByName(ctx context.Context, chatID, name, uom string) (*domain.Good, error)
	SearchByName(ctx context.Context, chatID, keyword string, limit int) ([]domain.Good, error)
	ListByChat(ctx context.Context, chatID string) ([]domain.Good, error)
	Upsert(ctx context.Context, g *domain.Good) error
	// UpdateConversion menyimpan faktor konversi yang DIPELAJARI dari user
	// (1 uom = factorUom conversionUom, mis. 1 galon = 15 lt) ke master goods.
	UpdateConversion(ctx context.Context, id int64, conversionUom string, factorUom float64) error
	Delete(ctx context.Context, id int64) error
}

const defaultGoodsSearchLimit = 50

type goodsRepo struct{ db *gorm.DB }

func NewGoodsRepository(db *gorm.DB) GoodsRepository {
	return &goodsRepo{db: db}
}

func (r *goodsRepo) WithTx(tx *gorm.DB) GoodsRepository {
	return &goodsRepo{db: tx}
}

func (r *goodsRepo) GetByCode(ctx context.Context, code string) (*domain.Good, error) {
	var g domain.Good
	err := r.db.WithContext(ctx).Where("code = ?", code).First(&g).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (r *goodsRepo) GetByName(ctx context.Context, chatID, name string) (*domain.Good, error) {
	var g domain.Good
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND LOWER(name) = LOWER(?)", chatID, name).
		First(&g).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// slugCode menurunkan code goods dari nama: uppercase, non-alfanumerik →
// "-", dipangkas 32 karakter. "Susu UHT 500ml" → "SUSU-UHT-500ML".
var slugNonAlnum = regexp.MustCompile(`[^A-Za-z0-9]+`)

func slugCode(name string) string {
	s := strings.Trim(slugNonAlnum.ReplaceAllString(strings.TrimSpace(name), "-"), "-")
	if len(s) > 32 {
		s = s[:32]
	}
	if s == "" {
		s = "GOODS"
	}
	return strings.ToUpper(s)
}

// GetOrCreateByName meresolve nama barang (case-insensitive) ke row goods
// pada chat tersebut. Bila belum terdaftar, dibuat baru dengan code dari
// slug nama; bentrok code dicegah dengan suffix -2/-3 lalu fallback
// G-<unixnano>.
func (r *goodsRepo) GetOrCreateByName(ctx context.Context, chatID, name, uom string) (*domain.Good, error) {
	if g, err := r.GetByName(ctx, chatID, name); err == nil {
		return g, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	code := slugCode(name)
	for attempt := 0; attempt < 3; attempt++ {
		tryCode := code
		if attempt > 0 {
			tryCode = fmt.Sprintf("%s-%d", code, attempt+1)
		}
		g := &domain.Good{ChatID: chatID, Code: tryCode, Name: strings.TrimSpace(name), Uom: uom}
		err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "chat_id"}, {Name: "code"}},
			DoNothing: true,
		}).Create(g).Error
		if err != nil {
			return nil, err
		}
		if g.ID != 0 {
			return g, nil
		}
		// Code bentrok (DoNothing) → coba suffix berikutnya.
	}

	// Fallback terakhir: code unik dari timestamp.
	g := &domain.Good{
		ChatID: chatID,
		Code:   fmt.Sprintf("G-%d", time.Now().UnixNano()),
		Name:   strings.TrimSpace(name), Uom: uom,
	}
	if err := r.db.WithContext(ctx).Create(g).Error; err != nil {
		// Race: nama mungkin baru dibuat proses lain — ambil yang ada.
		if existing, gerr := r.GetByName(ctx, chatID, name); gerr == nil {
			return existing, nil
		}
		return nil, err
	}
	return g, nil
}

// SearchByName mencari goods pada chat berdasarkan keyword menggunakan
// ILIKE. limit <= 0 dipatok defaultGoodsSearchLimit.
func (r *goodsRepo) SearchByName(ctx context.Context, chatID, keyword string, limit int) ([]domain.Good, error) {
	if limit <= 0 {
		limit = defaultGoodsSearchLimit
	}
	var goods []domain.Good
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND name ILIKE ?", chatID, "%"+keyword+"%").
		Order("name ASC").
		Limit(limit).
		Find(&goods).Error
	return goods, err
}

func (r *goodsRepo) ListByChat(ctx context.Context, chatID string) ([]domain.Good, error) {
	var goods []domain.Good
	err := r.db.WithContext(ctx).
		Where("chat_id = ?", chatID).
		Order("name ASC").
		Find(&goods).Error
	return goods, err
}

// Upsert menyimpan goods berdasarkan (chat_id, code): insert bila belum
// ada, update atribut bila sudah (idempoten untuk pengisian katalog).
func (r *goodsRepo) Upsert(ctx context.Context, g *domain.Good) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "chat_id"}, {Name: "code"}},
		DoUpdates: clause.Assignments(map[string]any{
			"name":           g.Name,
			"uom":            g.Uom,
			"conversion_uom": g.ConversionUom,
			"factor_uom":     g.FactorUom,
		}),
	}).Create(g).Error
}

func (r *goodsRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&domain.Good{}, id).Error
}

// UpdateConversion menyimpan faktor konversi hasil belajaran user ke row
// goods: 1 uom = factorUom conversionUom.
func (r *goodsRepo) UpdateConversion(ctx context.Context, id int64, conversionUom string, factorUom float64) error {
	return r.db.WithContext(ctx).Model(&domain.Good{}).
		Where("id = ?", id).
		Updates(map[string]any{"conversion_uom": conversionUom, "factor_uom": factorUom}).Error
}
