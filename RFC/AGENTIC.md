# RFC: Agentic Evolution — dari Chatbot Pipeline ke Agen Tool-Using

| | |
|---|---|
| **Status** | Draft (Rev. A) — menunggu review |
| **Pengganti** | — (melengkapi [`RFC.md`](./RFC.md) Rev. C, tidak menggantikan) |
| **Baseline kode** | commit `452e0a2` (branch `refactor/sub-agents`) |
| **Tanggal** | 2026-09-04 |

---

## 1. Abstract

Sistem saat ini adalah **pipeline chatbot ber-LLM yang deterministik**: LLM hanya berperan sebagai
classifier (hop 1) dan extractor (hop 2); seluruh keputusan setelahnya adalah switch statement.
Dokumen ini merancang evolusi bertahap menuju sistem **agentic** — LLM yang memutuskan aksi
berikutnya berdasarkan hasil observasi — dengan tiga fase berurutan: (F1) memori percakapan,
(F2) tool-calling pada report agent, (F3) proaktivitas terjadwal. Setiap fase dirancang agar
biaya, latensi, dan risiko halusinasi tetap terkendali, dan dapat diimplementasikan terpisah.

## 2. Problem Statement

Keterbatasan arsitektur saat ini yang MUNGKIN diselesaikan dokumen ini:

| # | Keterbatasan | Contoh nyata |
|---|---|---|
| P1 | **Tanpa memori antar giliran.** Setiap pesan diklasifikasi dari nol. | User: "beli beras" → bot: tercatat. User: "yang 5kg itu maksudku" → `none`. |
| P2 | **Laporan tidak bisa komposisi.** `report_type` adalah pilihan tunggal; satu request = satu query. | "bandingkan pengeluaran bulan ini vs bulan lalu" tidak dapat dijawab (butuh 2 query + aritmetika). |
| P3 | **Bot pasif total.** Data rate konsumsi tersimpan tapi tidak pernah dipakai untuk memulai percakapan. | "susu bmt estimasi habis 2 hari" ada di DB, user tetap harus bertanya. |

## 3. Baseline & Prinsip Desain

### 3.1. Klasifikasi jujur baseline

Komponen yang DISALAHKAN namanya (pelajaran untuk dokumen ini): `SubAgent` saat ini adalah
**handler deterministik**, bukan agen — 4 dari 5 prompt-nya tidak pernah dikirim ke LLM.
Orchestrator adalah router. Sistem ini berada di level 1 dari spektrum:

```
Level 0: FAQ bot (pattern matching)
Level 1: Router + Extractor  ← SEKARANG (LLM pilih jalur, kode eksekusi)
Level 2: Tool-using agent    ← F2      (LLM pilih tool, baca hasil, putuskan lanjut)
Level 3: Proactive agent     ← F3      (agen memulai aksi dari trigger waktu/data)
Level 4: Planner/autonomous  ← out of scope (loop panjang, multi-goal)
```

### 3.2. Prinsip

1. **Deterministik untuk tulis, agentic untuk baca.** Jalur pencatatan (uang/stok berpindah)
   TETAP deterministik seperti saat ini. Eksperimen agentic hanya di jalur baca (laporan,
   pertanyaan) di mana salah = jawaban salah, bukan data korup.
2. **Budget eksplisit per pesan**: maksimal N tool-call, maksimal X token konteks; melebihi
   budget → fallback ke jalur lama.
3. **Semua hop tercatat di Task** (`agent.Task`) — observabilitas tidak boleh mundur.
4. **Angka hanya dari tool result.** Laporan wajib mengutip nilai hasil query; instruksi prompt
   melarang aritmetika dari ingatan.

## 4. Scope

**In scope:** F1 (memori percakapan), F2 (tool-calling report agent), F3 (scheduler proaktif),
termasuk perubahan skema, kontrak, prompt, dan acceptance test.

**Out of scope:** write-tools (LLM menulis DB langsung), planner multi-langkah (Level 4),
memory lintas chat, voice/gambar, multi-bahasa.

---

## 5. Fase 1 — Conversation Memory (Level 1.5)

### 5.1. Desain

Tabel baru (GORM AutoMigrate):

```sql
chat_messages (
  id          bigserial PK,
  chat_id     varchar(64)  FK chats, index (chat_id, id),
  role        varchar(8)            -- 'user' | 'bot'
  text        text,
  task_id     varchar(16),          -- korelasi ke task log
  created_at  timestamptz
)
```

- **Penulisan** terpusat: pesan user dicatat di `Orchestrator.Process` (setelah task dibuat);
  balasan bot dicatat di `agent.SendReply/SendReplyWithCost` (sudah titik tunggal semua balasan).
