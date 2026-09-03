package stock

import (
	"fmt"
	"strings"

	"smart-ledger-agent/internal/domain"
)

// formatStock merangkai daftar inventory menjadi teks balasan stok.
func formatStock(items []domain.Inventory, itemFilter string) string {
	if len(items) == 0 {
		if itemFilter != "" {
			return fmt.Sprintf("Tidak ada stok untuk \"%s\".", itemFilter)
		}
		return "Belum ada barang di inventaris."
	}
	var b strings.Builder
	if itemFilter != "" {
		fmt.Fprintf(&b, "Stok saat ini (%s):\n", itemFilter)
	} else {
		b.WriteString("Stok saat ini:\n")
	}
	for _, it := range items {
		fmt.Fprintf(&b, "- %s: %g %s\n", it.ItemName, it.StockQty, it.Unit)
	}
	return b.String()
}
