# 🧰 LLM Configuration, Cost & Benchmarks

> Back to [README](../README.md)

## LLM Configuration

The system supports **Z.AI GLM API** (OpenAI-compatible), **DeepSeek**, and **OpenRouter** as LLM providers:

```env
# Option 1: Z.AI GLM API — pay as you go (recommended, cheap)
# https://docs.z.ai/api-reference/introduction
LLM_BASE_URL=https://api.z.ai/api/paas/v4
LLM_API_KEY=your_zai_api_key
LLM_MODEL=glm-5.3-flash

# Option 1b: Z.AI GLM Coding Plan (subscription)
# Endpoint WAJIB beda, dan API key HARUS dibuat di halaman
# "Individual Coding Plan > Plan Overview" (bukan API key biasa —
# key biasa akan kena error 1113 "Insufficient balance").
LLM_BASE_URL=https://api.z.ai/api/coding/paas/v4
LLM_API_KEY=your_coding_plan_key
LLM_MODEL=glm-5.3-flash

# Option 2: DeepSeek API direct (fallback)
LLM_BASE_URL=https://api.deepseek.com
LLM_API_KEY=your_deepseek_api_key
LLM_MODEL=deepseek-chat

# Option 3: Via OpenRouter (fallback)
LLM_BASE_URL=https://openrouter.ai/api/v1
LLM_API_KEY=your_openrouter_api_key
LLM_MODEL=deepseek/deepseek-chat
```

**Z.AI GLM API advantages:**
- **Very cheap**: GLM-5.3-Flash at $0.075 input / $0.25 output per 1M tokens (50% promo)
- **1M token context window** with built-in context caching
- OpenAI-compatible endpoint — no SDK change needed
- Cache hit tokens reported in `usage.prompt_tokens_details.cached_tokens`

**To switch models:**
```bash
# Z.AI GLM (recommended)
LLM_BASE_URL=https://api.z.ai/api/paas/v4
LLM_API_KEY=your_key
LLM_MODEL=glm-5.3-flash

# Restart app
make dev
```

**Note:** `session_id` is automatically sent only when using OpenRouter (for sticky routing / prompt caching). Z.AI and DeepSeek have context caching enabled by default — no configuration needed.

---

## 💰 Cost Tracking & Monitoring

Every LLM request includes real-time cost tracking and transparent reporting.

### Cost Display
```
💰 AI cost: $0.000156
```
This appears at the bottom of every WhatsApp reply, showing the total cost for that specific request.

### Cost Calculation
Cost is calculated based on GLM-5.3-Flash pricing:
- **Input tokens**: $0.075 per million (50% promo, list price $0.15)
- **Cache hits**: $0.015 per million
- **Output tokens**: $0.25 per million (50% promo, list price $0.50)

### Cost Breakdown per Request Type
| Request Type | Components | Avg Cost |
|--------------|------------|----------|
| **Intent only** (help, info) | Intent classification | $0.0001-0.0002 |
| **Transaction** (beli, transfer) | Intent + Extraction | $0.00015-0.00025 |
| **Stock query** (general) | Intent + Category summary | $0.00014-0.00024 |
| **Stock query** (specific) | Intent + Search results | $0.00012-0.00022 |

### Usage Tracking
The system tracks detailed token usage:
- `prompt_tokens`: Total input tokens
- `completion_tokens`: Output tokens  
- `prompt_cache_hit_tokens`: Cached tokens (Z.AI / DeepSeek KV cache)
- `total_tokens`: Combined total
- `cost_usd`: Calculated cost in USD

### Token Savings Through Optimization
With search optimization and category summarization:

| Optimization | Before | After | Savings |
|--------------|--------|-------|---------|
| **LLM Context** (specific queries) | ~1500-2000 tokens | ~200-500 tokens | **60-70%** |
| **WhatsApp Display** (general queries) | ~1500-2000 tokens | ~200-300 tokens | **80-90%** |
| **Overall Average** | ~1500 tokens | ~450 tokens | **~70%** |

### Cache Effectiveness
Z.AI's context cache provides automatic cost savings:
- **Cache hit rate**: ~85%+ for repeated system prompts
- **Cost discount**: 80% on cached tokens ($0.015 vs $0.075 per million)
- **No configuration needed**: Works automatically

---

## 📊 Performance Benchmarks

Based on local testing with DeepSeek Chat model:

| Operation | Average Time | Avg Tokens | Cost | Notes |
|-----------|--------------|------------|------|-------|
| Intent Classification | ~300ms | ~500 | $0.0001 | Single API call |
| Transaction Extraction (optimized) | ~500ms | ~200-500 | $0.00005-0.0001 | With search optimization |
| Stock Query (general) | ~600ms | ~200 | $0.00004 | Category summary |
| Stock Query (specific) | ~400ms | ~100 | $0.00002 | Search results |
| End-to-End Response | <2s | ~700-1200 | $0.00015-0.00025 | User query → reply |

**Token Optimization Results:**
- **LLM Context**: 60-70% savings (search vs full inventory)
- **WhatsApp Display**: 80-90% savings (category summary vs full list)
- **Overall**: ~70% average token reduction per request

**System Capacity** (with default settings):
- **Concurrency**: 4 workers (configurable)
- **Queue Size**: 256 messages (configurable)
- **Max Throughput**: ~120 messages/minute
- **Retry Logic**: 3 attempts with exponential backoff
- **Cost Tracking**: Real-time cost display in every reply

**Cost Transparency:**
Every WhatsApp reply includes real-time AI cost:
```
💰 AI cost: $0.000156
```
Cost breakdown includes intent classification + extraction (if applicable).
