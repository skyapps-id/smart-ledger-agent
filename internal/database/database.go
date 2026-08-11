package database

import (
	"fmt"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"smart-ledger-agent/internal/config"
	"smart-ledger-agent/internal/domain"
)

// New membuka koneksi database sesuai driver yang dikonfigurasi
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
	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	}

	switch cfg.Driver {
	case "postgres":
		return gorm.Open(postgres.Open(cfg.DSN), gormCfg)
	case "sqlite":
		// Pastikan direktori file sqlite sudah ada.
		if dir := filepath.Dir(cfg.DSN); dir != "." && dir != "" {
			if err := mkdirAll(dir); err != nil {
				return nil, err
			}
		}
		return gorm.Open(sqlite.Open(cfg.DSN), gormCfg)
	default:
		return nil, fmt.Errorf("driver database tidak dikenal: %s", cfg.Driver)
	}
}

func autoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&domain.Chat{},
		&domain.Transaction{},
		&domain.Inventory{},
		&domain.StockLog{},
	)
}
