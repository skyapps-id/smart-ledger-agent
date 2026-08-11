package handler

import (
	"net/http"
	"runtime"
	"time"

	"github.com/labstack/echo/v4"
)

// HealthHandler menyediakan endpoint readiness/liveness.
type HealthHandler struct {
	startTime time.Time
}

// NewHealth membuat handler health.
func NewHealth() *HealthHandler {
	return &HealthHandler{startTime: time.Now()}
}

// Live menangani GET /health/live.
func (h *HealthHandler) Live(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{"status": "alive"})
}

// Ready menangani GET /health/ready.
func (h *HealthHandler) Ready(c echo.Context) error {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return c.JSON(http.StatusOK, echo.Map{
		"status":   "ready",
		"uptime_s": int(time.Since(h.startTime).Seconds()),
		"goroutines": runtime.NumGoroutine(),
		"mem_alloc_kb": m.Alloc / 1024,
	})
}
