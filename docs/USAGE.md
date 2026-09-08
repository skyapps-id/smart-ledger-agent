# 💬 Commands & Natural Language Queries

> Back to [README](../README.md)

## Basic Commands
| Message | Action |
| :--- | :--- |
| `init` | Activate the ledger for this chat |
| `init project bangunan 1` | Activate + name the ledger |
| `info` | Show session metadata (chat_id, sender, status, name, transaction count) |
| `bantuan` | Show the recording-format guide |

## Master Barang (goods) — register first, everything else follows
```
tambah barang galon air satuan galon, 1 galon = 15lt, kategori MINUMAN
tambah barang bensin satuan liter kategori TRANSPORT   # kategori opsional (disarankan otomatis)
master barang                                           # daftar + faktor
info barang galon air                                   # detail + stok
set 1 galon air 15lt                                    # ubah faktor konversi
set satuan beras jadi kg                                # ubah satuan kanonik
set kategori galon air jadi MINUMAN                     # kategori permanen
set stok gas lpg 3kg ya                                 # pembelian menambah stok
set stok bensin tidak                                   # jasa/BBM: hanya catat keuangan
tambah barang bensin satuan liter non stok              # daftar langsung non-stok
```

## Transaction Recording (items must exist in the master)
```
tambah barang kopi satuan sachet          # 1. daftar dulu (master-first)
beli kopi 10sachet 15rb                   # 2. EXPENSE + stok, kategori dari master
gaji masuk 10jt                           # INCOME (juga butuh barang "gaji" terdaftar)
ambil kopi 1 sachet                       # CONSUMPTION: stok berkurang
saldo awal 5jt                            # INCOME: opening balance
beli barang asing 20rb                    # ❌ ditolak: "daftarkan dulu: tambah barang ..."
```

## Stock Queries (optimized for token efficiency)
```
stok                                        # → Show category summary (hemat tokens)
cek stock kecap                             # → Search specific item (1-5 results)
stok susu                                   # → Search specific item (1-5 results)
sisa air                                    # → Search specific item (1-5 results)
persediaan popok                            # → Search specific item (1-5 results)
stok minuman                                # → Search by category
barang saya apa aja                         # → Show category summary
```

**Token Optimization:**
- General query ("stok") → Category summary (~200 tokens vs ~1500 before)
- Specific query ("stok kecap") → Search results (~100 tokens vs ~1500 before)
- Overall savings: 60-90% per stock query

## Consumption Tracking (units converted via master factors)
```
konsumsi                                    # → Shows all active consumption cycles
konsumsi susu                               # → Consumption info for specific item
pakai galon air                             # → 1 satuan stok (galon), cycle baru
pakai galon air 3lt                         # → dikonversi via faktor master: 3/15 = 0.2 galon
pakai galon air 3kg                         # → ❌ "Satuan 'kg' tidak dikenali... sebut dalam galon atau lt"
terpakai galon air (SEP-07-105204) 5lt      # → Correct consumed amount for a batch
galon air sudah habis                       # → Complete cycle + rate/hari (satuan master)
barang aktif                                # → Lists all items currently being consumed
```

## Financial Reports
```
pengeluaran hari ini berapa                 # → Today's expenses
total pemasukan bulan ini                   # → Income this month
pengeluaran per item kemarin                # → Yesterday's expenses per item
ringkasan kemarin                           # → Yesterday's summary
```
