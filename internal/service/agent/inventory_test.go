package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

// fakeInvRepo mengimplementasikan InventoryRepository via embedding;
// hanya GetByChatGoods dan SearchByName yang dipakai resolver.
type fakeInvRepo struct {
	repository.InventoryRepository
	exact  map[string]*domain.Inventory
	search []domain.Inventory
}

// fakeGoodsRepo mengimplementasikan GoodsRepository via embedding; hanya
// GetByName yang dipakai resolver (exact: nama → goods).
type fakeGoodsRepo struct {
	repository.GoodsRepository
	byName map[string]*domain.Good
}

func (f *fakeGoodsRepo) WithTx(tx *gorm.DB) repository.GoodsRepository { return f }

func (f *fakeGoodsRepo) GetByName(ctx context.Context, name string) (*domain.Good, error) {
	if g, ok := f.byName[strings.ToLower(name)]; ok {
		return g, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeInvRepo) WithTx(tx *gorm.DB) repository.InventoryRepository { return f }

func (f *fakeInvRepo) GetByChatGoods(ctx context.Context, chatID string, goodsID int64) (*domain.Inventory, error) {
	for _, inv := range f.exact {
		if inv.ChatID == chatID && inv.GoodsID == goodsID {
			return inv, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeInvRepo) SearchByName(ctx context.Context, chatID, keyword string) ([]domain.Inventory, error) {
	return f.search, nil
}

// item membuat inventory yang terhubung ke goods dengan nama tertentu.
func item(name string, qty float64) domain.Inventory {
	g := &domain.Good{Name: name}
	return domain.Inventory{ChatID: "c1", GoodsID: g.ID, Good: g, StockQty: qty, Unit: "pcs"}
}

func testGoodsRepos(exact map[string]*domain.Inventory, search []domain.Inventory) (*fakeGoodsRepo, *fakeInvRepo) {
	invRepo := &fakeInvRepo{exact: exact, search: search}
	goodsRepo := &fakeGoodsRepo{byName: map[string]*domain.Good{}}
	for _, inv := range exact {
		goodsRepo.byName[strings.ToLower(inv.Name())] = inv.Good
	}
	return goodsRepo, invRepo
}

func TestResolveInventoryExactMatch(t *testing.T) {
	beras := item("beras", 1)
	goodsRepo, invRepo := testGoodsRepos(map[string]*domain.Inventory{"beras": &beras}, nil)
	inv, err := ResolveInventoryItem(context.Background(), nil, goodsRepo, invRepo, "c1", "pakai beras 1 kg", "beras")
	if err != nil || inv.Name() != "beras" {
		t.Fatalf("expected exact match, got inv=%v err=%v", inv, err)
	}
}

func TestResolveInventorySearchUnique(t *testing.T) {
	goodsRepo, invRepo := testGoodsRepos(nil, []domain.Inventory{item("susu bmt 200g", 3)})
	inv, err := ResolveInventoryItem(context.Background(), nil, goodsRepo, invRepo, "c1", "pakai susu bmt 200g", "susu bmt")
	if err != nil {
		t.Fatalf("expected resolved, got err=%v", err)
	}
	if inv.Name() != "susu bmt 200g" {
		t.Errorf("expected susu bmt 200g, got %s", inv.Name())
	}
}

func TestResolveInventoryFilteredByOriginalMessage(t *testing.T) {
	// Classifier melepas "200g" → item_name "susu bmt"; search mengembalikan
	// dua kandidat; pesan asli menyebut "200g" → kandidat 200g menang.
	goodsRepo, invRepo := testGoodsRepos(nil, []domain.Inventory{item("susu bmt 200g", 3), item("susu bmt 400g", 5)})
	inv, err := ResolveInventoryItem(context.Background(), nil, goodsRepo, invRepo, "c1", "Pakai susu bmt 200g date 01/01", "susu bmt")
	if err != nil {
		t.Fatalf("expected resolved via message filter, got err=%v", err)
	}
	if inv.Name() != "susu bmt 200g" {
		t.Errorf("expected susu bmt 200g, got %s", inv.Name())
	}
}

func TestResolveInventoryAmbiguous(t *testing.T) {
	goodsRepo, invRepo := testGoodsRepos(nil, []domain.Inventory{item("susu bmt 200g", 3), item("susu bmt 400g", 5)})
	_, err := ResolveInventoryItem(context.Background(), nil, goodsRepo, invRepo, "c1", "pakai susu bmt dong", "susu bmt")
	var amb *AmbiguousInventoryError
	if !errors.As(err, &amb) {
		t.Fatalf("expected AmbiguousInventoryError, got %v", err)
	}
	if len(amb.Names) != 2 {
		t.Errorf("expected 2 candidates, got %v", amb.Names)
	}
}

func TestResolveInventoryNotFound(t *testing.T) {
	goodsRepo, invRepo := testGoodsRepos(nil, []domain.Inventory{})
	_, err := ResolveInventoryItem(context.Background(), nil, goodsRepo, invRepo, "c1", "pakai kecap", "kecap")
	if !errors.Is(err, ErrInventoryNotFound) {
		t.Fatalf("expected ErrInventoryNotFound, got %v", err)
	}
}
