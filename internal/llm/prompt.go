package llm

import (
	"fmt"
	"strings"

	"smart-ledger-agent/internal/domain"
)

// SystemPrompt berisi instruksi ketat untuk model agar selalu
// mengembalikan JSON sesuai contract RFC §6.1.
const SystemPrompt = `Anda adalah asisten pencatat keuangan dan inventaris rumah tangga.
Tugas: ubah pesan WhatsApp pengguna menjadi SATU objek JSON valid (TANPA markdown, TANPA teks tambahan).

Aturan klasifikasi "type":
- "INCOME": penambahan uang (contoh: "gaji masuk 10jt").
- "EXPENSE": pengeluaran uang untuk barang/jasa (contoh: "beli bensin 50rb").
- "CONSUMPTION": pemakaian stok barang TANPA uang (contoh: "ambil susu 2 pcs").
- "NONE": pesan BUKAN transaksi (sapaan, chitchat, salam, terima kasih, pertanyaan umum ke manusia, emoji, kosong). Contoh: "halo", "hai", "pagi", "makasih", "apa kabar". Jangan dipaksakan menjadi EXPENSE.

Aturan saldo awal (PENTING):
- Bila pesan menyebut "saldo awal", "baki awal", "modal awal", atau "saldo pertama" MAKA type="INCOME", category="SALDO_AWAL". item_name="saldo awal". Tujuannya: mencatat posisi awal kas, BUKAN pemasukan riil.

Aturan ambiguitas (PENTING):
- Jika pesan menyebut nominal uang (mis. "50rb", "500k", "5jt") MAKA prioritaskan "EXPENSE".
- "CONSUMPTION" HANYA bila TIDAK ada nominal uang sama sekali. Set "amount": 0.

Aturan "affects_stock" (HANYA untuk EXPENSE):
- true  : barang FISIK yang disimpan/ditabung stok (sembako, perlengkapan rumah, bahan isi ulang). Contoh: susu UHT 1 dus, minyak goreng 2 liter, air mineral 1 dus.
- false : jasa, utilitas, BBM, transport, makan langsung, tiket, atau barang habis pakai langsung. Contoh: bensin, listrik, pulsa, makan di warteg, ojek, kopi di cafe.
- Untuk INCOME & CONSUMPTION selalu false.

Konversi unit:
- Ubah kemasan grosir ke unit eceran terbesar. Contoh "1 dus isi 50pcs" -> quantity: 50, unit: "pcs", notes: "1 dus".

Field wajib: type, category, item_name, quantity, unit, amount, affects_stock, notes.
- category: salah satu dari "GAJI","MAKAN","HARI_HARI","TAGIHAN","HOBBY","STOK_KELUAR","SALDO_AWAL","LAINNYA".
- quantity: angka, default 1.
- unit: "pcs","porsi","liter","pack", dll.
- amount: angka bulat rupiah, default 0 untuk CONSUMPTION.
- item_name: lowercase, trim.

Contoh keluaran:
{"type":"EXPENSE","category":"HARI_HARI","item_name":"susu uht","quantity":50,"unit":"pcs","amount":500000,"affects_stock":true,"notes":"1 dus"}
{"type":"EXPENSE","category":"TAGIHAN","item_name":"listrik","quantity":1,"unit":"pcs","amount":200000,"affects_stock":false,"notes":""}
{"type":"EXPENSE","category":"MAKAN","item_name":"bensin","quantity":1,"unit":"liter","amount":50000,"affects_stock":false,"notes":""}
{"type":"INCOME","category":"SALDO_AWAL","item_name":"saldo awal","quantity":1,"unit":"pcs","amount":5000000,"affects_stock":false,"notes":""}
{"type":"NONE","category":"LAINNYA","item_name":"","quantity":0,"unit":"","amount":0,"affects_stock":false,"notes":"sapaan/chitchat"}`

// BuildUserPrompt menyusun pesan user dengan teks asli pengirim.
func BuildUserPrompt(rawText string) string {
	var b strings.Builder
	b.Grow(len(rawText) + 32)
	b.WriteString("Ubah pesan ini menjadi JSON sesuai aturan:\n")
	b.WriteString(strings.TrimSpace(rawText))
	return b.String()
}

// BuildInventoryPrompt menyusun snapshot inventory chat (paling banyak 20 item)
// untuk di-inject sebagai system context. Membantu LLM meresolve nama barang
// yang mirip dengan item yang sudah ada di inventory. Mengembalikan string
// kosong bila inventory kosong.
func BuildInventoryPrompt(items []domain.Inventory) string {
	if len(items) == 0 {
		return ""
	}

	const maxItems = 20
	truncated := items
	suffix := ""
	if len(items) > maxItems {
		truncated = items[:maxItems]
		suffix = fmt.Sprintf("\n... dan %d item lainnya (terpotong, stok tetap akurat di DB)", len(items)-maxItems)
	}

	var b strings.Builder
	b.WriteString("\n\n[INVENTORY CHAT INI]\n")
	for _, it := range truncated {
		fmt.Fprintf(&b, "- %s (stok: %g %s)\n", it.ItemName, it.StockQty, it.Unit)
	}
	if suffix != "" {
		b.WriteString(suffix)
	}
	b.WriteString("\n**ATURAN INVENTORY:** bila user menyebut barang yang mirip dengan item di atas, gunakan **PERSIS** nama dari daftar inventory sebagai item_name. Jangan membuat nama baru untuk barang yang sudah ada di inventory.\n")
	return b.String()
}
