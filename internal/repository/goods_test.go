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
		Code: "GALON", Name: "galon air", Uom: "galon",
		ConversionUom: "lt", FactorUom: 19,
	}
	require.NoError(t, repo.Upsert(ctx, galon))

	got, err := repo.GetByCode(ctx, "GALON")
	require.NoError(t, err)
	assert.Equal(t, "galon air", got.Name)
	assert.Equal(t, "galon", got.Uom)
	assert.Equal(t, "lt", got.ConversionUom)
	assert.Equal(t, float64(19), got.FactorUom)

	byName, err := repo.GetByName(ctx, "galon air")
	require.NoError(t, err)
	assert.Equal(t, got.ID, byName.ID)

	_, err = repo.GetByCode(ctx, "TIDAK_ADA")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestGoodsUpsertUpdateOnSameCode(t *testing.T) {
	db := setupGoodsTestDB(t)
	repo := NewGoodsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, &domain.Good{
		Code: "GALON", Name: "galon air", Uom: "galon",
		ConversionUom: "lt", FactorUom: 19,
	}))
	require.NoError(t, repo.Upsert(ctx, &domain.Good{
		Code: "GALON", Name: "galon air isi ulang", Uom: "galon",
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
		{Code: "BERAS", Name: "beras", Uom: "kg", ConversionUom: "gr", FactorUom: 1000},
		{Code: "GALON", Name: "galon air", Uom: "galon", ConversionUom: "lt", FactorUom: 19},
	} {
		require.NoError(t, repo.Upsert(ctx, g))
	}

	goods, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Len(t, goods, 2)
	assert.Equal(t, "beras", goods[0].Name) // urut nama ASC

	require.NoError(t, repo.Delete(ctx, goods[0].ID))
	goods, err = repo.List(ctx)
	require.NoError(t, err)
	assert.Len(t, goods, 1)
}
