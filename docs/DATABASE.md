# 🗃️ Database Schema

> Back to [README](../README.md)

```mermaid
erDiagram
    chats ||--o{ goods : owns
    chats ||--o{ transactions : owns
    chats ||--o{ inventory : owns
    chats ||--o{ consumption_cycles : owns
    goods ||--o{ transactions : "item of"
    goods ||--o{ inventory : "stocked as"
    goods ||--o{ consumption_cycles : "consumed as"
    inventory ||--o{ stock_logs : logs

    chats {
        bigint id PK
        varchar(64) chat_id UK
        varchar(128) name "optional label"
        bool initialized
        timestamptz created_at
        timestamptz updated_at
    }
    goods {
        bigint id PK
        varchar(64) chat_id FK "per-chat ledger isolation"
        varchar(32) code UK "with chat_id, slug auto-generated"
        varchar(128) name "single source of item names"
        varchar(32) uom "canonical unit (galon, pcs...)"
        varchar(32) category "canonical category, fixed per item"
        bool affects_stock "flag barang berstok (default) vs jasa/BBM"
        varchar(32) conversion_uom "1 uom = factor_uom conversion_uom"
        numeric(12,3) factor_uom "learned/curated conversion"
        timestamptz created_at
        timestamptz updated_at
    }
    transactions {
        bigint id PK
        varchar(64) chat_id FK
        bigint goods_id FK "relation via id"
        varchar(32) sender_phone "audit sender"
        varchar(16) type "INCOME / EXPENSE"
        varchar(32) category
        varchar(128) item_name "denormalized display snapshot"
        numeric amount
        numeric quantity "snapshot qty beli"
        varchar(32) unit
        numeric unit_price "harga beli satuan (amount/qty)"
        text raw_payload
        date transaction_date "bisa beda dari created_at (kemarin, 01/08)"
        timestamptz created_at
    }
    inventory {
        bigint id PK
        varchar(64) chat_id FK "UK with goods_id"
        bigint goods_id FK "UK with chat_id"
        numeric stock_qty
        varchar(32) unit
        timestamptz updated_at
    }
    stock_logs {
        bigint id PK
        bigint inventory_id FK
        varchar(16) change_type "IN / OUT"
        numeric quantity
        text notes
        timestamptz created_at
    }
    consumption_cycles {
        bigint id PK
        varchar(64) chat_id FK
        bigint goods_id FK "relation via id"
        varchar(64) batch_number "auto-generated"
        date start_date
        date end_date "nullable"
        numeric inventory_qty "qty pengambilan stok (satuan stok)"
        varchar(32) inventory_unit "satuan stok"
        numeric conversion_factor "faktor master verbatim (mis. 15 lt per galon)"
        numeric consumed_qty
        varchar(32) consumed_unit "satuan master verbatim (mis. lt)"
        varchar(16) status "active/completed"
        text notes
        timestamptz created_at
        timestamptz updated_at
    }
```

## Goods Master (single source of truth for items)

`goods` is a **per-chat catalog** — each chat (session/ledger) owns its own goods rows, so item names and learned conversion factors are isolated between chats, consistent with ledger isolation:

- **Relation by id, not name.** `inventory`, `consumption_cycles`, and `transactions` reference `goods_id`; `transactions.item_name` is kept only as a denormalized display snapshot for reports.
- **Check-first (no auto-create).** Transactions resolve items via `agent.ResolveGoods` (exact → LIKE → original-message filter); items not in the master are REJECTED with guidance to register first (`"tambah barang [x] satuan [u]"`). Explicit registration goes through `GoodsRepository.GetOrCreateByName` (case-insensitive, slug code, unique per `chat_id + code`).
- **Canonical category.** `category` on the goods row wins over per-transaction LLM classification — once set (explicitly or seeded from the first purchase), it never drifts. `GetCategorySummary` reads it for stock overviews.
- **Stock flag on the master.** `affects_stock` lives on the goods row — physical stored goods (`galon`, `gas lpg`) add stock on purchase; services/fuel (`listrik`, `bensin`) never do. The LLM no longer decides this per transaction; `tambah barang` applies a keyword heuristic (correctable via `set stok [barang] ya|tidak`).
- **UOM conversion factors.** `uom` = canonical unit, `conversion_uom` + `factor_uom` = conversion registered explicitly by the chat's users (`set 1 galon 15lt`, or inline at `tambah barang galon satuan galon, 1 galon = 15lt`). Factors are stored on the chat's goods row, so subsequent usage in the same chat converts stably — prompts never invent conversion factors.
- **Name resolution via DB query (no context injection).** LLM-facing contracts still use `item_name` strings and the LLM extracts names verbatim; matching happens post-extraction via `agent.ResolveGoods` (exact → LIKE → original-message filter) — zero extra prompt tokens.

Tables are created automatically via GORM `AutoMigrate` on application start. Adding a new struct field → new column is added automatically (existing columns are not dropped).

> ⚠️ AutoMigrate only adds new tables/columns — it does not drop or rename. For breaking schema changes, run SQL manually against postgres.
