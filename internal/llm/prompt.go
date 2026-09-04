package llm

import (
	"fmt"
	"strings"
	"time"

	"smart-ledger-agent/internal/domain"
)

// Catatan arsitektur (multi-agent): system prompt BUKAN milik package ini.
// Package llm murni transport OpenAI-compatible; tiap SubAgent memiliki
// system prompt-nya sendiri (lihat prompt.go di masing-masing package internal/service/<domain>) dan
// mengirimkannya lewat parameter Extract/ClassifyIntent.

// TimeContext mengembalikan blok konteks tanggal hari ini untuk ditambahkan
// ke system prompt. Tanpa ini LLM tidak tahu tanggal hari ini, sehingga kata
// relatif ("kemarin", "besok") dan tahun berjalan ("11/08" → tahun?)
// cenderung dihalusinasi.
func TimeContext(now time.Time) string {
	hari := []string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}[int(now.Weekday())]
	return fmt.Sprintf("\n\n[KONTEKS WAKTU] Hari ini: %s (%s).", now.Format("2006-01-02"), hari)
}

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
		fmt.Fprintf(&b, "- %s (stok: %g %s)\n", it.Name(), it.StockQty, it.Unit)
	}
	if suffix != "" {
		b.WriteString(suffix)
	}
	b.WriteString("\n**ATURAN INVENTORY:** bila user menyebut barang yang SAMA PERSIS dengan item di atas (nama, ukuran, berat), gunakan **PERSIS** nama dari daftar inventory. Jangan membuat nama baru untuk barang yang sudah ada di inventory.\n")
	b.WriteString("**PENTING:** Barang dengan ukuran/berat berbeda adalah PRODUK BERBEDA. Contoh: \"susu bmt 400gr\" dan \"susu bmt 200gr\" adalah DUA barang berbeda dan harus punya entry terpisah.\n")
	return b.String()
}
