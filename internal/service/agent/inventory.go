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

// ErrInventoryNotFound dikembalikan bila item tidak ditemukan sama sekali.
var ErrInventoryNotFound = errors.New("item tidak ditemukan di inventaris")

// AmbiguousInventoryError dikembalikan bila ada beberapa kandidat barang
// sehingga agent tidak boleh menebak; user diminta memilih (idealnya lewat
// konfirmasi bernomor).
type AmbiguousInventoryError struct {
	Names []string
	Items []domain.Inventory // kandidat lengkap (untuk menampilkan stok)
}

func (e *AmbiguousInventoryError) Error() string {
	return "beberapa barang cocok: " + strings.Join(e.Names, ", ")
}

// ResolveInventoryItem mencocokkan nama barang hasil ekstraksi LLM ke
// inventory dengan bertingkat:
//  1. exact match;
//  2. ILIKE search (substring, case-insensitive);
//  3. bila search mengembalikan beberapa kandidat, pilih kandidat yang
//     namanya muncul utuh di pesan asli user.
//
// Ini menutup kasus classifier melepas ukuran dari nama barang: pesan
// "pakai susu bmt 200g" diekstrak jadi item_name "susu bmt" (200g dianggap
// jumlah pakai), padahal di inventory barangnya tercatat "susu bmt 200g".
func ResolveInventoryItem(ctx context.Context, db *gorm.DB, invRepo repository.InventoryRepository, chatID, userMessage, itemName string) (*domain.Inventory, error) {
	inv, err := invRepo.WithTx(db).GetByChatItem(ctx, chatID, itemName)
	if err == nil {
		return inv, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	results, err := invRepo.WithTx(db).SearchByName(ctx, chatID, itemName)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, ErrInventoryNotFound
	}
	if len(results) == 1 {
		return &results[0], nil
	}

	// Beberapa kandidat: saring yang namanya muncul di pesan asli
	// (mis. pesan menyebut "... 200g" → kandidat "susu bmt 200g" menang).
	lower := strings.ToLower(userMessage)
	var matched []domain.Inventory
	for i := range results {
		if strings.Contains(lower, strings.ToLower(results[i].ItemName)) {
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
	names := make([]string, len(candidates))
	for i, it := range candidates {
		names[i] = it.ItemName
	}
	return nil, &AmbiguousInventoryError{Names: names, Items: candidates}
}

// FormatItemChoice merangkai daftar kandidat barang bernomor siap kirim.
func FormatItemChoice(query string, amb *AmbiguousInventoryError) string {
	var b strings.Builder
	fmt.Fprintf(&b, "🔍 \"%s\" ketemu beberapa barang — pilih nomornya ya:\n", query)
	for i, it := range amb.Items {
		fmt.Fprintf(&b, "%d. %s (sisa %g %s)\n", i+1, it.ItemName, it.StockQty, it.Unit)
	}
	fmt.Fprintf(&b, "\nBalas nomornya (1-%d).", len(amb.Items))
	return b.String()
}

// ItemOptionNames mengembalikan nama kandidat (untuk Options di PendingChoice).
func ItemOptionNames(amb *AmbiguousInventoryError) []string {
	names := make([]string, len(amb.Items))
	for i, it := range amb.Items {
		names[i] = it.ItemName
	}
	return names
}
