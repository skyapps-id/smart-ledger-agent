// Package llm berisi klien ke Z.AI GLM (OpenAI-compatible API) untuk
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

// Usage merepresentasikan token usage dari LLM response.
type Usage struct {
	PromptTokens         int     `json:"prompt_tokens"`
	CompletionTokens     int     `json:"completion_tokens"`
	TotalTokens          int     `json:"total_tokens"`
	PromptCacheHitTokens int     `json:"prompt_cache_hit_tokens,omitempty"`
	CostUSD              float64 `json:"cost_usd"`
}

// Extractor abstraksi klien ekstraksi LLM. System prompt dibawa caller
// (agent pemiliknya), bukan di-hardcode di transport layer.
type Extractor interface {
	Extract(ctx context.Context, systemPrompt, rawText, inventoryContext, sessionID string) (domain.Extraction, Usage, error)
}

// IntentExtractor abstraksi untuk intent classification menggunakan LLM.
type IntentExtractor interface {
	ClassifyIntent(ctx context.Context, systemPrompt, rawText, sessionID string) (domain.ServiceAction, Usage, error)
}

// ConversionReasoning hasil penalaran konversi satuan oleh LLM.
type ConversionReasoning struct {
	Action      string  `json:"action"`       // "convert" | "ask" | "reject"
	ContentQty  float64 `json:"content_qty"`  // isi per kemasan (hanya bila eksplisit)
	ContentUnit string  `json:"content_unit"` // lt/ml/gr/kg/pcs
	Question    string  `json:"question"`     // pertanyaan natural untuk user (action=ask)
}

