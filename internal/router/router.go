// Package router menyusun rute dan middleware Echo.
package router

import (
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"smart-ledger-agent/internal/handler"
)

// New membuat instance *echo.Echo terkonfigurasi penuh.
func New(webhook *handler.WebhookHandler, health *handler.HealthHandler) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Use(echomw.Recover())
	e.Use(echomw.RequestID())
	e.Use(echomw.BodyLimit("1M"))
	e.Use(echomw.TimeoutWithConfig(echomw.TimeoutConfig{
		Timeout: 10 * time.Second,
	}))

	// Health checks.
	healthGroup := e.Group("/health")
	healthGroup.GET("/live", health.Live)
	healthGroup.GET("/ready", health.Ready)

	// WAHA webhook.
	e.POST("/webhook", webhook.Handle)

	return e
}
