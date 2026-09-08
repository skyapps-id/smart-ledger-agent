# smart-ledger-agent

A WhatsApp-based assistant for tracking personal expenses and inventory. Just chat naturally — an LLM turns your messages into structured transactions in PostgreSQL.

> Status: Implemented (RFC Rev. C) — see [`RFC/RFC.md`](./RFC/RFC.md) for the full specification.

---

## ✨ Features

- **🤖 LLM-Based Intelligence.** Smart routing via LLM intent classification — handles typos, variations, and natural language automatically. No rigid patterns required.
- **Chat = Ledger.** Each chat (DM or group) is an independent ledger. Groups share one ledger among members; DMs are personal.
- **Natural Language Processing.** Type `beli kopi 15rb`, `cek stock kecap`, atau `analisa konsumsi bulan ini` → LLM understands intent and extracts structured parameters automatically.
- **📦 Goods Master (master-first).** Every item lives in a per-chat `goods` catalog — the single source of names, canonical units (uom), categories, conversion factors, and the stock flag (`affects_stock`). Transactions/inventory/consumption resolve by `goods_id`; unknown items are rejected with registration guidance (no auto-create, no hallucinated names).
- **⚖️ UOM from master, never from the LLM.** Conversion is defined once (`set 1 galon 15lt`) and applied everywhere: stock units, consumption cycles (stored verbatim — 15 lt, not 15000 ml), and usage conversion (`pakai 3lt` → 0.2 galon). No factor registered? The item simply lives in its stock unit (galon → galon, factor 1).
- **🏷️ Canonical categories.** Category is fixed on the master row — LLM classification can never drift it. `tambah barang` without a category gets a keyword-based suggestion (correctable via `set kategori`).
- **🚦 Stock flag from master.** Whether a purchase adds stock is decided once per item on the master (`affects_stock`), never per message by the LLM. Physical stored goods (`gas lpg`, `galon`) stock up; services/fuel (`listrik`, `bensin`) never do — correctable via `set stok [barang] ya|tidak`.
- **💰 Real-Time Cost Tracking.** Every WhatsApp reply displays exact LLM cost. Two LLM hops max (intent + extraction), constant prompt size — no per-message catalog injection.
- **Automatic Inventory Management.** Physical-goods expenses increase stock in the master's unit; consumption decreases it via master factors with batch tracking and daily-rate analytics.
- **Consumption Cycle Tracking.** Per-batch usage from start to finish — auto-generated batch numbers, multi-batch support, history and rate in the master's conversion unit.
- **Financial Tracking.** Income, expenses, opening balance — category comes from the master (keyword-suggested at `tambah barang`, correctable anytime via `set kategori`).
- **🔍 DB-side name resolution.** Ambiguous names (`susu` → uht/bmt) resolved by exact → LIKE → original-message filter, with numbered confirmation when needed — zero extra prompt tokens.
- **✅ Deterministic Writes.** LLM only routes intents and extracts text verbatim; every DB write is owned by deterministic code.
- **Smart Date Handling.** Various date formats supported: "kemarin", "01/08/2026", "01/08", "11-08" — LLM extracts and parses automatically, with today's date injected at runtime ([KONTEKS WAKTU]) so relative dates and short dates are never hallucinated.
- **🧭 End-to-End Task Tracing.** Every message gets a Task ID at the webhook (`X-Task-ID` response header) that correlates all logs across handler → worker → orchestrator → sub-agents → reply, with per-step cost/duration and a one-line trace summary.
- **Group Anti-Spam.** Bot only responds when @-mentioned in groups, preventing unwanted messages.
- **Async Processing.** Webhooks acknowledged in <50ms; LLM processing and database operations run in background with retry logic.

---

## 🚀 Quick Start

### Prerequisites

