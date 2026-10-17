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

// seedStock mendaftarkan barang (kategori opsional) + stoknya.
func seedStock(t *testing.T, db *gorm.DB, name, category string) {
	t.Helper()
	ctx := context.Background()
	goodsRepo := NewGoodsRepository(db)
	g, err := goodsRepo.GetOrCreateByName(ctx, "c1", name, "pcs")
	require.NoError(t, err)
	if category != "" {
		require.NoError(t, goodsRepo.UpdateCategory(ctx, g.ID, category))
	}
	_, err = NewInventoryRepository(db).AddStock(ctx, "c1", g.ID, 1, "pcs")
	require.NoError(t, err)
}

func setupInventoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Good{}, &domain.Inventory{}))
	return db
}

func names(items []domain.Inventory) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name())
	}
	return out
}

// TestListByCategoryMatchesSummary: "stok [kategori]" memakai aturan yang
// sama dengan ringkasan — kategori master menang, keyword hanya fallback.
func TestListByCategoryMatchesSummary(t *testing.T) {
	db := setupInventoryTestDB(t)
	seedStock(t, db, "galon air", "MINUMAN")     // kategori master
	seedStock(t, db, "susu uht", "")             // fallback keyword "susu"
	seedStock(t, db, "air mancur hias", "DEKOR") // master menang atas keyword "air"
	seedStock(t, db, "beras", "SEMBAKO")
	seedStock(t, db, "sapu", "HARI_HARI")
	repo := NewInventoryRepository(db)
	ctx := context.Background()

	got, err := repo.ListByCategory(ctx, "c1", "minuman")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"galon air", "susu uht"}, names(got))

	got, err = repo.ListByCategory(ctx, "c1", "hari hari")
	require.NoError(t, err)
	assert.Equal(t, []string{"sapu"}, names(got), "spasi dianggap underscore")

	got, err = repo.ListByCategory(ctx, "c1", "kecap")
	require.NoError(t, err)
	assert.Empty(t, got, "bukan kategori → kosong (caller fallback ke cari nama)")

	summary, err := repo.GetCategorySummary(ctx, "c1")
	require.NoError(t, err)
	counts := map[string]int64{}
	for _, s := range summary {
		counts[s.Category] = s.Count
	}
	assert.Equal(t, int64(2), counts["MINUMAN"], "jumlah ringkasan = isi ListByCategory")
}

// TestItemCategoryDeterministic: barang yang cocok ke beberapa keyword
// ("mie susu" → SEMBAKO via "mie" & MINUMAN via "susu") selalu jatuh ke
// kategori yang sama — yang pertama di urutan categoryKeywords (MINUMAN).
func TestItemCategoryDeterministic(t *testing.T) {
	inv := domain.Inventory{Good: &domain.Good{Name: "mie susu"}}
	for i := 0; i < 50; i++ {
		assert.Equal(t, "MINUMAN", itemCategory(inv))
	}
	assert.Equal(t, "LAINNYA", itemCategory(domain.Inventory{Good: &domain.Good{Name: "obeng"}}))
}
