# RFC: WAHA AI Agent for Personal Expense & Inventory Management

- **RFC Number:** RFC-2026-001
- **Title:** WhatsApp-Based Personal Finance and Inventory Tracking Engine
- **Status:** Implemented (Rev. C)
- **Authors:** Backend Engineering Team
- **Created:** August 10, 2026
- **Last Updated:** August 11, 2026
- **Target Stack:** Go 1.25 (Echo v4 + GORM), WAHA 2026.7 (engine NOWEB), OpenRouter (DeepSeek API), PostgreSQL

> **Riwayat Revisi**
> - **Rev. A (8 Aug 2026):** Spesifikasi awal (Draft).
> - **Rev. B (10 Aug 2026):** Implementasi referensi. Tambahan: onboarding/init flow, dukungan group chat (mention-based), autentikasi API Key WAHA, resolusi LID addressing, deployment Docker Compose. Aturan `affects_stock` (§5.6) — tidak semua `EXPENSE` menambah stok.
> - **Rev. C (11 Aug 2026):** Perubahan desain signifikan. (1) **Lingkup ledger berubah dari per-orang ke per-chat** — group = ledger bersama anggota, DM = ledger pribadi; reporter yang sama di 5 group menghasilkan 5 ledger terpisah. Tabel `users` diturunkan menjadi `chats`, kolom `user_phone` menjadi `chat_id`, dan tabel `transactions` mendapat kolom audit `sender_phone`. (2) **Tipe ekstraksi `NONE`** untuk menampung sapaan/chitchat agar tidak dipaksa menjadi transaksi palsu. (3) **Kategori `SALDO_AWAL`** untuk mencatat posisi awal kas; ditampilkan terpisah dari "Pemasukan" namun tetap dihitung di "Selisih". (4) **Stripping mention** `@<bot_jid>` dari body pesan group sebelum pemrosesan. (5) **Restrukturisasi paket**: `handler/model`, `repository/model`, dan `entity` (sibling `service`).

---

## 1. Abstract

Dokumen Request for Comments (RFC) ini menetapkan spesifikasi arsitektur, kriteria desain, serta standar integrasi untuk sistem **WAHA AI Agent**. Sistem ini bertugas mengolah input teks bahasa alami (*natural language*) dari pengguna melalui WhatsApp untuk secara otomatis mengklasifikasikan, mengekstrak, dan mencatat transaksi keuangan (*Ledger*) serta penggunaan persediaan barang (*Inventory*) ke dalam basis data terstruktur secara *asynchronous*.

Setiap chat (DM maupun group) merupakan satu **ledger** yang terisolasi. Pada group, seluruh anggota yang meng-mention bot berbagi ledger group yang sama; pada DM, ledger bersifat pribadi.

---

## 2. Problem Statement & Motivation

Pencatatan keuangan dan inventaris harian secara manual menggunakan aplikasi manajemen keuangan konvensional memiliki tingkat *friction* (hambatan) yang tinggi bagi pengguna. Hambatan utama meliputi keperluan membuka antarmuka aplikasi khusus, memilih menu multi-tingkat, dan menginput data secara manual.

Di sisi lain, pesan instan WhatsApp menawarkan antarmuka tanpa *friction*. Namun, mengubah teks tidak terstruktur (seperti *"Abis beli susu UHT 1 dus isi 50pcs harga 500rb"* atau *"Ambil susu UHT 2 pcs"*) menjadi catatan berbasis basis data yang konsisten menghadirkan beberapa tantangan teknis:

1. **Ambiguitas Teks Sintaksis:** Membedakan secara presisi antara pengeluaran uang langsung (`EXPENSE`), pemasukan (`INCOME`), pemakaian barang persediaan (`CONSUMPTION`), dan pesan non-transaksi (`NONE`).
2. **Kalkulasi & Konversi Kuantitas:** Mengubah unit grosir/kemasan menjadi unit eceran secara otomatis (contoh: $1 \text{ dus} \rightarrow 50 \text{ pcs}$).
3. **Latensi Pemrosesan:** Memastikan antarmuka WhatsApp merespons pesan secara *real-time* tanpa terhalang oleh latensi inferensi *Large Language Model* (LLM).

