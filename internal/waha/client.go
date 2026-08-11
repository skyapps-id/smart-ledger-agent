// Package waha adalah klien WhatsApp HTTP API (WAHA) untuk
// mengirim balasan pesan teks ke pengguna (RFC §4.1 langkah 6).
package waha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"smart-ledger-agent/internal/config"
)

// Sender abstraksi pengiriman pesan WhatsApp.
type Sender interface {
	SendText(ctx context.Context, chatID, text string) error
}

// New membuat klien WAHA.
func New(cfg config.WAHAConfig) Sender {
	return &wahaClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type wahaClient struct {
	cfg        config.WAHAConfig
	httpClient *http.Client
}

type sendTextRequest struct {
	ChatID string `json:"chatId"`
	Text   string `json:"text"`
	Session string `json:"session"`
}

// SendText mengirim pesan teks ke chatID (nomor WA, format: 62xxx@c.us).
func (c *wahaClient) SendText(ctx context.Context, chatID, text string) error {
	payload := sendTextRequest{
		ChatID:  chatID,
		Text:    text,
		Session: c.cfg.Session,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload waha: %w", err)
	}

	endpoint, err := url.JoinPath(c.cfg.BaseURL, "/api/sendText")
	if err != nil {
		return fmt.Errorf("bangun url waha: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("buat request waha: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("X-Api-Key", c.cfg.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("kirim request waha: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("waha sendText status %d", resp.StatusCode)
	}
	return nil
}
