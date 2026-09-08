# 🤖 LLM Architecture FAQ & Notes

> Back to [README](../README.md)

## FAQ

**Q: How does the system handle typos?**
A: The LLM intent classifier is trained to handle common typos and variations. Examples like `"persedian kecap"` (typo for "persediaan") are correctly classified.

**Q: Can I add custom query patterns?**
A: Yes! Add the pattern + a one-line example in `intentSystemPrompt` (internal/service/orchestrator/prompt.go). No code changes needed.

**Q: What happens if the LLM API is down?**
A: The system has built-in retry logic with exponential backoff. Configure `WORKER_MAX_RETRIES` and monitor logs for LLM errors.

**Q: How accurate is the intent classification?**
A: The system handles common typos and variations via LLM classification. Examples like `"persedian kecap"` (typo for "persediaan") are correctly classified. Accuracy depends on the LLM model used — see [model recommendations](./LLM.md).

**Q: Can I switch to a different LLM model?**
A: Yes! Update `LLM_MODEL` in your `.env` file. The prompts are designed to work with various instruction-following models.

**Q: How do I monitor a specific message?**
A: Grab the Task ID (from `/dev/message` response, `X-Task-ID` header, or the first log line) and grep the logs:
```bash
docker logs <container> 2>&1 | grep <task_id>
```
You'll see every step (intent → agent → persist → reply) with cost and duration, ending in a `task selesai` summary.

**Q: How does the system decide whether a purchase adds stock?**
A: Via the `affects_stock` flag on the goods master — not the LLM. `tambah barang` sets it from an explicit "non stok" mention or a service/fuel keyword heuristic (listrik/bensin/parkir → non-stock; gas LPG stays stocked), and you can flip it anytime with `set stok [barang] ya|tidak`. Purchases of stock-managed items update inventory; non-stock items are recorded as finance-only transactions.

**Q: How does the consumption module handle different units?**
A: Units come exclusively from the goods master. Register a factor once (`set 1 galon 15lt`) and `pakai galon air 3lt` converts to 0.2 galon via `ConvertUsage()`. Without a factor, the item simply lives in its stock unit (galon → galon, factor 1). Unknown units are rejected with a hint listing the accepted units.

**Q: Can I track multiple consumption cycles for the same item?**
A: Yes! The system supports multi-batch tracking with auto-generated batch numbers (e.g., "AUG-12-135918"). Each batch is tracked independently with its own consumption analytics.

## ⚠️ Notes

- **AutoMigrate** only adds new tables/columns — it does not drop or rename. For breaking schema changes (drop/rename), run SQL manually against postgres.
- **Group mention required.** The bot ignores group messages that don't @-mention it (anti-spam). The `@<bot_jid>` token is automatically stripped from the body before processing.
- **Privacy.** The sender's phone number (`sender_phone`) is recorded for audit; restrict its exposure in monitoring logs.
- **LLM Rate Limits.** Z.AI returns HTTP 429 on concurrency/limit exceed — the worker retries with exponential backoff (2s→4s→8s). Coding Plan keys have low concurrency limits; keep `WORKER_CONCURRENCY` modest (1-4).
- **Prompt Updates.** Changes to `intentSystemPrompt` (orchestrator) or `transactionSystemPrompt` (transaction) affect all subsequent classifications. Test via `/dev/message` before deploying.
- **Model Dependencies.** The system is designed to work with various LLM models, but prompt effectiveness may vary. Test with your chosen model.