---

## 3. Scope & Out of Scope

### 3.1. In Scope
- Penerimaan pesan masuk dari WhatsApp HTTP API (WAHA) via Webhook.
- **Dukungan dua konteks pesan:** chat privat dan group (lihat §5.4).
- **Isolasi ledger per-chat** (Rev. C): setiap chat memiliki ledger independen.
- **Onboarding otomatis per-chat:** chat baru menerima template format pencatatan sebelum diproses (lihat §5.5).
- Parsing dan ekstraksi entitas teks menggunakan OpenRouter DeepSeek API.
- **Penanganan pesan non-transaksi (`NONE`)**: sapaan/chitchat tidak dipaksa menjadi transaksi.
- **Pencatatan saldo awal** sebagai posisi awal kas yang ditampilkan terpisah dari pemasukan (lihat §5.8).
- Normalisasi unit barang dan pemisahan logika transaksi keuangan vs stok.
- Penyimpanan data ACID-compliant pada SQLite/PostgreSQL.
- Pengiriman balasan konfirmasi pesan otomatis ke WhatsApp pengguna.
- **Deployment terkontainerisasi** via Docker Compose (WAHA + aplikasi).

### 3.2. Out of Scope
- Antarmuka web pengguna (Web UI/Dashboard) untuk visualisasi laporan.
- Sistem autentikasi multi-tenant terpisah (identitas ledger cukup diturunkan dari `chat_id`; identitas pengirim dicatat untuk audit).
- Pengolahan media selain teks (gambar struk, pesan suara, atau video).

---

## 4. High-Level Architecture & Workflow

Sistem dirancang berdasar pada arsitektur *event-driven asynchronous pipeline* untuk mengisolasi proses penerimaan webhook dari latensi ekstraksi LLM.

```
[User WhatsApp] ──> [WAHA Engine] ──(Webhook HTTP POST)──> [Go Webhook Handler]
                                                                  │
                                                        (Instant 200 OK Response)
                                                                  │
                                                        [Async Worker Queue]
                                                                  │
                                                    [OpenRouter DeepSeek API]
                                                                  │
                                                      [Logic & DB Transaction]
                                                                  │
[User WhatsApp] <──(Send Text API)── [WAHA Engine] <──────────────┴───
```

### 4.1. Komponen Teknologi
- **HTTP Framework:** Echo v4 (handler webhook, health check, middleware).
- **ORM / Persistence:** GORM dengan driver `gorm.io/driver/postgres`. Database tunggal: PostgreSQL (di-deploy via container `postgres:16-alpine`, lihat `docker-compose.yml`).
- **Worker Pool:** goroutine pool ber-channel dengan *exponential backoff* untuk error retryable.
- **WAHA Engine:** NOWEB (ringan, tanpa Chromium). Image Docker: `devlikeapro/waha:noweb-arm` (arm64) / `devlikeapro/waha:noweb` (amd64).

### 4.2. Lifecycle Request:
1. **Webhook Ingestion:** WAHA meneruskan payload pesan masuk ke Go Webhook Handler via HTTP POST.
2. **Immediate Acknowledgment:** Server Go segera memvalidasi *Webhook Token* dan payload, lalu mengembalikan status HTTP `200 OK` ke WAHA (< 50ms) guna mencegah pengiriman ulang pesan (*duplicate retries*).
3. **Routing Konteks:** Handler menentukan konteks pesan (privat vs group), melakukan filter mention pada group, **men-strip token `@<bot_jid>` dari body** (agar `init`/command matcher bekerja), lalu mengekstrak `chat_id` (partition key ledger) dan `sender_phone` (audit). Pesan masuk ke antrean pekerja.
4. **Onboarding Gate (Worker):** Bila chat belum *initialized* atau meminta bantuan, worker mengirim template pencatatan dan **tidak** melanjutkan ke ekstraksi. Lompat ke langkah 7.
5. **Intent Routing:** Bila pesan terdeteksi sebagai permintaan laporan (§5.7), worker membaca basis data pada scope `chat_id`, memformat jawaban, lalu lompat ke langkah 7. Bila tidak, lanjut ke ekstraksi.
6. **Entity Extraction (LLM):** Worker mengirimkan teks pesan beserta instruksi *system prompt* ketat ke OpenRouter DeepSeek API. Bila hasil ber-tipe `NONE`, worker membalas *small-talk message* tanpa persistensi.
7. **Business Logic & Database Persistence:** Hasil ekstraksi terstruktur (JSON) diproses oleh logika bisnis dan disimpan ke basis data melalui transaksi bersyarat, di-key oleh `chat_id`.
8. **Outbound Notification:** Worker memanggil API WAHA (`POST /api/sendText` dengan header `X-Api-Key`) untuk memberikan konfirmasi/balasan ke chat asal.

