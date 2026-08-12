// Package waha adalah klien WhatsApp HTTP API (WAHA) untuk
// mengirim balasan pesan teks ke pengguna (RFC §4.1 langkah 6).
package waha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"smart-ledger-agent/internal/config"
)

// Sender abstraksi pengiriman pesan WhatsApp.
type Sender interface {
	SendText(ctx context.Context, chatID, text string) error
}

// New membuat klien WAHA dengan rate limiting untuk menghindari ban WhatsApp.
func New(cfg config.WAHAConfig) Sender {
	client := &wahaClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		lastMessageTime: make(map[string]time.Time),
		mu:             sync.Mutex{},
	}
	
	// Start background cleanup goroutine for lastMessageTime map
	go client.cleanupLastMessageTime()
	
	return client
}

type wahaClient struct {
	cfg            config.WAHAConfig
	httpClient     *http.Client
	lastMessageTime map[string]time.Time // chatID -> last sent time
	mu             sync.Mutex
}

// cleanupLastMessageTime removes entries older than 1 hour to prevent memory leak
func (c *wahaClient) cleanupLastMessageTime() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	
	for range ticker.C {
		c.mu.Lock()
		cutoff := time.Now().Add(-1 * time.Hour)
		for chatID, lastTime := range c.lastMessageTime {
			if lastTime.Before(cutoff) {
				delete(c.lastMessageTime, chatID)
			}
		}
		c.mu.Unlock()
	}
}

type sendTextRequest struct {
	ChatID  string `json:"chatId"`
	Text    string `json:"text"`
	Session string `json:"session"`
}

// SendText mengirim pesan teks ke chatID (nomor WA, format: 62xxx@c.us)
// dengan human-like delay untuk menghindari ban WhatsApp.
func (c *wahaClient) SendText(ctx context.Context, chatID, text string) error {
	// Calculate delay based on last message time to this chat
	c.mu.Lock()
	lastTime, exists := c.lastMessageTime[chatID]
	c.mu.Unlock()
	
	var delay time.Duration
	if exists {
		// Add delay between 2-5 seconds for same chat to mimic human typing
		elapsed := time.Since(lastTime)
		minDelay := 2 * time.Second
		maxDelay := 5 * time.Second
		
		if elapsed < minDelay {
			// Need to wait more
			delay = minDelay - elapsed
			// Add some randomness to make it more human-like
			delay += time.Duration(rand.Intn(2000)) * time.Millisecond // 0-2s random
		} else if elapsed < maxDelay {
			// Small random delay even if we waited enough
			delay = time.Duration(rand.Intn(1000)) * time.Millisecond // 0-1s random
		}
	} else {
		// First message to this chat, minimal delay
		delay = time.Duration(rand.Intn(500)) * time.Millisecond // 0-0.5s random
	}
	
	// Apply the delay if needed
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("delay dibatalkan: %w", ctx.Err())
		}
	}
	
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
	
	// Update last message time for this chat
	c.mu.Lock()
	c.lastMessageTime[chatID] = time.Now()
	c.mu.Unlock()
	
	return nil
}
