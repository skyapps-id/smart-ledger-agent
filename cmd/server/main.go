// Command server adalah entrypoint aplikasi smart-ledger-agent.
// Menggelarkan seluruh dependency (DI) dan menjalankan Echo server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"smart-ledger-agent/internal/config"
	"smart-ledger-agent/internal/database"
	"smart-ledger-agent/internal/handler"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/router"
	"smart-ledger-agent/internal/service"
	"smart-ledger-agent/internal/waha"
	"smart-ledger-agent/internal/worker"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("gagal memuat konfigurasi", "err", err)
		os.Exit(1)
	}

	// ── Database ──
	db, err := database.New(cfg.DB)
	if err != nil {
		logger.Error("gagal inisialisasi database", "err", err)
		os.Exit(1)
	}

	// ── Repositories ──
	chatRepo := repository.NewChatRepository(db)
	txnRepo := repository.NewTransactionRepository(db)
	invRepo := repository.NewInventoryRepository(db)
	logRepo := repository.NewStockLogRepository(db)

	// ── External clients ──
	extractor := llm.New(cfg.LLM)
	sender := waha.New(cfg.WAHA)

	// ── Service ──
	agent := service.NewAgent(db, chatRepo, txnRepo, invRepo, logRepo, extractor, sender, logger)

	// ── Worker pool ──
	pool := worker.New(
		worker.Config{
			Concurrency: cfg.Worker.Concurrency,
			QueueSize:   cfg.Worker.QueueSize,
			MaxRetries:  cfg.Worker.MaxRetries,
		},
		agent,
		logger,
	)

	// ── HTTP (Echo) ──
	webhook := handler.NewWebhook(pool, cfg.WAHA.WebhookToken, logger)
	health := handler.NewHealth()
	e := router.New(webhook, health)

	addr := ":" + cfg.App.Port

	go func() {
		logger.Info("server mendengarkan", "addr", addr, "env", cfg.App.Env)
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			logger.Error("server berhenti", "err", err)
			os.Exit(1)
		}
	}()

	// Tunggu sinyal interrupt/terminate.
	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-shutdownCtx.Done()
	logger.Info("sinyal shutdown diterima, menghentikan server...")

	// Graceful shutdown berurutan: HTTP -> Worker.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown HTTP gagal", "err", err)
	}
	pool.Shutdown(ctx)

	logger.Info("aplikasi berhenti dengan bersih")
}