### 4.3. Struktur Paket Kode (Rev. C)
Pemisahan DTO/entitas dari logika untuk mencegah *import cycle* dan memudahkan *testing*:

```
internal/
├── entity/                  # entity bisnis lintas-layer
│   └── message.go           # IncomingMessage (dipakai handler, worker, service)
├── domain/                  # model domain persistensi (GORM models + konstanta)
├── handler/
│   ├── model/payload.go     # WahaPayload (DTO parsing webhook WAHA)
│   └── webhook.go
├── repository/
│   ├── model/model.go       # TxnSummary, ItemBreakdown, StockMovement (DTO hasil query)
│   ├── chat.go              # ChatRepository (onboarding per-chat)
│   ├── transaction.go
│   ├── inventory.go
│   ├── stock_log.go
│   └── report.go            # implementasi Summary / ExpenseByItem
├── service/
│   ├── agent.go             # orchestrator utama
│   ├── report.go            # reporting pipeline
│   └── template.go          # message templates
├── llm/                     # OpenRouter client + prompt
├── waha/                    # WhatsApp HTTP client
├── worker/                  # async worker pool
├── database/                # GORM setup + auto-migrate
├── router/                  # Echo routes
└── config/                  # env loader
```

---

## 5. Functional Requirements & Specifications

### 5.1. Klasifikasi Jenis Transaksi (`type`)
Sistem harus mengklasifikasikan setiap pesan masuk ke dalam salah satu dari empat tipe:

| Tipe Transaksi | Definisi Logika | Contoh Input Teks |
| :--- | :--- | :--- |
| `INCOME` | Transaksi penambahan uang kas/rekening (termasuk saldo awal, lihat §5.8). | *"Gaji bulan ini masuk 10jt"* |
| `EXPENSE` | Transaksi pengurangan uang kas/rekening untuk barang, jasa, atau operasional. | *"Beli bensin 50rb"*, *"Beli susu UHT 1 dus isi 50pcs harga 500rb"* |
| `CONSUMPTION` | Penggunaan/pengurangan stok barang tanpa transaksi uang langsung. | *"Ambil susu UHT 2 pcs"* |
| `NONE` | Pesan BUKAN transaksi (sapaan, chitchat, salam, terima kasih, pertanyaan umum ke manusia, emoji, kosong). Tidak boleh dipaksakan menjadi transaksi. | *"halo"*, *"pagi"*, *"makasih"* |

### 5.2. Aturan Resolusi Ambiguitas (Ambiguity Resolution Rules)
- **Aturan Uang (Monetary priority):** Jika pesan mengandung kata-kata yang mengindikasikan pemakaian barang (misal *"ambil"*, *"pakai"*), namun memuat nominal nilai uang (seperti *"500rb"*, *"50k"*), sistem **WAJIB** memprioritaskan klasifikasi sebagai `EXPENSE`.
- **Aturan Pemakaian Murni:** Tipe `CONSUMPTION` **HANYA** digunakan jika tidak ada nominal uang yang disebutkan dalam pesan. Nilai `amount` pada tipe ini wajib bernilai `0`.
- **Aturan Non-Transaksi:** Bila pesan tidak bermakna transaksional (sapaan/chitchat), sistem mengembalikan `NONE` dan **tidak memaksakan** klasifikasi ke `EXPENSE`/`INCOME`/`CONSUMPTION`.

