package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/repository"
)

// ErrGoodsNotFound dikembalikan bila barang tidak terdaftar di master goods.
// Kebijakan master-first: transaksi/inventory TIDAK auto-create goods —
// user harus mendaftarkan barang dulu ("tambah barang [x] satuan [u]").
var ErrGoodsNotFound = errors.New("barang tidak ada di master goods")

// AmbiguousGoodsError dikembalikan bila beberapa barang master cocok;
// caller meminta user menyebut nama lengkap (atau pilihan bernomor).
type AmbiguousGoodsError struct {
	Names []string
	Items []domain.Good
}

func (e *AmbiguousGoodsError) Error() string {
	return "beberapa barang cocok: " + strings.Join(e.Names, ", ")
}

// ResolveGoods mencocokkan nama barang hasil ekstraksi LLM ke master goods
// chat dengan bertingkat:
//  1. exact match (case-insensitive);
//  2. LIKE search (substring, case-insensitive);
//  3. bila beberapa kandidat, pilih yang namanya muncul utuh di pesan asli.
//
// Dipakai jalur transaksi: barang di luar master DITOLAK (ErrGoodsNotFound),
// bukan di-auto-create.
func ResolveGoods(ctx context.Context, db *gorm.DB, goodsRepo repository.GoodsRepository, chatID, userMessage, itemName string) (*domain.Good, error) {
	if g, err := goodsRepo.WithTx(db).GetByName(ctx, chatID, itemName); err == nil {
		return g, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	results, err := goodsRepo.WithTx(db).SearchByName(ctx, chatID, itemName, 5)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, ErrGoodsNotFound
	}
	if len(results) == 1 {
		return &results[0], nil
	}

	// Beberapa kandidat: saring yang namanya muncul di pesan asli.
	lower := strings.ToLower(userMessage)
	var matched []domain.Good
	for i := range results {
		if strings.Contains(lower, strings.ToLower(results[i].Name)) {
			matched = append(matched, results[i])
		}
	}
	if len(matched) == 1 {
		return &matched[0], nil
	}
	candidates := matched
	if len(candidates) == 0 {
		candidates = results
	}
	return nil, &AmbiguousGoodsError{Names: goodsNames(candidates), Items: candidates}
}

func goodsNames(items []domain.Good) []string {
	names := make([]string, len(items))
	for i, g := range items {
		names[i] = g.Name
	}
	return names
}

// GoodsOptionNames mengembalikan nama kandidat goods (untuk Options di
// PendingChoice).
func GoodsOptionNames(amb *AmbiguousGoodsError) []string {
	return goodsNames(amb.Items)
}

// FormatGoodsChoice merangkai daftar kandidat goods bernomor siap kirim.
func FormatGoodsChoice(query string, amb *AmbiguousGoodsError) string {
	var b strings.Builder
	fmt.Fprintf(&b, "🔍 \"%s\" ketemu beberapa barang — pilih nomornya ya:\n", query)
	for i, g := range amb.Items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, g.Name)
	}
	fmt.Fprintf(&b, "\nBalas nomornya (1-%d).", len(amb.Items))
	return b.String()
}
