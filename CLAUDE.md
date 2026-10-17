# CLAUDE.md

WhatsApp expense & inventory ledger bot in Go. WAHA delivers messages to a webhook; an LLM classifies intent and extracts fields; deterministic Go code does every DB write (PostgreSQL via GORM). Each chat (DM `@c.us` or group `@g.us`) is its own isolated ledger.

Deep docs live in [docs/](docs/) and the spec in [RFC/RFC.md](RFC/RFC.md). Read the matching doc before touching a subsystem; update it when behavior changes.

## Commands

```bash
docker compose up -d     # postgres + waha (app runs on host)
make dev                 # go run ./cmd/server (needs .env, copy from .env.example)
make build               # bin/server
make vet                 # go vet ./...
make test                # go test ./... -race -cover
go test ./internal/service/transaction/... -run TestName -v
```

`docs/DEVELOPMENT.md` mentions `make test-llm`, `make test-flow`, `make db-up` — these targets do **not** exist in the Makefile.

Manual end-to-end test without WAHA (dev mode, `APP_ENV=development`): the reply comes back in the HTTP response and is not sent to WhatsApp.

```bash
curl -s -X POST localhost:8080/dev/message -H 'Content-Type: application/json' \
  -d '{"chat_id":"628123456789@c.us","text":"beli kopi 15rb"}'
```

## Architecture

Flow: `handler/webhook.go` (validate token, ignore group msgs without @mention, assign Task ID) → `worker/pool.go` (concurrent LLM workers + retry) → `service/orchestrator` (one LLM hop: intent classification) → a `SubAgent` → reply via `sender.Capture` → `sender` (single sequential WAHA worker, randomized delay for anti-spam).

- **Composition root is [cmd/server/main.go](cmd/server/main.go).** All DI wiring happens there. The orchestrator is domain-agnostic: it receives `[]agent.SubAgent` and builds an action → agent registry. It must never import a domain package.
- **SubAgent contract** ([internal/service/agent/agent.go](internal/service/agent/agent.go)): `Actions()`, `SystemPrompt()`, `Handle(ctx, Request)`. Every agent owns its prompt in its own `prompt.go`, even if it doesn't call the LLM yet.
- **Domain agents** under `internal/service/`: `transaction` (record_transaction, 2nd LLM hop for extraction), `stock`, `consumption`, `goods`, `report`, `system` (init/help/info/none). Action constants are in [internal/domain/models.go](internal/domain/models.go).
- **`internal/service/agent`** holds shared helpers: `SendReply`/`SendReplyWithCost`, `ResolveGoods`/`ResolveInventoryItem`, `PendingConfirms` (go-cache, TTL 5m — numbered choices answered with "1"/"2" resume without an LLM hop), `Task` tracing, templates.
- **`internal/llm`** is pure OpenAI-compatible transport (Z.AI GLM by default; DeepSeek/OpenRouter also work). It never hardcodes prompts — callers pass them in. Append `llm.TimeContext(now)` to prompts that need today's date.
- Schema is created by GORM `AutoMigrate` in [internal/database/database.go](internal/database/database.go); new models must be added there.

Adding a domain: implement `agent.SubAgent` in a new `internal/service/<domain>/` package, add action constants in `domain`, teach the orchestrator prompt about the action ([internal/service/orchestrator/prompt.go](internal/service/orchestrator/prompt.go)), append the agent in `main.go`.

## Invariants (don't break these)

- **LLM only routes and extracts verbatim.** No unit math, no conversion factors, no category decisions, no `affects_stock` decisions in prompts or from LLM output. All writes are deterministic code.
- **Goods master is the source of truth** (`domain.Good`, per chat): name, canonical `Uom`, `Category`, `ConversionUom`/`FactorUom`, `AffectsStock`. Transactions/inventory/consumption reference `goods_id`.
- **Unknown items are rejected** with registration guidance (`tambah barang ...`). Never auto-create goods from a transaction.
- **Zero context injection.** Prompts stay constant-size — never put the catalog in a prompt. Match names after extraction via `ResolveGoods` (exact → LIKE → filter by original message → numbered confirmation).
- **Ledger isolation by `ChatID`.** Every query must be scoped per chat; groups share one ledger, `SenderPhone` is only an audit trail.
- Multi-table writes go through `db.WithContext(ctx).Transaction(...)`.
- Keep the Task ID flowing through `context`; record LLM/persist steps on `agent.TaskFromContext(ctx)` so the end-of-task trace and cost stay accurate. Replies show LLM cost — use `SendReplyWithCost` when LLM calls were made.

## Conventions

- Code comments, log messages and all user-facing WhatsApp replies are in **Indonesian**; match that. Docs in `docs/` are mostly English.
- Logging: `log/slog`, use `agent.LoggerWithTask(log, ctx)` so lines carry `task=`.
- Tests: `testify` (`require`/`assert`) with in-memory SQLite (`gorm.io/driver/sqlite`, `:memory:`) + `AutoMigrate` of the needed models. LLM and sender are replaced with small hand-written mocks (see `internal/service/transaction/agent_test.go`). Tests never hit a real LLM.
- Changing the orchestrator or transaction prompt affects every message — verify with tests and `/dev/message` before committing.
- `.env`, `data/`, `sessions/` (WAHA WhatsApp session) are git-ignored and contain secrets/state — don't commit or delete them.