### 5.3. Konversi Unit Grosir & Normalisasi Entitas
- Jika pengguna menyebutkan rincian grosir (contoh: *"1 dus isi 50pcs"*), sistem LLM mengekstrak nilai kuantitas ke dalam unit eceran terbesar (`quantity: 50`, `unit: "pcs"`), sedangkan informasi grosir disimpan pada bidang catatan (`notes`).
- Nama barang (`item_name`) disatukan dalam format teks normal (*lowercase & space-trimmed*) pada tingkat basis data untuk mencegah duplikasi entitas inventaris.

### 5.4. Routing Konteks: Chat Privat vs Group
Sistem wajib membedakan dua konteks pesan berdasar suffix JID pada field `from`:

| Konteks | Deteksi | Perilaku |
| :--- | :--- | :--- |
| **Privat** | suffix `@c.us` / `@lid` | Diproses langsung. `chat_id = from`; `sender_phone` diturunkan dari `remoteJidAlt` (nomor asli) atau `from`. |
| **Group** | suffix `@g.us` | **Hanya diproses bila bot di-@mention** (`mentionedIds`/`mentionedJid` memuat `me.id`/`me.lid`); selainnya diabaikan diam-diam (*anti-spam*). `chat_id = from` (ID group); `sender_phone` diturunkan dari `participantAlt`/`author`/`participant` (pengirim asli). |

**Konsekuensi (Rev. C):** laporan keuangan & inventaris bersifat **per-chat**, BUKAN per-orang. Pada group, seluruh anggota yang meng-mention bot berbagi ledger yang sama. Reporter yang sama di 5 group menghasilkan 5 ledger terpisah. Balasan pada konteks group dikirim secara publik ke group yang sama (`@g.us`).

> **Penghapusan mention dari body:** Pada pesan group, token `@<bot_phone>` (turunan `me.id`) dan `@<bot_lid>` (turunan `me.lid`) **dihapus** dari body teks sebelum diproses. Ini wajib karena ID bot hanya tersedia di payload webhook (`p.Me`), dan tanpa stripping, body seperti `"@159948994543807 init"` tidak akan cocok dengan matcher `isInitCommand`/`isHelpCommand`.

### 5.5. Onboarding & Bantuan (Init Eksplisit per Chat)
Sistem menganut model **init eksplisit per chat**: sebuah chat harus diaktifkan sebelum pemrosesan pesan apapun.

- **Pre-init Gate:** Selama `chats.initialized = false`, SEMUA pesan (selain command init/info) hanya membalas pesan *Pre-Init* yang meminta pengirim mengetik `init`. Tidak ada pencatatan/laporan yang diproses.
- **Command Init (+ nama opsional):** Pesan bernilai (case-insensitive) `init`, `mulai`, `start`, `daftar`, atau `aktivasi` mengaktifkan chat (`initialized = true`) dan membalas konfirmasi. Idempoten — init berulang tidak mengubah status. Setiap chat di-init secara independen (DM terpisah dari group A, terpisah dari group B, dst.).
  - **Nama ledger opsional:** segmen teks setelah keyword init dijadikan nama ledger yang disimpan di `chats.name`. Contoh: `init project bangunan 1` → name="project bangunan 1". Tanda kutip di awal/akhir otomatis dikupas: `init "kas rumah"` ≡ `init kas rumah`. Nama ditampilkan di command `info` (§5.5).
  - **Rename via re-init:** bila chat sudah aktif dan user mengirim `init <nama baru>`, hanya nama yang diperbarui (status tetap aktif). Bila `init` tanpa argumen pada chat yang sudah aktif, cukup dibalas status.
