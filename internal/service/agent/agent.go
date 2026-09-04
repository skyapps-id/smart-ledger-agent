// Package agent berisi kontrak bersama sub-agent (SubAgent, Request,
// MessageSender) beserta utilitas lintas domain: helper balasan WhatsApp,
// error bisnis, format angka, dan template pesan.
//
// Setiap domain (transaction, stock, consumption, report, system) punya
// package sendiri di bawah internal/service/ dan mengimplementasikan
// kontrak SubAgent dari package ini.
package agent

import (
	"context"

	"smart-ledger-agent/internal/domain"
	"smart-ledger-agent/internal/entity"
	"smart-ledger-agent/internal/sender"
)

// MessageSender abstraksi pengiriman pesan ke WhatsApp.
type MessageSender interface {
	Enqueue(msg sender.Message) bool
}

// Request adalah konteks pemrosesan yang diteruskan Orchestrator ke sub-agent.
type Request struct {
	Message    entity.IncomingMessage // pesan mentah dari pengguna
	Chat       *domain.Chat           // status ledger chat
	Action     domain.ServiceAction   // hasil klasifikasi intent LLM
	IntentCost float64                // biaya LLM klasifikasi intent (diakumulasi ke balasan)
}

// SubAgent adalah kontrak agent spesialis domain. Orchestrator mengklasifikasi
// intent via LLM lalu men-dispatch pesan ke sub-agent yang menangani action
// tersebut. Setiap agent WAJIB memiliki system prompt sesuai skill-nya
// (lihat prompt.go di package masing-masing) — prompt dimiliki agent,
// bukan transport layer.
// Total LLM call per pesan tidak bertambah: klasifikasi tetap satu hop di
// orchestrator; agent yang belum memanggil LLM tetap wajib mendefinisikan
// prompt-nya sebagai bagian dari kontrak skill.
type SubAgent interface {
	// Actions mengembalikan daftar action yang ditangani agent ini.
	Actions() []string
	// SystemPrompt mengembalikan system prompt milik agent ini.
	SystemPrompt() string
	// Handle memproses satu request; balasan dikirim langsung via WhatsApp.
	Handle(ctx context.Context, req Request) error
}
