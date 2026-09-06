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
	Name        string    `gorm:"size:128" json:"name"` // label opsional, mis. "project bangunan 1"; di-set via `init <nama>`
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
	// GoodsID = relasi ke master goods; ItemName tetap disimpan sebagai
	// snapshot display (dipakai grouping laporan) — relasi tetap via id.
	GoodsID         int64      `gorm:"index" json:"goods_id"`
	ItemName        string     `gorm:"size:128" json:"item_name"`
	Amount          float64    `gorm:"type:numeric(15,2)" json:"amount"`
	RawPayload      string     `gorm:"type:text" json:"raw_payload"`
	TransactionDate time.Time  `gorm:"type:date" json:"transaction_date"`        // tanggal transaksi (bisa beda dari created_at)
	ConsumptionDate *time.Time `gorm:"type:date" json:"consumption_date"`        // tanggal barang habis (nullable)
	TotalConsumed   float64    `gorm:"type:numeric(15,2)" json:"total_consumed"` // jumlah yang benar-benar habis dipakai
	CreatedAt       time.Time  `json:"created_at"`
}

func (Transaction) TableName() string { return "transactions" }

// ChangeType menandai arah pergerakan stok.
type ChangeType string

const (
	StockIn  ChangeType = "IN"
	StockOut ChangeType = "OUT"
)

// Inventory adalah master stok barang per chat (ledger), direlasikan ke
// master global goods via GoodsID — BUKAN lagi via nama barang.
type Inventory struct {
	ID       int64   `gorm:"primaryKey;autoIncrement" json:"id"`
	ChatID   string  `gorm:"size:64;uniqueIndex:idx_inv_chat_goods" json:"chat_id"`
	GoodsID  int64   `gorm:"uniqueIndex:idx_inv_chat_goods" json:"goods_id"`
	Good     *Good   `gorm:"foreignKey:GoodsID" json:"good,omitempty"`
	StockQty float64 `gorm:"type:numeric(12,2)" json:"stock_qty"`
	Unit     string  `gorm:"size:32" json:"unit"`
	// Faktor konversi (1 Unit = FactorUom ConversionUom, mis. 1 galon = 15 lt)
	// hidup di master goods — bukan lagi kolom inventory.
	UpdatedAt time.Time `json:"updated_at"`
}

func (Inventory) TableName() string { return "inventory" }

// Name mengembalikan nama barang dari relasi goods (display). Kosong bila
// relasi tidak di-preload — pastikan repo selalu preload.
func (i Inventory) Name() string {
	if i.Good == nil {
		return ""
	}
	return i.Good.Name
}

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

// Good adalah master katalog barang PER CHAT (ledger) — nama barang &
// faktor satuan terisolasi antar chat, konsisten dengan isolasi ledger.
// Uom = satuan kanonik barang (mis. "galon"); ConversionUom + FactorUom =
// faktor konversi resmi (1 Uom = FactorUom ConversionUom, mis. 19 lt).
// Master ini sumber kebenaran satuan: prompt LLM dilarang mengarang UOM /
// faktor konversi dan wajib merujuk ke sini.
type Good struct {
	ID            int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ChatID        string    `gorm:"size:64;uniqueIndex:idx_goods_chat_code;index:idx_goods_chat_name" json:"chat_id"`
	Code          string    `gorm:"size:32;uniqueIndex:idx_goods_chat_code" json:"code"`
	Name          string    `gorm:"size:128;index:idx_goods_chat_name" json:"name"`
	Uom           string    `gorm:"size:32" json:"uom"`
	ConversionUom string    `gorm:"size:32" json:"conversion_uom"`
	FactorUom     float64   `gorm:"type:numeric(12,3)" json:"factor_uom"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (Good) TableName() string { return "goods" }

// Extraction adalah contract JSON yang dikembalikan oleh LLM.
// Sesuai RFC §5.1 dan §6.1.
type Extraction struct {
	Type             ExtractionType `json:"type"`
	Category         string         `json:"category"`
	ItemName         string         `json:"item_name"`
	Quantity         float64        `json:"quantity"`
	Unit             string         `json:"unit"`
	Amount           float64        `json:"amount"`
	AffectsStock     bool           `json:"affects_stock"`
	Notes            string         `json:"notes"`
	TransactionDate  string         `json:"transaction_date,omitempty"`  // format: "YYYY-MM-DD" atau kosong untuk hari ini
	ConsumptionDate  string         `json:"consumption_date,omitempty"`  // format: "YYYY-MM-DD" untuk tanggal habis, bila ada
	TotalConsumption float64        `json:"total_consumption,omitempty"` // jumlah total yang benar-benar habis dipakai (dalam unit yang sama)
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

// Action types untuk LLM-based routing
const (
	ActionRecordTransaction = "record_transaction"
	ActionGetStock          = "get_stock"
	ActionGetReport         = "get_report"
	ActionConsumption       = "consumption"
	ActionInit              = "init"
	ActionHelp              = "help"
	ActionInfo              = "info"
	ActionNone              = "none"
)

// ServiceAction merepresentasikan aksi yang diekstrak oleh LLM intent classifier
type ServiceAction struct {
	Action string                 `json:"action"`
	Params map[string]interface{} `json:"params"`
	Data   *Extraction            `json:"data,omitempty"`
}

// ServiceParams adalah parameter yang jelas untuk setiap service action
type ServiceParams struct {
	// Untuk get_stock
	ItemFilter string `json:"item_filter,omitempty"`

	// Untuk get_report
	ReportType string `json:"report_type,omitempty"`
	Period     string `json:"period,omitempty"`

	// Untuk record_transaction
	TransactionData *Extraction `json:"transaction_data,omitempty"`

	// Untuk init
	LedgerName string `json:"ledger_name,omitempty"`
}