- **Keyword Bantuan (post-init):** Pesan `bantuan`, `bantu`, `panduan`, `menu`, `format`, `help` memicu pengiriman template format pencatatan tanpa efek samping.
- **Command Info (diagnostic, selalu tersedia):** Pesan `info`, `sesi`, `session`, `identitas`, atau `debug` memicu balasan metadata sesi — berguna untuk debugging alur init/mention/group. Tersedia bahkan sebelum init. Isi balasan:
  - `Chat ID` — partition key ledger (`phone@c.us` / `id@g.us`)
  - `Tipe` — Group / Privat / Privat (LID) — diderive dari suffix `chat_id`
  - `Status` — Aktif / Belum init
  - `Sender` — `sender_phone` (pengirim asli pesan ini)
  - `Session` — nama sesi WAHA (`p.Session`)
  - `Bot ID` / `Bot LID` — `me.id` / `me.lid`
  - `Transaksi` — total baris transaksi pada chat ini (via `TransactionRepository.CountByChat`)

### 5.6. Aturan Pengeluaran vs Stok (`affects_stock`)
Tidak semua `EXPENSE` menambah inventaris. Sistem mengandalkan judgement LLM lewat field `affects_stock`:
- **`true`** → barang **fisik yang disimpan/ditabung stok** (sembako, perlengkapan rumah, bahan isi ulang). Contoh: *"Beli susu UHT 1 dus 500rb"*, *"Beli minyak goreng 2 liter"*.
- **`false`** → **jasa, utilitas, BBM, transport, makan langsung, tiket**, atau barang habis pakai seketika. Contoh: *"Beli bensin 50rb"*, *"Bayar listrik 200rb"*, *"Makan siang 25rb"*.

Hanya `EXPENSE` dengan `affects_stock = true` yang memicu *upsert* `inventory` dan `stock_logs` (detail di §7.2).

### 5.7. Intent Reporting (Pertanyaan vs Pencatatan)
Selain path pencatatan, sistem mendeteksi pesan sebagai **permintaan laporan** bila memuat penanda tanya (suffix `?`, atau kata `berapa`/`ringkas`/`laporan`/`riwayat`/`sisa`/`stok apa`, atau diawali `lihat`/`tampilkan`/`cek`). Pesan demikian **tidak** diteruskan ke ekstraksi LLM; alih-alih:

1. **Parsing metric** dari teks:
   - `STOCK` → bila menyebut *stok/sisa/persediaan/inventaris* (daftar stok saat ini).
   - `EXPENSE_BY_ITEM` → bila menyebut *per item/per barang/rincian belanja/beli apa* (rincian pengeluaran per nama barang).
   - `CONSUMPTION` → bila menyebut *pemakaian/dipakai/stok keluar* (riwayat pengurangan stok dari `stock_logs`).
   - `INCOME` → bila menyebut *pemasukan/pendapatan/masuk*.
   - `EXPENSE` → bila menyebut *pengeluaran/biaya/keluar*.
   - `SUMMARY` (default) → selisih pemasukan & pengeluaran.
2. **Parsing periode** (default: hari ini): `hari ini`, `kemarin`, `minggu ini`, `minggu lalu`, `bulan ini`, `bulan lalu`, `semua`.
3. **Agregasi basis data** (selalu di-scope ke `chat_id`):
   - `TransactionRepository.Summary` (total income/expense + saldo awal + rincian per kategori).
   - `TransactionRepository.ExpenseByItem` (rincian pengeluaran per nama barang).
   - `InventoryRepository.ListByChat` (stok saat ini pada chat).
   - `StockLogRepository.MovementsByChat` (riwayat IN/OUT stok, join `inventory` untuk `item_name`/`unit`).
4. **Format jawaban** natural (deterministik, tanpa panggilan LLM tambahan).

### 5.8. Saldo Awal (Opening Balance)
Untuk mencatat posisi awal kas tanpa mengembungkan total pemasukan riil, sistem mengenal kategori khusus `SALDO_AWAL`:

- **Input pemicu:** pesan yang menyebut *"saldo awal"*, *"baki awal"*, *"modal awal"*, atau *"saldo pertama"* dengan nominal.
- **Klasifikasi:** `type = INCOME`, `category = SALDO_AWAL`, `item_name = "saldo awal"`.
- **Penyimpanan:** sebagai baris transaksi biasa (mempertahankan audit trail bertanggal; mendukung beberapa opening balance sepanjang waktu).
- **Tampilan laporan:** `Summary()` memisahkan akumulasi `SALDO_AWAL` ke field `OpeningBalance`, terpisah dari `Income`. Laporan menampilkan baris **"Saldo awal"** hanya bila nilainya > 0. **Selisih = Saldo awal + Pemasukan − Pengeluaran**, sehingga *running balance* tetap akurat.

---

## 6. Data Schema & Contract Specifications

### 6.1. Contract Output JSON (LLM Interface)

LLM diwajibkan mengembalikan struktur data JSON valid tanpa elemen sintaks tambahan.

| Field | Tipe Data | Wajib | Keterangan |
| :--- | :--- | :--- | :--- |
| `type` | String | Ya | Nilai terbatas pada: `"INCOME"`, `"EXPENSE"`, `"CONSUMPTION"`, `"NONE"`. |
| `category` | String | Ya | Kategori ringkas: `"GAJI"`, `"MAKAN"`, `"HARI_HARI"`, `"TAGIHAN"`, `"HOBBY"`, `"STOK_KELUAR"`, `"SALDO_AWAL"`, `"LAINNYA"`. |
| `item_name` | String | Ya | Deskripsi/nama barang atau transaksi. Kosong untuk `NONE`. |
| `quantity` | Float/Number| Ya | Kuantitas fisik barang (Default `1` jika tidak disebutkan). |
| `unit` | String | Ya | Satuan barang (`"pcs"`, `"porsi"`, `"liter"`, `"pack"`, dll). |
| `amount` | Float/Number| Ya | Nilai transaksi uang dalam nominal bulat (Default `0` untuk `CONSUMPTION`/`NONE`). |
| `affects_stock` | Boolean | Ya | Hanya relevan untuk `EXPENSE`. `true` bila barang fisik yang ditabung/disi-mpan stok; `false` untuk jasa, utilitas, BBM, makan langsung, dll. Menentukan apakah inventaris di-*upsert* (lihat §7.2). Selalu `false` untuk `INCOME`/`CONSUMPTION`/`NONE`. |
| `notes` | String | Tidak | Catatan tambahan atau metadatar grosir. |

### 6.2. Skema Relasional Basis Data (Database Schema)

#### Tabel Chat / Ledger (`chats`) — Rev. C
- **`id`**: BigSerial (Primary Key)
- **`chat_id`**: Varchar(64) (Unique — partition key ledger: `phone@c.us` / `id@g.us`)
- **`name`**: Varchar(128) (Label opsional ledger, mis. "project bangunan 1"; di-set via `init <nama>`)
- **`initialized`**: Boolean (Default `false` — status onboarding per-chat)
- **`created_at`**: Timestamptz
- **`updated_at`**: Timestamptz

#### Tabel Transaksi Keuangan (`transactions`)
- **`id`**: BigSerial (Primary Key)
- **`chat_id`**: Varchar(64) (Index — pemilik ledger)
- **`sender_phone`**: Varchar(32) (Pengirim asli pesan; audit trail. Pada group bisa berbeda-beda per transaksi.)
- **`type`**: Varchar(16) (`INCOME` / `EXPENSE`)
- **`category`**: Varchar(32) (Termasuk `SALDO_AWAL`)
- **`item_name`**: Varchar(128)
- **`amount`**: Numeric(15,2)
- **`raw_payload`**: Text (Pesan asli dari pengguna)
- **`created_at`**: Timestamptz

#### Tabel Master Stok Inventaris (`inventory`)
- **`id`**: BigSerial (Primary Key)
- **`chat_id`**: Varchar(64)
- **`item_name`**: Varchar(128) (Unique constraint bersama `chat_id`: `idx_inv_chat_item`)
- **`stock_qty`**: Numeric(12,2)
- **`unit`**: Varchar(32)
- **`updated_at`**: Timestamptz