- Go 1.25+
- Docker + Docker Compose
- A Z.AI account (API key — or a GLM Coding Plan subscription, see [LLM Configuration](./docs/LLM.md#llm-configuration))
- An active WhatsApp number (this will become the bot)

### 1. Clone & configure env

```bash
git clone https://github.com/skyapps-id/smart-ledger-agent.git
cd smart-ledger-agent
cp .env.example .env
# Edit .env: set WAHA_API_KEY, WAHA_WEBHOOK_TOKEN, LLM_API_KEY
```

### 2. Start WAHA + PostgreSQL (via docker compose)

```bash
docker compose up -d            # waha + postgres
make db-up                      # alternative: postgres only
```

Scan the WhatsApp QR at `http://localhost:3000/dashboard` (user `admin`, password = `WAHA_DASHBOARD_PASSWORD`).

### 3. Run the app

```bash
make dev                        # go run ./cmd/server
```

Send messages to the bot number:
```
init project bangunan 1
beli semen 5 sak 250rb
ambil semen 2 sak
info
ringkasan
stok kecap
cek sisa susu di rumah          # natural language query
```

---

## ⚙️ Configuration (`.env`)

| Variable | Required | Default | Description |
| :--- | :---: | :--- | :--- |
| `APP_ENV` | – | `development` | runtime env (`development` auto-enables dev endpoint) |
| `APP_PORT` | – | `8080` | HTTP server port |
| `APP_DEV_MODE` | – | `true` di development | enable `POST /dev/message` test endpoint |
| `DB_DSN` | – | (localhost pg) | PostgreSQL connection string |
| `WAHA_BASE_URL` | – | `http://localhost:3000` | WAHA base URL |
| `WAHA_SESSION` | – | `default` | WAHA session name |
| `WAHA_API_KEY` | ✓ | – | API key for `POST /api/sendText` |
| `WAHA_WEBHOOK_TOKEN` | ✓ | – | webhook validation token |
| `WAHA_DASHBOARD_PASSWORD` | – | `admin` | WAHA dashboard password |
| `LLM_BASE_URL` | – | `https://api.z.ai/api/paas/v4` | Z.AI API base URL (Coding Plan: `https://api.z.ai/api/coding/paas/v4`) |
| `LLM_API_KEY` | ✓ | – | Z.AI API key (Coding Plan users: key from Plan Overview, NOT a regular API key) |
| `LLM_MODEL` | – | `glm-5.3-flash` | LLM model |
| `WORKER_CONCURRENCY` | – | `4` | LLM worker concurrency |
| `WORKER_QUEUE_SIZE` | – | `256` | LLM worker queue size |
| `WORKER_MAX_RETRIES` | – | `3` | max retries for LLM operations |
| `WAHA_SENDER_QUEUE_SIZE` | – | `100` | WAHA sender queue size |
| `WAHA_SENDER_MIN_DELAY_MS` | – | `100` | Min delay between sends (ms) |
| `WAHA_SENDER_MAX_DELAY_MS` | – | `1000` | Max delay between sends (ms) |

---

## 🧰 Tech Stack

| Component | Choice |
| :--- | :--- |
| Language | Go 1.25 |
| HTTP Framework | Echo v4 |
| ORM | GORM |
| Database | PostgreSQL 16 |
| WhatsApp Engine | WAHA (NOWEB) |
| LLM Provider | Z.AI (GLM) |
| LLM Model | GLM-5.3-Flash (default) |
| Intent Classification | Custom LLM-based routing |
| Consumption Tracking | Auto-generated batch numbers, conversion via goods-master factors |
| Pending Confirmations | `patrickmn/go-cache` (in-memory, TTL 5m — numbered goods/batch choices) |
| Container | Docker + Docker Compose |
| Logging | `log/slog` (stdlib) |
| Testing | Go testing + Mock objects |

---

## 📚 Documentation

| Document | Contents |
| :--- | :--- |
| [`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md) | System architecture, multi-agent orchestration, task tracing, worker tiers, message lifecycle, name resolution, token optimization, LLM routing |
| [`docs/DATABASE.md`](./docs/DATABASE.md) | ERD, schema details, goods master rules |
| [`docs/USAGE.md`](./docs/USAGE.md) | Full command list & natural language query examples |
| [`docs/DEVELOPMENT.md`](./docs/DEVELOPMENT.md) | Project structure, make commands, dev endpoint testing, prompt engineering |
| [`docs/DEPLOYMENT.md`](./docs/DEPLOYMENT.md) | Local & container deployment, production considerations |
| [`docs/LLM.md`](./docs/LLM.md) | LLM provider setup (Z.AI / DeepSeek / OpenRouter), cost tracking, benchmarks |
| [`docs/FAQ.md`](./docs/FAQ.md) | LLM architecture FAQ + operational notes |
| [`docs/ROADMAP.md`](./docs/ROADMAP.md) | Future enhancements |
| [`RFC/RFC.md`](./RFC/RFC.md) | Full architecture specification (Rev. C) |
| [`RFC/AGENTIC.md`](./RFC/AGENTIC.md) | Draft roadmap for the agentic evolution (memory, tool-calling, proactive scheduler) |

---

## ⚠️ Notes

- **Group mention required.** The bot ignores group messages that don't @-mention it (anti-spam). The `@<bot_jid>` token is automatically stripped from the body before processing.
- **Prompt Updates.** Changes to `intentSystemPrompt` (orchestrator) or `transactionSystemPrompt` (transaction) affect all subsequent classifications. Test via `/dev/message` before deploying.
- See [docs/FAQ.md](./docs/FAQ.md) for the full operational notes (AutoMigrate, privacy, rate limits, model dependencies).
