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
			fmt.Fprintf(&b, "- %s: %g %s (%s)\n", it.Name(), it.StockQty, it.Unit, formatLastPurchase(last))
			continue
		}
		fmt.Fprintf(&b, "- %s: %g %s\n", it.Name(), it.StockQty, it.Unit)
	}
	return b.String()
}

// formatLastPurchase merangkai info pembelian terakhir, mis.
// "beli terakhir: Rp64.000/ball, 31/08". Harga satuan (unit_price) lebih
// berguna untuk perbandingan antar pembelian; fallback ke total untuk row
// lama tanpa kolom.
func formatLastPurchase(last *domain.Transaction) string {
	if last.UnitPrice > 0 && last.Unit != "" {
		return fmt.Sprintf("beli terakhir: Rp%s/%s, %s",
			agent.FormatRupiah(last.UnitPrice), last.Unit, last.TransactionDate.Format("02/01"))
	}
	return fmt.Sprintf("beli terakhir: Rp%s, %s",
		agent.FormatRupiah(last.Amount), last.TransactionDate.Format("02/01"))
}