#### Tabel Riwayat Perubahan Stok (`stock_logs`)
- **`id`**: BigSerial (Primary Key)
- **`inventory_id`**: BigInt (Foreign Key ke `inventory.id`)
- **`change_type`**: Varchar(16) (`IN` untuk pembelian, `OUT` untuk pemakaian)
- **`quantity`**: Numeric(12,2)
- **`notes`**: Text
- **`created_at`**: Timestamptz

---

## 7. Business Logic & Transactional Integrity

### 7.0. Pra-kondisi: Init Gate per Chat (Eksplisit)
Sebelum klasifikasi & persistensi, worker memeriksa status `chats.initialized` pada `chat_id`:
- Bila pesan adalah command init → set `initialized = true`, balas konfirmasi, **berhenti**.
- Bila belum *initialized* (dan bukan command init) → balas *Pre-Init Message*, **berhenti** (tolak semua pesan lain).
- Bila sudah *initialized* → lanjut ke help/report/klasifikasi (§7.1–7.3).

### 7.1. Penanganan Non-Transaksi (`NONE`)
Bila hasil ekstraksi LLM ber-tipe `NONE`, worker membalas *Small-Talk Message* (panduan ringkas ke `bantuan`) dan **tidak melakukan persistensi apa pun**. Hal ini mencegah baris transaksi sampah (mis. *"halo"* dicatat sebagai `EXPENSE` Rp0) yang terjadi pada revisi sebelumnya.

### 7.2. Persistensi Transaksional
Setiap instruksi eksekusi data wajib dijalankan dalam bingkai Transaksi Basis Data (*Database Transaction*) terisolasi, dengan seluruh operasi di-scope ke `chat_id`:

1. **Kasus `EXPENSE`:**
   - Selalu masukkan baris pencatatan baru ke tabel `transactions` (mencatat `chat_id` + `sender_phone`).
   - **Bila `affects_stock = true`** (barang fisik stok): lakukan operasi *Upsert* (`INSERT ... ON CONFLICT DO UPDATE`) pada tabel `inventory` pada `(chat_id, item_name)` untuk menambah nilai `stock_qty` sebesar kuantitas barang yang dibeli ($+N$), dan catat entri pada `stock_logs` dengan `change_type = 'IN'`.
   - **Bila `affects_stock = false`** (jasa/utilitas/BBM/makan langsung): jangan sentuh `inventory` maupun `stock_logs`; cukup transaksi uang yang tercatat.

2. **Kasus `CONSUMPTION` Pemakaian Stok:**
   - Cari entri barang pada `inventory` berdasarkan `chat_id` dan `item_name`.
   - Jika entri ditemukan dan stok mencukupi, kurangi nilai `stock_qty` ($-N$).
   - Jika entri barang tidak ditemukan, batalkan transaksi dan kirimkan pesan peringatan bahwa barang belum terdaftar di inventaris chat tersebut.
   - Catat entri perubahan pada `stock_logs` dengan `change_type = 'OUT'`.

3. **Kasus `INCOME` (termasuk `SALDO_AWAL`):**
   - Cukup masukkan baris pencatatan ke `transactions`. Tidak ada efek ke `inventory`/`stock_logs`.
   - Untuk `category = SALDO_AWAL`, agregasi laporan memisahkan nilai ini dari `Income` (lihat §5.8).

> **Catatan pengurangan stok atomik:** Operasi `−N` dijalankan via `UPDATE ... WHERE stock_qty ≥ N` dalam satu statement; bila `rows_affected = 0` dianggap stok tidak mencukupi (mencegah race condition TOCTOU).

---

