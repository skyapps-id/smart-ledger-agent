package database

import (
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"smart-ledger-agent/internal/config"
	"smart-ledger-agent/internal/domain"
)

// New membuka koneksi PostgreSQL sesuai DSN yang dikonfigurasi
// dan menjalankan auto-migrate untuk seluruh model domain.
func New(cfg config.DBConfig) (*gorm.DB, error) {
	db, err := open(cfg)
	if err != nil {
		return nil, fmt.Errorf("buka database: %w", err)
	}

	if err := autoMigrate(db); err != nil {
		return nil, fmt.Errorf("migrasi database: %w", err)
	}
	return db, nil
}

func open(cfg config.DBConfig) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
}

func autoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&domain.Chat{},
		&domain.Transaction{},
		&domain.Inventory{},
		&domain.StockLog{},
		&domain.Good{},
		&domain.ConsumptionCycle{},
	)
}
