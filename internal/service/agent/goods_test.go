package agent

import (
	"context"
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

func setupGoodsResolverTestDB(t *testing.T) (*gorm.DB, repository.GoodsRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("buka sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.Good{}); err != nil {
		t.Fatalf("migrasi: %v", err)
	}
	return db, repository.NewGoodsRepository(db)
}

func TestResolveGoodsExact(t *testing.T) {
	db, repo := setupGoodsResolverTestDB(t)
	ctx := context.Background()
	created, err := repo.GetOrCreateByName(ctx, "c1", "beras 5kg", "kg")
	if err != nil {
		t.Fatal(err)
	}

	got, err := ResolveGoods(ctx, db, repo, "c1", "beli beras 5kg 75rb", "BERAS 5KG")
	if err != nil || got.ID != created.ID {
		t.Fatalf("expected exact match id=%d, got=%v err=%v", created.ID, got, err)
	}
}

func TestResolveGoodsSearchUnique(t *testing.T) {
	db, repo := setupGoodsResolverTestDB(t)
	ctx := context.Background()
	if _, err := repo.GetOrCreateByName(ctx, "c1", "susu bmt 200g", "pcs"); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveGoods(ctx, db, repo, "c1", "beli susu bmt 200g", "susu bmt")
	if err != nil || got.Name != "susu bmt 200g" {
		t.Fatalf("expected resolved via search, got=%v err=%v", got, err)
	}
}

func TestResolveGoodsNotFound(t *testing.T) {
	db, repo := setupGoodsResolverTestDB(t)
	_, err := ResolveGoods(context.Background(), db, repo, "c1", "beli kecap 25rb", "kecap")
	if !errors.Is(err, ErrGoodsNotFound) {
		t.Fatalf("expected ErrGoodsNotFound, got %v", err)
	}
}

func TestResolveGoodsAmbiguous(t *testing.T) {
	db, repo := setupGoodsResolverTestDB(t)
	ctx := context.Background()
	for _, name := range []string{"susu bmt 200g", "susu bmt 400g"} {
		if _, err := repo.GetOrCreateByName(ctx, "c1", name, "pcs"); err != nil {
			t.Fatal(err)
		}
	}

	var amb *AmbiguousGoodsError
	_, err := ResolveGoods(ctx, db, repo, "c1", "beli susu bmt dong", "susu bmt")
	if !errors.As(err, &amb) || len(amb.Names) != 2 {
		t.Fatalf("expected AmbiguousGoodsError with 2 names, got %v", err)
	}

	// Pesan asli menyebut "200g" → kandidat 200g menang.
	got, err := ResolveGoods(ctx, db, repo, "c1", "beli susu bmt 200g", "susu bmt")
	if err != nil || got.Name != "susu bmt 200g" {
		t.Fatalf("expected message-filtered match, got=%v err=%v", got, err)
	}
}

func TestResolveGoodsChatIsolation(t *testing.T) {
	db, repo := setupGoodsResolverTestDB(t)
	ctx := context.Background()
	if _, err := repo.GetOrCreateByName(ctx, "c1", "galon air", "galon"); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveGoods(ctx, db, repo, "c2", "beli galon air", "galon air")
	if !errors.Is(err, ErrGoodsNotFound) {
		t.Fatalf("expected isolation (not found in c2), got %v", err)
	}
}
