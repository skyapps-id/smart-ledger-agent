// Package llm berisi klien ke OpenRouter (model DeepSeek) untuk
// ekstraksi entitas teks bahasa alami menjadi domain.Extraction.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"smart-ledger-agent/internal/config"
	"smart-ledger-agent/internal/domain"
)

// Extractor abstraksi klien ekstraksi LLM.
type Extractor interface {
	Extract(ctx context.Context, rawText string, inventoryContext string, sessionID string) (domain.Extraction, error)
}

// IntentExtractor abstraksi untuk intent classification menggunakan LLM.
type IntentExtractor interface {
	ClassifyIntent(ctx context.Context, rawText string, sessionID string) (domain.ServiceAction, error)
}

// New membuat klien OpenRouter dengan HTTP client default.
func New(cfg config.LLMConfig) Extractor {
	return &openRouterClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewIntentExtractor membuat klien OpenRouter untuk intent classification.
func NewIntentExtractor(cfg config.LLMConfig) IntentExtractor {
	return &openRouterClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type openRouterClient struct {
	cfg        config.LLMConfig
	httpClient *http.Client
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
	SessionID      string        `json:"session_id,omitempty"`
}

// shouldSendSessionID mengecek apakah base URL adalah OpenRouter.
// session_id hanya berguna untuk sticky routing di OpenRouter.
func (c *openRouterClient) shouldSendSessionID() bool {
	return strings.Contains(c.cfg.BaseURL, "openrouter.ai")
}

// buildChatRequest membuat chatRequest dengan session_id hanya untuk OpenRouter.
func (c *openRouterClient) buildChatRequest(messages []chatMessage, sessionID string) chatRequest {
	req := chatRequest{
		Model:          c.cfg.Model,
		Messages:       messages,
		Temperature:    0,
		ResponseFormat: &respFormat{Type: "json_object"},
	}
	if c.shouldSendSessionID() && sessionID != "" {
		req.SessionID = sessionID
	}
	return req
}

// setHeaders men-set HTTP headers. OpenRouter butuh header tambahan.
func (c *openRouterClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if c.shouldSendSessionID() {
		req.Header.Set("HTTP-Referer", "https://github.com/smart-ledger-agent")
		req.Header.Set("X-Title", "smart-ledger-agent")
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type respFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Extract mengirim teks ke LLM dan mengembalikan entitas terstruktur.
// inventoryContext adalah snapshot inventory chat (hasil BuildInventoryPrompt)
// yang di-inject sebagai konteks tambahan ke system prompt.
func (c *openRouterClient) Extract(ctx context.Context, rawText string, inventoryContext string, sessionID string) (domain.Extraction, error) {
	systemPrompt := SystemPrompt
	if inventoryContext != "" {
		systemPrompt += inventoryContext
	}

	body := c.buildChatRequest([]chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: BuildUserPrompt(rawText)},
	}, sessionID)

	payload, err := json.Marshal(body)
	if err != nil {
		return domain.Extraction{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return domain.Extraction{}, fmt.Errorf("buat request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domain.Extraction{}, &RequestError{Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.Extraction{}, fmt.Errorf("baca response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return domain.Extraction{}, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Extraction{}, fmt.Errorf("openrouter status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var chat chatResponse
	if err := json.Unmarshal(raw, &chat); err != nil {
		return domain.Extraction{}, fmt.Errorf("decode response: %w", err)
	}
	if chat.Error != nil {
		return domain.Extraction{}, errors.New(chat.Error.Message)
	}
	if len(chat.Choices) == 0 {
		return domain.Extraction{}, errors.New("respons LLM kosong")
	}

	extraction, err := parseContent(chat.Choices[0].Message.Content)
	if err != nil {
		return domain.Extraction{}, fmt.Errorf("parsing JSON LLM: %w", err)
	}
	extraction.Normalise()
	return extraction, nil
}

// parseContent membersihkan kemungkinan markdown lalu decode JSON.
func parseContent(content string) (domain.Extraction, error) {
	clean := extractJSON(content)
	var e domain.Extraction
	if err := json.Unmarshal([]byte(clean), &e); err != nil {
		return domain.Extraction{}, err
	}
	return e, nil
}

// ClassifyIntent mengklasifikasikan intent pesan pengguna menggunakan LLM.
func (c *openRouterClient) ClassifyIntent(ctx context.Context, rawText string, sessionID string) (domain.ServiceAction, error) {
	body := c.buildChatRequest([]chatMessage{
		{Role: "system", Content: SystemPromptIntent},
		{Role: "user", Content: "Klasifikasikan pesan ini: " + strings.TrimSpace(rawText)},
	}, sessionID)

	payload, err := json.Marshal(body)
	if err != nil {
		return domain.ServiceAction{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return domain.ServiceAction{}, fmt.Errorf("buat request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domain.ServiceAction{}, &RequestError{Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.ServiceAction{}, fmt.Errorf("baca response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return domain.ServiceAction{}, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return domain.ServiceAction{}, fmt.Errorf("openrouter status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var chat chatResponse
	if err := json.Unmarshal(raw, &chat); err != nil {
		return domain.ServiceAction{}, fmt.Errorf("decode response: %w", err)
	}
	if chat.Error != nil {
		return domain.ServiceAction{}, errors.New(chat.Error.Message)
	}
	if len(chat.Choices) == 0 {
		return domain.ServiceAction{}, errors.New("respons LLM kosong")
	}

	action, err := parseIntentContent(chat.Choices[0].Message.Content)
	if err != nil {
		return domain.ServiceAction{}, fmt.Errorf("parsing JSON LLM: %w", err)
	}

	// Log raw response untuk debugging param extraction
	log.Printf("[LLM INTENT] raw=%s action=%s params=%v", truncate(chat.Choices[0].Message.Content, 300), action.Action, action.Params)

	return action, nil
}

// parseIntentContent membersihkan kemungkinan markdown lalu decode JSON untuk ServiceAction.
func parseIntentContent(content string) (domain.ServiceAction, error) {
	clean := extractJSON(content)
	var a domain.ServiceAction
	if err := json.Unmarshal([]byte(clean), &a); err != nil {
		return domain.ServiceAction{}, err
	}
	return a, nil
}

// extractJSON mengambil substring JSON pertama (antara { dan } terakhir).
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
