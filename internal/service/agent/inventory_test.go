package agent

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

// fakeInvRepo mengimplementasikan InventoryRepository via embedding;
// hanya GetByChatItem dan SearchByName yang dipakai resolver.
type fakeInvRepo struct {
	repository.InventoryRepository
	exact  map[string]*domain.Inventory
	search []domain.Inventory
}

func (f *fakeInvRepo) WithTx(tx *gorm.DB) repository.InventoryRepository { return f }

func (f *fakeInvRepo) GetByChatItem(ctx context.Context, chatID, itemName string) (*domain.Inventory, error) {
	if inv, ok := f.exact[itemName]; ok {
		return inv, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeInvRepo) SearchByName(ctx context.Context, chatID, keyword string) ([]domain.Inventory, error) {
	return f.search, nil
}

func item(name string, qty float64) domain.Inventory {
	return domain.Inventory{ChatID: "c1", ItemName: name, StockQty: qty, Unit: "pcs"}
}

func TestResolveInventoryExactMatch(t *testing.T) {
	repo := &fakeInvRepo{exact: map[string]*domain.Inventory{"beras": {ItemName: "beras"}}}
	inv, err := ResolveInventoryItem(context.Background(), nil, repo, "c1", "pakai beras 1 kg", "beras")
	if err != nil || inv.ItemName != "beras" {
		t.Fatalf("expected exact match, got inv=%v err=%v", inv, err)
	}
}

func TestResolveInventorySearchUnique(t *testing.T) {
	repo := &fakeInvRepo{
		search: []domain.Inventory{item("susu bmt 200g", 3)},
	}
	inv, err := ResolveInventoryItem(context.Background(), nil, repo, "c1", "pakai susu bmt 200g", "susu bmt")
	if err != nil {
		t.Fatalf("expected resolved, got err=%v", err)
	}
	if inv.ItemName != "susu bmt 200g" {
		t.Errorf("expected susu bmt 200g, got %s", inv.ItemName)
	}
}

func TestResolveInventoryFilteredByOriginalMessage(t *testing.T) {
	// Classifier melepas "200g" → item_name "susu bmt"; search mengembalikan
	// dua kandidat; pesan asli menyebut "200g" → kandidat 200g menang.
	repo := &fakeInvRepo{
		search: []domain.Inventory{item("susu bmt 200g", 3), item("susu bmt 400g", 5)},
	}
	inv, err := ResolveInventoryItem(context.Background(), nil, repo, "c1", "Pakai susu bmt 200g date 01/01", "susu bmt")
	if err != nil {
		t.Fatalf("expected resolved via message filter, got err=%v", err)
	}
	if inv.ItemName != "susu bmt 200g" {
		t.Errorf("expected susu bmt 200g, got %s", inv.ItemName)
	}
}

func TestResolveInventoryAmbiguous(t *testing.T) {
	repo := &fakeInvRepo{
		search: []domain.Inventory{item("susu bmt 200g", 3), item("susu bmt 400g", 5)},
	}
	_, err := ResolveInventoryItem(context.Background(), nil, repo, "c1", "pakai susu bmt dong", "susu bmt")
	var amb *AmbiguousInventoryError
	if !errors.As(err, &amb) {
		t.Fatalf("expected AmbiguousInventoryError, got %v", err)
	}
	if len(amb.Names) != 2 {
		t.Errorf("expected 2 candidates, got %v", amb.Names)
	}
}

func TestResolveInventoryNotFound(t *testing.T) {
	repo := &fakeInvRepo{search: []domain.Inventory{}}
	_, err := ResolveInventoryItem(context.Background(), nil, repo, "c1", "pakai kecap", "kecap")
	if !errors.Is(err, ErrInventoryNotFound) {
		t.Fatalf("expected ErrInventoryNotFound, got %v", err)
	}
}
