// Package domain berisi model database dan konstanta untuk seluruh aplikasi.
package domain

import (
	"time"
)

// ConsumptionCycleType jenis siklus konsumsi
type ConsumptionCycleStatus string

const (
	ConsumptionCycleActive    ConsumptionCycleStatus = "active"
	ConsumptionCycleCompleted ConsumptionCycleStatus = "completed"
)

// ConsumptionCycle mencatat siklus konsumsi barang dari pembelian sampai habis.
// Setiap siklus merepresentasikan satu periode konsumsi lengkap.
type ConsumptionCycle struct {
	ID               int64                  `gorm:"primaryKey;autoIncrement" json:"id"`
	ChatID           string                 `gorm:"size:64;index" json:"chat_id"`
	ItemName         string                 `gorm:"size:128;index" json:"item_name"`
	BatchNumber      string                 `gorm:"size:64;index" json:"batch_number,omitempty"` // untuk tracking per batch
	StartDate        time.Time              `gorm:"type:date;index" json:"start_date"`
	EndDate          *time.Time             `gorm:"type:date" json:"end_date,omitempty"`
	PurchaseQty      float64                `gorm:"type:numeric(12,2)" json:"purchase_qty"`
	PurchaseUnit     string                 `gorm:"size:32" json:"purchase_unit"`
	ConversionFactor float64                `gorm:"type:numeric(10,4)" json:"conversion_factor"` // factor ke satuan terkecil (gr/ml)
	ConsumedQty      float64                `gorm:"type:numeric(12,2)" json:"consumed_qty"`
	ConsumedUnit     string                 `gorm:"size:32" json:"consumed_unit"`
	Status           ConsumptionCycleStatus `gorm:"size:16;default:'active'" json:"status"`
	Notes            string                 `gorm:"type:text" json:"notes,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

func (ConsumptionCycle) TableName() string { return "consumption_cycles" }
