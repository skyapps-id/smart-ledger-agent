package stock

import (
	"fmt"
	"strings"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/service/agent"
)

// formatStock merangkai daftar inventory menjadi teks balasan stok.
// lastPurchases (opsional) memetakan nama item → transaksi pembelian
// terakhirnya agar user tahu harga beli terakhir per item.
func formatStock(items []domain.Inventory, itemFilter string, lastPurchases map[string]*domain.Transaction) string {
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
		if last, ok := lastPurchases[it.Name()]; ok && last != nil {
			fmt.Fprintf(&b, "- %s: %g %s (beli terakhir: Rp%s, %s)\n",
				it.Name(), it.StockQty, it.Unit,
				agent.FormatRupiah(last.Amount), last.TransactionDate.Format("02/01"))
			continue
		}
		fmt.Fprintf(&b, "- %s: %g %s\n", it.Name(), it.StockQty, it.Unit)
	}
	return b.String()
}
