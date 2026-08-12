package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config menyimpan seluruh konfigurasi aplikasi yang dibaca dari environment.
type Config struct {
	App        AppConfig
	DB         DBConfig
	WAHA       WAHAConfig
	LLM        LLMConfig
	Worker     WorkerConfig
	WahaSender WahaSenderConfig
}

type AppConfig struct {
	Env  string
	Port string
}

type DBConfig struct {
	DSN string // PostgreSQL connection string
}

type WAHAConfig struct {
	BaseURL      string
	Session      string
	APIKey       string
	WebhookToken string
}

type LLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

type WorkerConfig struct {
	Concurrency int
	QueueSize   int
	MaxRetries  int
}

type WahaSenderConfig struct {
	QueueSize int
	MinDelay  int // milliseconds
	MaxDelay  int // milliseconds
}

// Load memuat konfigurasi dari file .env (jika ada) lalu environment variable.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		App: AppConfig{
			Env:  env("APP_ENV", "development"),
			Port: env("APP_PORT", "8080"),
		},
		DB: DBConfig{
			DSN: env("DB_DSN", "host=localhost user=postgres password=postgres dbname=smart_ledger port=5432 sslmode=disable timezone=Asia/Jakarta"),
		},
		WAHA: WAHAConfig{
			BaseURL:      env("WAHA_BASE_URL", "http://localhost:3000"),
			Session:      env("WAHA_SESSION", "default"),
			APIKey:       env("WAHA_API_KEY", ""),
			WebhookToken: env("WAHA_WEBHOOK_TOKEN", ""),
		},
		LLM: LLMConfig{
			BaseURL: env("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
			APIKey:  env("OPENROUTER_API_KEY", ""),
			Model:   env("OPENROUTER_MODEL", "deepseek/deepseek-chat"),
		},
		Worker: WorkerConfig{
			Concurrency: envInt("WORKER_CONCURRENCY", 4), // LLM workers tetap concurrent
			QueueSize:   envInt("WORKER_QUEUE_SIZE", 256),
			MaxRetries:  envInt("WORKER_MAX_RETRIES", 3),
		},
		WahaSender: WahaSenderConfig{
			QueueSize:  envInt("WAHA_SENDER_QUEUE_SIZE", 100),
			MinDelay:   envInt("WAHA_SENDER_MIN_DELAY_MS", 2000),  // 2 seconds
			MaxDelay:   envInt("WAHA_SENDER_MAX_DELAY_MS", 5000),  // 5 seconds
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.LLM.APIKey == "" {
		return fmt.Errorf("OPENROUTER_API_KEY wajib diisi")
	}
	return nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
