// Package router menyusun rute dan middleware Echo.
package router

import (
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"smart-ledger-agent/internal/handler"
)

// New membuat instance *echo.Echo terkonfigurasi penuh.
// dev boleh nil: endpoint test hanya diregistrasi saat dev mode aktif.
func New(webhook *handler.WebhookHandler, health *handler.HealthHandler, dev *handler.DevHandler) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Timeout TIDAK dipasang global: /dev/message harus boleh menunggu
	// pipeline LLM (klasifikasi + ekstraksi bisa ~20-40 detik).
	e.Use(echomw.Recover())
	e.Use(echomw.RequestID())
	e.Use(echomw.BodyLimit("1M"))

	// Rute standar: timeout 10s (webhook WAHA wajib balas cepat < 50ms,
	// health check trivial).
	std := e.Group("", echomw.TimeoutWithConfig(echomw.TimeoutConfig{
		Timeout: 10 * time.Second,
	}))

	// Health checks.
	healthGroup := std.Group("/health")
	healthGroup.GET("/live", health.Live)
	healthGroup.GET("/ready", health.Ready)

	// WAHA webhook.
	std.POST("/webhook", webhook.Handle)

	// Endpoint test (tanpa WAHA) — hanya di dev mode.
	// Timeout 60s harus lebih besar dari replyTimeout (45s) di handler/dev.go
	// supaya pembatalan selalu terjadi dari sisi handler, bukan middleware.
	if dev != nil {
		devGroup := e.Group("", echomw.TimeoutWithConfig(echomw.TimeoutConfig{
			Timeout: 60 * time.Second,
		}))
		devGroup.POST("/dev/message", dev.Handle)
	}

	return e
}