- **Pembacaan**: `ConversationStore.Recent(ctx, chatID, n)` — default **5 giliran** (± 100–150
  token), di-inject ke konteks hop ekstraksi (transaction agent) sebagai blok:

  ```
  [RIWAYAT CHAT TERAKHIR]
  user: beli beras 5kg kemarin
  bot: Pengeluaran tercatat: beras x5 kg ...
  ```

  Ditambah satu paragraf instruksi: referensi seperti "yang itu", "yang 5kg", "ganti yang 2kg"
  menunjuk entitas pada riwayat.

- **Intent prompt TIDAK diberi riwayat** di F1 — klasifikasi tetap murah dan stabil. Hanya
  ekstraksi yang butuh anafora.

### 5.2. Keputusan

| Keputusan | Pilihan | Alasan |
|---|---|---|
| Penyimpanan | PostgreSQL, bukan go-cache | Memori harus selamat restart; volume kecil (± 2 baris/pesan). |
| Retensi | Purge > 30 hari (job harian) | Privasi + tabel tetap ramping. |
| Group chat | Ikut tercatat, per chat_id (bukan per user) | Ledger memang shared per chat. |
| Kapan di-inject | Selalu (5 giliran ringkas) | Selektif = kompleks; 150 token murah dibanding bug "yang itu". |

### 5.3. Acceptance

- "beli beras" → "yang 5kg salah, ganti 2kg" menghasilkan koreksi EXPENSE beras 2kg (pesan kedua
  sendirian diklasifikasi `record_transaction` dan diekstrak dengan referensi riwayat).
- Tanpa riwayat (chat baru), perilaku identik baseline.
- Task log menampilkan langkah `memory.load`.

---

## 6. Fase 2 — Tool-Calling Report Agent (Level 2)

### 6.1. Desain

Klien LLM ditambah satu method (OpenAI-compatible `tools`):

```go
// internal/llm
type ToolSpec struct { Name, Description string; Parameters any } // JSON Schema

type ToolCall struct { Name string; Arguments map[string]any }

// Hasil satu ronde: model memilih memanggil tool ATAU memberi jawaban final.
func (c *Client) ChatWithTools(ctx, systemPrompt, userText string, history []Message,
    tools []ToolSpec) (toolCalls []ToolCall, finalText string, usage Usage, err error)
```

**Tool registry** report agent (semua READ-ONLY, terikat repository yang sudah ada):

| Tool | Sumber | Keterangan |
|---|---|---|
| `query_summary(period, report_type, from_date?, to_date?)` | `txnRepo.Summary` | income/expense/net + per kategori |
| `query_expense_by_item(period, ...)` | `txnRepo.ExpenseByItem` | rincian per barang |
| `query_stock_movements(period, item_filter?)` | `logRepo.MovementsByChat` | pemakaian stok |
| `query_inventory(item_filter?)` | `invRepo` | stok saat ini |
| `query_consumption_rate(item_name)` | cycleRepo | rate gr/hari + estimasi habis |

**Loop terbatas** di report agent:

```
maxRounds := 3
for round := 1; round <= maxRounds; round++ {
    calls, final, usage := ChatWithTools(...)
    task.AddStep("report", "tool.think", fmt.Sprint(round), usage.CostUSD, ...)
    if final != "" { kirim jawaban; selesai }
    for each call: result := eksekusi via repository (read-only)
    task.AddStep("report", "tool.call", call.Name, 0, ...)
    append hasil (dipotong 2 KB/tool) sebagai message tool → ronde berikutnya
}
fallback: jalur lama (switch report_type) + catatan "jawaban ringkas"
```

### 6.2. Keputusan

| Keputusan | Pilihan | Alasan |
|---|---|---|
| Agent pertama | report (baca-saja) | Salah = jawaban salah, bukan data korup (Prinsip 1). |
| Write-tools | TIDAK di F2 | Persistensi tetap lewat extractor deterministik. |
| maxRounds = 3 | budget keras | 3 panggilan cukup untuk "bandingkan A vs B"; latensi puncak ~3× hop. |
| Fallback | jalur lama tetap ada | Model tidak meng-emit tool-call / budget habis → perilaku baseline. |
| Jawaban angka | wajib dari tool result | Prompt eksplisit: dilarang mengarang/menghitung dari memori. |

### 6.3. Biaya (estimasi, glm-5.3-flash)

| Komponen | Token | Estimasi |
|---|---|---|
| Hop intent (tetap) | ~2.5K | ~$0.0002 |
| Tool loop: system + 5 tool spec | ~1.5K | ~$0.0001/pesan laporan |
| Ronde 1–3 (result ≤ 2 KB/ronde) | ~2–6K | ~$0.0002–0.0005 |
| **Total laporan kompleks** | | **≤ $0.001** (vs ~$0.0003 laporan sederhana) |

