package llm

import (
	"fmt"
	"strings"
	"time"
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
