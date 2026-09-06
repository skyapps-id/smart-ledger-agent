package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
)

func setupGoodsTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Good{}))
	return db
}

func TestGoodsUpsertAndLookup(t *testing.T) {
	db := setupGoodsTestDB(t)
	repo := NewGoodsRepository(db)
	ctx := context.Background()

	galon := &domain.Good{
		ChatID: "c1", Code: "GALON", Name: "galon air", Uom: "galon",
		ConversionUom: "lt", FactorUom: 19,
	}
	require.NoError(t, repo.Upsert(ctx, galon))

	got, err := repo.GetByCode(ctx, "GALON")
	require.NoError(t, err)
	assert.Equal(t, "galon air", got.Name)
	assert.Equal(t, "galon", got.Uom)
	assert.Equal(t, "lt", got.ConversionUom)
	assert.Equal(t, float64(19), got.FactorUom)

	byName, err := repo.GetByName(ctx, "c1", "galon air")
	require.NoError(t, err)
	assert.Equal(t, got.ID, byName.ID)

	// Nama sama di chat lain TIDAK boleh ketemu — goods terisolasi per chat.
	_, err = repo.GetByName(ctx, "c2", "galon air")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	_, err = repo.GetByCode(ctx, "TIDAK_ADA")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGoodsUpsertUpdateOnSameCode(t *testing.T) {
	db := setupGoodsTestDB(t)
	repo := NewGoodsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, &domain.Good{
		ChatID: "c1", Code: "GALON", Name: "galon air", Uom: "galon",
		ConversionUom: "lt", FactorUom: 19,
	}))
	require.NoError(t, repo.Upsert(ctx, &domain.Good{
		ChatID: "c1", Code: "GALON", Name: "galon air isi ulang", Uom: "galon",
		ConversionUom: "lt", FactorUom: 18.9,
	}))

	var count int64
	require.NoError(t, db.Model(&domain.Good{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	got, err := repo.GetByCode(ctx, "GALON")
	require.NoError(t, err)
	assert.Equal(t, "galon air isi ulang", got.Name)
	assert.Equal(t, float64(18.9), got.FactorUom)
}

func TestGoodsListAndDelete(t *testing.T) {
	db := setupGoodsTestDB(t)
	repo := NewGoodsRepository(db)
	ctx := context.Background()

	for _, g := range []*domain.Good{
		{ChatID: "c1", Code: "BERAS", Name: "beras", Uom: "kg", ConversionUom: "gr", FactorUom: 1000},
		{ChatID: "c1", Code: "GALON", Name: "galon air", Uom: "galon", ConversionUom: "lt", FactorUom: 19},
	} {
		require.NoError(t, repo.Upsert(ctx, g))
	}

	goods, err := repo.ListByChat(ctx, "c1")
	require.NoError(t, err)
	assert.Len(t, goods, 2)
	assert.Equal(t, "beras", goods[0].Name) // urut nama ASC

	require.NoError(t, repo.Delete(ctx, goods[0].ID))
	goods, err = repo.ListByChat(ctx, "c1")
	require.NoError(t, err)
	assert.Len(t, goods, 1)
}

func TestGoodsPerChatIsolation(t *testing.T) {
	db := setupGoodsTestDB(t)
	repo := NewGoodsRepository(db)
	ctx := context.Background()

	g1, err := repo.GetOrCreateByName(ctx, "c1", "galon air", "galon")
	require.NoError(t, err)
	g2, err := repo.GetOrCreateByName(ctx, "c2", "galon air", "galon")
	require.NoError(t, err)

	// Nama sama di chat berbeda = row goods berbeda (ledger terisolasi).
	assert.NotEqual(t, g1.ID, g2.ID)
	assert.Equal(t, "c1", g1.ChatID)
	assert.Equal(t, "c2", g2.ChatID)

	// Faktor konversi chat c1 TIDAK bocor ke chat c2.
	require.NoError(t, repo.UpdateConversion(ctx, g1.ID, "lt", 19))
	fresh2, err := repo.GetByName(ctx, "c2", "galon air")
	require.NoError(t, err)
	assert.Equal(t, float64(0), fresh2.FactorUom)

	c1Goods, err := repo.ListByChat(ctx, "c1")
	require.NoError(t, err)
	assert.Len(t, c1Goods, 1)
}
