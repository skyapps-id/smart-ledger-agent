// Package model berisi DTO hasil agregasi / query yang dikembalikan
// repository ke service. Dipisah dari implementasi repository agar service
// dapat bergantung pada bentuk data tanpa import cycle risk.
package model

import (
	"time"

	"smart-ledger-agent/internal/domain"
)

// TxnSummary hasil agregasi transaksi pada rentang waktu tertentu.
// OpeningBalance = akumulasi INCOME kategori SALDO_AWAL (posisi awal kas),
// dipisahkan dari Income agar tidak mengembungkan total pemasukan riil.
type TxnSummary struct {
	Income         float64
	OpeningBalance float64
	Expense        float64
	Count          int64
	ByCategory     map[string]float64 // rincian pengeluaran per kategori
}

// ItemBreakdown hasil agregasi pengeluaran per nama barang.
type ItemBreakdown struct {
	ItemName string
	Amount   float64
	Count    int64
}

// StockMovement adalah satu baris riwayat perubahan stok (IN/OUT) yang
// sudah di-join dengan inventaris untuk mendapat item_name & unit.
type StockMovement struct {
	ItemName   string
	Unit       string
	ChangeType domain.ChangeType
	Quantity   float64
	Notes      string
	CreatedAt  time.Time
}
