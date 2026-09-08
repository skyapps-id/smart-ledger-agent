# 🛠️ Development

> Back to [README](../README.md)

## Commands

```bash
make run            # go run ./cmd/server
make build          # build binary to bin/server
make vet            # go vet
make tidy           # go mod tidy
make test           # go test -race -cover
make clean          # remove bin/ and data/

# LLM-specific testing
make test-llm       # test LLM intent classification
make test-flow      # test full flow simulation
```

## Testing Tanpa WAHA (Dev Endpoint)

Saat `APP_ENV=development` (atau `APP_DEV_MODE=true`), tersedia endpoint untuk
menyuntik pesan langsung ke pipeline tanpa WAHA — dan **balasan bot
dikembalikan langsung di response HTTP**:

```bash
curl -s -X POST localhost:8080/dev/message \
  -H 'Content-Type: application/json' \
  -d '{"chat_id":"628123456789@c.us","text":"Beli beras 1kg 100k"}'
```

```json
{
  "status": "replied",
  "task_id": "3f9a21c0e7b84d12",
  "chat_id": "628123456789@c.us",
  "reply": { "chat_id": "...", "text": "Pengeluaran tercatat: beras x1 kg = Rp100.000 (...)" }
}
```

`status` kemungkinan: `replied` (balasan ditangkap), `timeout` (pipeline > 45
detik — pantau via log), `queued` (hanya bila capture tidak terpasang).
Pantau detail proses per langkah di log dengan `task_id` (juga di header
`X-Task-ID`):

```bash
docker logs -f <container> 2>&1 | grep <task_id>
```

Catatan: balasan untuk pesan dari `/dev/message` **tidak dikirim ke WAHA**
(hanya dikembalikan di response HTTP + log) — WAHA tidak perlu jalan saat
testing. Balasan pesan dari webhook asli tetap normal ke WAHA.

## Working with LLM Prompts

Each agent owns its prompt in its own package (`prompt.go`) — the transport layer (`internal/llm`) never hardcodes prompts:

| Prompt | File | Sent to LLM |
|--------|------|-------------|
| Intent classification | `internal/service/orchestrator/prompt.go` | every message |
| Transaction extraction | `internal/service/transaction/prompt.go` | `record_transaction` only |
| stock / consumption / report / system | `internal/service/<domain>/prompt.go` | not yet (contract-ready) |

**Adding a new intent action:**
1. Edit `internal/service/orchestrator/prompt.go` — add the action + params + a one-line example
2. Edit `internal/domain/models.go` — add action constant if needed
3. Edit `internal/service/<domain>/agent.go` — handle the action
4. Test with `go test -v ./internal/service/... -run TestYourNewFeature` or via `/dev/message`

**Consumption Module Integration:**
- Master-factor conversion: `ConvertUsage()` converts usage to the stock unit strictly from the registered master factor (same unit → as-is; conversion unit → qty/factor; anything else → guidance reply listing accepted units)
- Multi-batch support: Track multiple consumption cycles for the same item
- Smart completion: `handleConsumptionAction()` with info, list, use, complete actions
- Enhanced LLM prompts: Comprehensive consumption query patterns

**Prompt Best Practices:**
- **Be Specific**: "Extract transaction data" not "Parse the message"
- **JSON Format**: Always request structured JSON output
- **Examples**: Provide 3-5 input → output examples
- **Error Handling**: Define behavior for invalid inputs
- **Zero Context Injection**: Keep prompts constant-size — match item names post-extraction via DB queries (`ResolveGoods`), never inject the catalog
- **Unit Verbatim**: LLM copies names/units verbatim; the goods master owns all conversions

**Monitoring LLM Performance:**
```bash
# Check LLM response times and accuracy
tail -f logs/app.log | grep "LLM"

# Test specific patterns
go test -v ./internal/service/... -run TestGetStockQueryPatterns
```

## 🧱 Project Structure

```
smart-ledger-agent/
├── cmd/server/                 # entrypoint
├── internal/
│   ├── config/                 # env loader
│   ├── database/               # GORM setup + auto-migrate
│   ├── domain/                 # GORM models + constants (Chat, Good, Transaction, Inventory, StockLog, ConsumptionCycle)
│   ├── entity/                 # cross-layer business entities (IncomingMessage)
│   ├── handler/
│   │   ├── model/              # webhook parsing DTOs (WahaPayload)
│   │   ├── webhook.go          # WAHA webhook + Task ID generation
│   │   ├── dev.go              # POST /dev/message test endpoint (dev mode)
│   │   └── health.go
│   ├── llm/                    # OpenAI-compatible client + prompt builders (TimeContext)
│   ├── repository/
│   │   ├── model/              # query-result DTOs (TxnSummary, ItemBreakdown, StockMovement)
│   │   ├── chat.go
│   │   ├── goods.go             # goods master repository (GetOrCreateByName, UpdateConversion)
│   │   ├── consumption_cycle.go  # consumption cycle repository
│   │   ├── transaction.go
│   │   ├── inventory.go
│   │   ├── stock_log.go
│   │   └── report.go
│   ├── router/                 # Echo routes (per-group timeouts)
│   ├── sender/
│   │   ├── sender.go           # sequential WAHA sender worker + rate limit
│   │   └── capture.go          # per-task reply capture (dev endpoint)
│   ├── service/
│   │   ├── agent/              # SubAgent contract, ResolveGoods/ResolveInventoryItem, PendingConfirms, Task tracing, templates
│   │   ├── orchestrator/       # intent classification + dispatch (domain-agnostic)
│   │   ├── transaction/        # record_transaction agent + extraction prompt
│   │   ├── stock/              # get_stock agent
│   │   ├── consumption/        # consumption cycle service + agent
│   │   ├── goods/              # goods master agent (list/info/set_factor/set_uom)
│   │   ├── report/             # get_report agent + formatting + date parsing
│   │   └── system/             # init/help/info/none agent
│   ├── waha/                   # WhatsApp HTTP client
│   └── worker/                 # async worker pool + retry
├── RFC/RFC.md                  # full specification
├── docs/                       # documentation (this folder)
├── Dockerfile
├── docker-compose.yml          # postgres + waha + app (optional, via profile)
└── Makefile
```