### 6.4. Acceptance

- "bandingkan pengeluaran bulan ini vs bulan lalu" → 2× `query_summary` + jawaban berisi angka
  kedua periode + delta (angka cocok dengan SQL manual).
- "barang apa yang paling boros bulan ini?" → `query_expense_by_item` → jawaban menunjuk item top.
- Task log: `tool.think(1) → tool.call(query_summary) → tool.think(2) → ...` lengkap dengan biaya.
- Query 1-periode biasa ("pengeluaran hari ini") tetap 1 ronde — tidak ada regresi biaya.

---

## 7. Fase 3 — Proactive Scheduler (Level 3)

### 7.1. Desain

Worker baru `internal/scheduler` — `time.Ticker` interval 1 jam, jendela kirim 08:00–20:00 WIB:

| Trigger | Sumber data | Pesan contoh |
|---|---|---|
| Estimasi habis ≤ 2 hari | `consumption_cycles` aktif + rate | "susu bmt 200g estimasi habis ~2 hari. Beli lagi? balas: beli susu bmt 200g" |
| Stok ≤ 1 | `inventory` | "stok popok sisa 1. mau catat beli?" |
| Rekap mingguan (opt-in) | `query_summary` | rekap Senin pagi |

- **Opt-in per chat**: kolom `chats.proactive_enabled` (default `false`); aktif via command
  "reminder on/off" (system agent, tanpa LLM baru).
- **Anti-spam**: dedup `chat_id + trigger_kind + tanggal` (go-cache cukup, bukan DB) + maksimal
  1 pesan proaktif per chat per hari.
- **Pengiriman**: lewat sender yang sama (WAHA); task ID prefix `cron-` agar trace tetap utuh.
- Balasan user diproses pipeline normal — tidak ada jalur baru.

### 7.2. Acceptance

- Cycle rate 100 gr/hari, sisa 150 gr, `proactive_enabled=true` → pesan terkirim sekali, tidak
  berulang di jam berikutnya; `proactive_enabled=false` → tidak ada pesan.
- Semua pesan proaktif punya `task=cron-...` di log.

---

## 8. Risiko & Mitigasi

| Risiko | Fase | Mitigasi |
|---|---|---|
| Memori menjerumus (riwayat salah dijadikan konteks transaksi baru) | F1 | Riwayat hanya konteks rujukan; instruksi eksplisit "pesan BARU yang dicatat, bukan riwayat"; acceptance test koreksi. |
| Tool loop molor / tool-calling tidak didukung model | F2 | maxRounds + timeout 60s + fallback jalur deterministik. |
| LLM mengarang angka laporan | F2 | Angka wajib dari tool result; verifikasi di acceptance test (banding SQL). |
| Spam proaktif membenci user | F3 | Opt-in, quiet hours, dedup harian, 1 pesan/chat/hari. |
| Biaya melonjak | semua | Task cost tracking sudah ada → alarm bila avg cost/pesan > threshold. |

## 9. Alternatif yang Dipertimbangkan

1. **Full autonomous agent loop untuk SEMUA jalur** — ditolak: mencatat uang butuh determinisme;
   biaya & latensi 5–10× untuk nilai tambah kecil (Prinsip 1).
2. **RAG atas `raw_payload` untuk memori** — ditolak di F1: kebutuhan anafora cukup 5 giliran
   ringkas; vector DB = infra baru belum proporsional.
3. **Prompt sejarah panjang (50 giliran)** — ditolak: token membengkak, kualitas ekstraksi menurun.
4. **LangGraph-style graph orchestrator** — ditolak sekarang: kompleksitas runtime baru;
   `agent.SubAgent` + task tracing sudah cukup hingga Level 3.

## 10. Urutan Implementasi & Definition of Done

| Fase | PR | Isi | DoD |
|---|---|---|---|
| F1 | #1 | tabel `chat_messages`, store, inject 5 giliran, purge job | §5.3 |
| F2 | #2 | `ChatWithTools` + 5 tool + loop report agent + fallback | §6.4 |
| F3 | #3 | scheduler, opt-in, dedup, 3 trigger | §7.2 |

F1 independen; F2 disarankan setelah F1 (jawaban komparatif memakai istilah riwayat);
F3 hanya bergantung pada data yang sudah ada.

## 11. Pertanyaan Terbuka

1. Apakah memori perlu per-sender di group (bukan per-chat) untuk privasi? (F1)
2. Apakah tool spec boleh dibagikan ke agent lain (stock agent) di F2.x, atau menunggu evaluasi F2? 
3. Rekap mingguan: default opt-in atau opt-out? (F3)
4. Budget alarm cost: threshold berapa (per pesan / per chat / per hari)?
