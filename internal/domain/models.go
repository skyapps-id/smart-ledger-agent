package domain

import (
	"time"
)

// TransactionType membatasi tipe transaksi keuangan.
type TransactionType string

const (
	TransactionIncome  TransactionType = "INCOME"
	TransactionExpense TransactionType = "EXPENSE"
)

// Chat mencatat status onboarding per chat (DM = nomor@c.us, group = id@g.us).
// Inilah pemilik ledger: laporan keuangan & inventaris terisolasi per chat,
// BUKAN per pengirim (RFC Rev. C). Pada group, seluruh anggota yang mention
// bot berbagi ledger group yang sama.
type Chat struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ChatID      string    `gorm:"size:64;uniqueIndex" json:"chat_id"`
	Initialized bool      `gorm:"default:false" json:"initialized"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (Chat) TableName() string { return "chats" }

// Transaction mencatat pergerakan uang (INCOME / EXPENSE).
// ChatID = pemilik ledger (partition key). SenderPhone = pengirim asli
// pesan tersebut (audit trail; di group bisa berbeda-beda per transaksi).
type Transaction struct {
	ID          int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	ChatID      string          `gorm:"size:64;index" json:"chat_id"`
	SenderPhone string          `gorm:"size:32" json:"sender_phone"`
	Type        TransactionType `gorm:"size:16" json:"type"`
	Category    string          `gorm:"size:32" json:"category"`
	ItemName    string          `gorm:"size:128" json:"item_name"`
	Amount      float64         `gorm:"type:numeric(15,2)" json:"amount"`
	RawPayload  string          `gorm:"type:text" json:"raw_payload"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (Transaction) TableName() string { return "transactions" }

// ChangeType menandai arah pergerakan stok.
type ChangeType string

const (
	StockIn  ChangeType = "IN"
	StockOut ChangeType = "OUT"
)

// Inventory adalah master stok barang per chat (ledger).
type Inventory struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ChatID    string    `gorm:"size:64;uniqueIndex:idx_inv_chat_item" json:"chat_id"`
	ItemName  string    `gorm:"size:128;uniqueIndex:idx_inv_chat_item" json:"item_name"`
	StockQty  float64   `gorm:"type:numeric(12,2)" json:"stock_qty"`
	Unit      string    `gorm:"size:32" json:"unit"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Inventory) TableName() string { return "inventory" }

// StockLog mencatat riwayat perubahan stok (audit trail).
type StockLog struct {
	ID          int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	InventoryID int64      `gorm:"index" json:"inventory_id"`
	ChangeType  ChangeType `gorm:"size:16" json:"change_type"`
	Quantity    float64    `gorm:"type:numeric(12,2)" json:"quantity"`
	Notes       string     `gorm:"type:text" json:"notes"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (StockLog) TableName() string { return "stock_logs" }

// Extraction adalah contract JSON yang dikembalikan oleh LLM.
// Sesuai RFC §5.1 dan §6.1.
type Extraction struct {
	Type        ExtractionType `json:"type"`
	Category    string         `json:"category"`
	ItemName    string         `json:"item_name"`
	Quantity    float64        `json:"quantity"`
	Unit        string         `json:"unit"`
	Amount      float64        `json:"amount"`
	AffectsStock bool          `json:"affects_stock"`
	Notes       string         `json:"notes"`
}

type ExtractionType string

const (
	ExtractionIncome      ExtractionType = "INCOME"
	ExtractionExpense     ExtractionType = "EXPENSE"
	ExtractionConsumption ExtractionType = "CONSUMPTION"
	ExtractionNone        ExtractionType = "NONE"
)

// Kategori transaksi (selaras dengan daftar di prompt LLM).
const (
	// CategoryOpeningBalance menandai baris INCOME yang merupakan saldo awal /
	// modal awal periode. Di laporan, kategori ini ditampilkan sebagai baris
	// terpisah ("Saldo awal") dan TIDAK mengembungkan total "Pemasukan", namun
	// tetap dihitung sebagai bagian dari Selisih (net running balance).
	CategoryOpeningBalance = "SALDO_AWAL"
)

// Normalise menyesuaikan default sesuai RFC §6.1.
func (e *Extraction) Normalise() {
	if e.Quantity == 0 {
		e.Quantity = 1
	}
	if e.Unit == "" {
		e.Unit = "pcs"
	}
	if e.Type == ExtractionConsumption {
		e.Amount = 0
	}
	// Hanya EXPENSE yang berpotensi menambah stok.
	if e.Type != ExtractionExpense {
		e.AffectsStock = false
	}
}
