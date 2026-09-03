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

	"github.com/patrickmn/go-cache"

	"smart-ledger-agent/internal/config"
	"smart-ledger-agent/internal/database"
	"smart-ledger-agent/internal/handler"
	"smart-ledger-agent/internal/llm"
	"smart-ledger-agent/internal/repository"
	"smart-ledger-agent/internal/router"
	"smart-ledger-agent/internal/sender"
	"smart-ledger-agent/internal/service/agent"
	"smart-ledger-agent/internal/service/consumption"
	"smart-ledger-agent/internal/service/orchestrator"
	"smart-ledger-agent/internal/service/report"
	"smart-ledger-agent/internal/service/stock"
	"smart-ledger-agent/internal/service/system"
	"smart-ledger-agent/internal/service/transaction"
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
	consumptionCycleRepo := repository.NewConsumptionCycleRepository(db)

	// ── External clients ──
	extractor := llm.New(cfg.LLM)
	intentExtractor := llm.NewIntentExtractor(cfg.LLM)
	wahaSender := waha.New(cfg.WAHA)

	// ── WAHA Sender Worker (Sequential dengan Rate Limiting) ──
	wahaProcessor := sender.NewWahaProcessor(wahaSender)
	wahaSenderWorker := sender.New(
		sender.Config{
			QueueSize: cfg.WahaSender.QueueSize,
			MinDelay:  time.Duration(cfg.WahaSender.MinDelay) * time.Millisecond,
			MaxDelay:  time.Duration(cfg.WahaSender.MaxDelay) * time.Millisecond,
		},
		wahaProcessor,
		logger,
	)
	wahaSenderWorker.Start()

	// Reply sender: sender asli dibungkus Capture — merekam balasan per task
	// ID (dipakai /dev/message untuk mengembalikan balasan di response HTTP)
	// sambil tetap meneruskan ke WAHA. Overhead tanpa waiter: satu map lookup.
	replySender := sender.NewCapture(wahaSenderWorker)

	// ── Sub-agents (spesialis domain) ──
	// Semua wiring DI ada di sini (composition root): orchestrator tidak
	// import package domain sama sekali, cukup menerima []agent.SubAgent.
	consumptionService := consumption.NewService(db, consumptionCycleRepo, logger)
	// invCache di-share antar agent: diisi transactionAgent (snapshot konteks
	// LLM), di-invalidate siapa pun yang mengubah stok (transaksi/pemakaian).
	invCache := cache.New(5*time.Minute, 10*time.Minute)

	// Konfirmasi pending (mis. pilihan batch bernomor): di-share antara
	// consumption agent (mendaftarkan pilihan) dan orchestrator (resolve
	// jawaban "1"/"2" tanpa LLM hop).
	pendingConfirms := agent.NewPendingConfirms()

	agents := []agent.SubAgent{
		transaction.NewAgent(db, txnRepo, invRepo, logRepo, consumptionService, extractor, invCache, pendingConfirms, replySender, logger),
		stock.NewAgent(db, invRepo, replySender, logger),
		consumption.NewAgent(db, invRepo, logRepo, consumptionService, invCache, pendingConfirms, replySender, logger),
		report.NewAgent(db, txnRepo, logRepo, replySender, logger),
		system.NewAgent(db, chatRepo, txnRepo, replySender, logger),
	}
	orch := orchestrator.New(agents, chatRepo, intentExtractor, replySender, pendingConfirms, logger)

	// ── Worker pool (LLM processing, concurrent) ──
	pool := worker.New(
		worker.Config{
			Concurrency: cfg.Worker.Concurrency,
			QueueSize:   cfg.Worker.QueueSize,
			MaxRetries:  cfg.Worker.MaxRetries,
		},
		orch,
		logger,
	)

	// ── HTTP (Echo) ──
	webhook := handler.NewWebhook(pool, cfg.WAHA.WebhookToken, logger)
	health := handler.NewHealth()

	// Endpoint test tanpa WAHA — hanya terpasang saat dev mode aktif.
	var devHandler *handler.DevHandler
	if cfg.App.DevMode {
		devHandler = handler.NewDevMessage(pool, replySender, logger)
		logger.Info("dev endpoint aktif", "path", "POST /dev/message", "contoh",
			`curl -X POST localhost:`+cfg.App.Port+`/dev/message -H 'Content-Type: application/json' -d '{"chat_id":"628123456789@c.us","text":"beli kopi 15rb"}'`)
	}

	e := router.New(webhook, health, devHandler)

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

	// Graceful shutdown berurutan: HTTP -> LLM Worker -> WAHA Sender.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown HTTP gagal", "err", err)
	}
	pool.Shutdown(ctx)
	wahaSenderWorker.Shutdown(ctx)

	logger.Info("aplikasi berhenti dengan bersih")
}