## 8. Non-Functional Requirements & Safety
1. **Rate Limit & Resiliensi:** OpenRouter API tier gratis memiliki batasan jumlah pemanggilan (*Rate Limit*). Antrean pekerja (*Worker Queue*) dilengkapi *exponential backoff retry* untuk error retryable (mis. HTTP `429 Too Many Requests` atau kegagalan transport jaringan).
2. **Integritas ACID:** Pembaruan data transaksi dan stok wajib bersifat atomik. Kegagalan pada penambahan stok harus menyebabkan *rollback* pada pencatatan transaksi uang.
3. **Autentikasi API WAHA:** Semua panggilan keluar ke WAHA (`POST /api/sendText`) wajib menyertakan header `X-Api-Key` yang nilainya dikonfigurasi via `WAHA_API_KEY`.
4. **Verifikasi Webhook Masuk:** Endpoint `POST /webhook` memvalidasi *Webhook Token* (`WAHA_WEBHOOK_TOKEN`) via query string `?token=` atau header `X-Webhook-Token`; ketidakcocokan dikembalikan sebagai `401 Unauthorized`. Token disisipkan WAHA lewat `WHATSAPP_HOOK_URL`.
5. **Resolusi Identitas (LID Addressing):** Engine NOWEB dapat mengirim `from` berformat `@lid` (Linked Identity). Sistem wajib:
   - Menggunakan `from` apa adanya sebagai `chat_id` (WAHA tahu meresolve-nya).
   - Mengekstrak `sender_phone` (nomor asli pengirim) dari field `participantAlt`/`remoteJidAlt`/`participant` agar kolom audit selalu berupa nomor telepon sebenarnya.
6. **Keamanan & Privasi:** `chat_id` dan `sender_phone` harus dibatasi akses log-nya pada tingkatan sistem monitoring; kredensial (`WAHA_API_KEY`, `OPENROUTER_API_KEY`) hanya boleh diinjeksi via environment dan tidak dicatat ke log.

---

## 9. Future Improvements (Unresolved Questions)
- **Handling Undefined Units:** Bagaimana sistem menangani kondisi ketika unit barang saat pembelian (`EXPENSE`) berbeda dengan unit saat konsumsi (`CONSUMPTION`) (misal beli dalam unit *"pack"* tapi dikonsumsi dalam *"sachet"*)?
- **Multi-Currency Support:** Penanganan otomatis untuk transaksi luar negeri atau konversi mata uang non-IDR.
- **Interactive Clarification:** Pengembangan fitur di mana AI Agent balik bertanya kepada pengguna jika kepastian ekstraksi JSON berada di bawah ambang batas kesahihan (*confidence threshold*).
- **Periode Opening Balance (true period-aware):** Saat ini `SALDO_AWAL` hanya menampilkan akumulasi total pada periode laporan. Pengembangan: hitung "saldo awal periode" sebagai `SUM(SALDO_AWAL) + SUM(INCOME) − SUM(EXPENSE)` sebelum `periode.from`, sehingga laporan bulanan/kemarin benar-benar menggambarkan posisi awal periode tersebut.
- **Per-reporter View di Group:** Pada group dengan banyak reporter, opsi memfilter laporan per `sender_phone` (mis. "pengeluaran saya saja bulan ini").
- **Onboarding privat-only di group:** Saat ini template onboarding untuk chat baru di group dikirim secara publik ke group. Opsional: kirim template via DM pengirim alih-alih ke group.

### 9.1. Resolved (sejak Rev. B/C)
- ~~*Multi-user identity:*~~ (Rev. B) Identitas pengirim diturunkan dari nomor WA per-orang.
- ~~*Per-orang ledger di group:*~~ (Rev. C) **Berubah**: ledger sekarang **per-chat**. Pada group, seluruh anggota berbagi satu ledger. Alasan: konteks pengeluaran rumah tangga/tim lebih alami dikelompokkan per group; reporter yang sama di group berbeda lazimnya mengelola kas terpisah.
- ~~*Sapaan/chitchat dipaksa jadi transaksi:*~~ (Rev. C) Ditangani via tipe `NONE`.
- ~~*Pencatatan saldo awal mengembungkan pemasukan:*~~ (Rev. C) Ditangani via kategori `SALDO_AWAL` yang dipisahkan di laporan.
- ~~*Command matcher gagal pada pesan mention group:*~~ (Rev. C) Mention `@<bot_jid>` di-strip dari body di webhook sebelum queueing.
- ~~*Deteksi tipe pesan teks pada engine NOWEB:*~~ (Rev. B) Field `type`/`hasText` tidak diandalkan; deteksi teks mengandalkan keberadaan `body` non-kosong.