// ConversionReasoner abstraksi penalaran konversi satuan kemasan via LLM:
// dipakai hanya di jalur ambigu (faktor tidak diketahui kode) — kode tetap
// memegang matematika & penyimpanan.
type ConversionReasoner interface {
	ReasonConversion(ctx context.Context, systemPrompt, rawText, sessionID string) (ConversionReasoning, Usage, error)
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

// NewConversionReasoner membuat klien OpenRouter untuk penalaran konversi.
func NewConversionReasoner(cfg config.LLMConfig) ConversionReasoner {
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

// tokenUsage adalah struktur usage dari response API (OpenAI-compatible).
type tokenUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	TotalTokens          int `json:"total_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens,omitempty"`
	PromptTokensDetails  *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
}

// cacheHitTokens mengambil jumlah token cache hit, mendukung format
// DeepSeek (prompt_cache_hit_tokens) maupun Z.AI/OpenAI (prompt_tokens_details.cached_tokens).
func cacheHitTokens(u *tokenUsage) int {
	if u.PromptCacheHitTokens > 0 {
		return u.PromptCacheHitTokens
	}
	if u.PromptTokensDetails != nil {
		return u.PromptTokensDetails.CachedTokens
	}
	return 0
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage *tokenUsage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// calculateCost menghitung biaya LLM berdasarkan token usage dan harga GLM-5.3-Flash.
// GLM-5.3-Flash pricing (per 1M tokens):
// - Input: $0.075 USD (promo 50%, list price $0.15)
// - Cache hit: $0.015 USD
// - Output: $0.25 USD (promo 50%, list price $0.50)
func calculateCost(promptTokens, completionTokens, cachedTokens int) float64 {
	const (
		inputPricePerMillion  = 0.075 // $0.075 per 1M input tokens
		cachePricePerMillion  = 0.015 // $0.015 per 1M cache hit tokens
		outputPricePerMillion = 0.25  // $0.25 per 1M output tokens
	)

	// Calculate input cost (excluding cache hits)
	regularInputTokens := promptTokens - cachedTokens
	if regularInputTokens < 0 {
		regularInputTokens = 0
	}
	inputCost := float64(regularInputTokens) / 1_000_000 * inputPricePerMillion

	// Calculate cache hit cost (80% discount)
	cacheCost := float64(cachedTokens) / 1_000_000 * cachePricePerMillion

	// Calculate output cost
	outputCost := float64(completionTokens) / 1_000_000 * outputPricePerMillion

	totalCost := inputCost + cacheCost + outputCost
	return totalCost
}

// Extract mengirim teks ke LLM dan mengembalikan entitas terstruktur.
// systemPrompt adalah prompt milik agent pemanggil; inventoryContext adalah
// snapshot inventory chat (hasil BuildInventoryPrompt) yang di-inject sebagai
// konteks tambahan ke system prompt.
func (c *openRouterClient) Extract(ctx context.Context, systemPrompt, rawText, inventoryContext, sessionID string) (domain.Extraction, Usage, error) {
	if inventoryContext != "" {
		systemPrompt += inventoryContext
	}

	body := c.buildChatRequest([]chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: BuildUserPrompt(rawText)},
	}, sessionID)

	payload, err := json.Marshal(body)
	if err != nil {
		return domain.Extraction{}, Usage{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return domain.Extraction{}, Usage{}, fmt.Errorf("buat request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domain.Extraction{}, Usage{}, &RequestError{Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.Extraction{}, Usage{}, fmt.Errorf("baca response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return domain.Extraction{}, Usage{}, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return domain.Extraction{}, Usage{}, fmt.Errorf("llm status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var chat chatResponse
	if err := json.Unmarshal(raw, &chat); err != nil {
		return domain.Extraction{}, Usage{}, fmt.Errorf("decode response: %w", err)
	}
	if chat.Error != nil {
		return domain.Extraction{}, Usage{}, errors.New(chat.Error.Message)
	}
	if len(chat.Choices) == 0 {
		return domain.Extraction{}, Usage{}, errors.New("respons LLM kosong")
	}

	extraction, err := parseContent(chat.Choices[0].Message.Content)
	if err != nil {
		return domain.Extraction{}, Usage{}, fmt.Errorf("parsing JSON LLM: %w", err)
	}
	extraction.Normalise()

	// Build usage info
	usage := Usage{}
	if chat.Usage != nil {
		usage.PromptTokens = chat.Usage.PromptTokens
		usage.CompletionTokens = chat.Usage.CompletionTokens
		usage.TotalTokens = chat.Usage.TotalTokens
		usage.PromptCacheHitTokens = cacheHitTokens(chat.Usage)
		usage.CostUSD = calculateCost(usage.PromptTokens, usage.CompletionTokens, usage.PromptCacheHitTokens)
	}

	return extraction, usage, nil
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
// systemPrompt adalah prompt milik orchestrator pemanggil.
func (c *openRouterClient) ClassifyIntent(ctx context.Context, systemPrompt, rawText, sessionID string) (domain.ServiceAction, Usage, error) {
	body := c.buildChatRequest([]chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Klasifikasikan pesan ini: " + strings.TrimSpace(rawText)},
	}, sessionID)

	payload, err := json.Marshal(body)
	if err != nil {
		return domain.ServiceAction{}, Usage{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return domain.ServiceAction{}, Usage{}, fmt.Errorf("buat request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domain.ServiceAction{}, Usage{}, &RequestError{Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.ServiceAction{}, Usage{}, fmt.Errorf("baca response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return domain.ServiceAction{}, Usage{}, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return domain.ServiceAction{}, Usage{}, fmt.Errorf("llm status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var chat chatResponse
	if err := json.Unmarshal(raw, &chat); err != nil {
		return domain.ServiceAction{}, Usage{}, fmt.Errorf("decode response: %w", err)
	}
	if chat.Error != nil {
		return domain.ServiceAction{}, Usage{}, errors.New(chat.Error.Message)
	}
	if len(chat.Choices) == 0 {
		return domain.ServiceAction{}, Usage{}, errors.New("respons LLM kosong")
	}

	action, err := parseIntentContent(chat.Choices[0].Message.Content)
	if err != nil {
		return domain.ServiceAction{}, Usage{}, fmt.Errorf("parsing JSON LLM: %w", err)
	}

	// Build usage info
	usage := Usage{}
	if chat.Usage != nil {
		usage.PromptTokens = chat.Usage.PromptTokens
		usage.CompletionTokens = chat.Usage.CompletionTokens
		usage.TotalTokens = chat.Usage.TotalTokens
		usage.PromptCacheHitTokens = cacheHitTokens(chat.Usage)
		usage.CostUSD = calculateCost(usage.PromptTokens, usage.CompletionTokens, usage.PromptCacheHitTokens)
	}

	// Log raw response untuk debugging param extraction
	log.Printf("[LLM INTENT] raw=%s action=%s params=%v", truncate(chat.Choices[0].Message.Content, 300), action.Action, action.Params)

	return action, usage, nil
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

// ReasonConversion meminta LLM menalar konversi satuan dari konteks yang
// dibawa rawText (disusun caller: pesan user, nama barang, satuan stok,
// jumlah pemakaian). Hanya dipanggil di jalur ambigu.
func (c *openRouterClient) ReasonConversion(ctx context.Context, systemPrompt, rawText, sessionID string) (ConversionReasoning, Usage, error) {
	content, usage, err := c.doChat(ctx, systemPrompt, rawText, sessionID)
	if err != nil {
		return ConversionReasoning{}, usage, err
	}

	clean := extractJSON(content)
	var r ConversionReasoning
	if err := json.Unmarshal([]byte(clean), &r); err != nil {
		return ConversionReasoning{}, usage, fmt.Errorf("parsing JSON LLM: %w", err)
	}
	return r, usage, nil
}

// doChat menjalankan chat completion sederhana (system+user) dan
// mengembalikan konten jawaban beserta info usage-nya.
func (c *openRouterClient) doChat(ctx context.Context, systemPrompt, rawText, sessionID string) (string, Usage, error) {
	body := c.buildChatRequest([]chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: strings.TrimSpace(rawText)},
	}, sessionID)

	payload, err := json.Marshal(body)
	if err != nil {
		return "", Usage{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, fmt.Errorf("buat request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", Usage{}, &RequestError{Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", Usage{}, fmt.Errorf("baca response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", Usage{}, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, fmt.Errorf("llm status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var chat chatResponse
	if err := json.Unmarshal(raw, &chat); err != nil {
		return "", Usage{}, fmt.Errorf("decode response: %w", err)
	}
	if chat.Error != nil {
		return "", Usage{}, errors.New(chat.Error.Message)
	}
	if len(chat.Choices) == 0 {
		return "", Usage{}, errors.New("respons LLM kosong")
	}

	usage := Usage{}
	if chat.Usage != nil {
		usage.PromptTokens = chat.Usage.PromptTokens
		usage.CompletionTokens = chat.Usage.CompletionTokens
		usage.TotalTokens = chat.Usage.TotalTokens
		usage.PromptCacheHitTokens = cacheHitTokens(chat.Usage)
		usage.CostUSD = calculateCost(usage.PromptTokens, usage.CompletionTokens, usage.PromptCacheHitTokens)
	}
	return chat.Choices[0].Message.Content, usage, nil
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
