// Package sender menyediakan implementasi processor untuk pengiriman pesan.
package sender

import (
	"context"

	"smart-ledger-agent/internal/waha"
)

// WahaProcessor adalah implementasi Processor menggunakan waha.Sender.
type WahaProcessor struct {
	wahaSender waha.Sender
}

// NewWahaProcessor membuat processor WAHA baru.
func NewWahaProcessor(wahaSender waha.Sender) *WahaProcessor {
	return &WahaProcessor{
		wahaSender: wahaSender,
	}
}

// SendMessage mengirim pesan teks ke WhatsApp via WAHA. Indikator
// "mengetik..." (dinyalakan webhook saat pesan masuk) dimatikan dulu;
// gagal stop tidak menggagalkan pengiriman.
func (p *WahaProcessor) SendMessage(ctx context.Context, msg Message) error {
	_ = p.wahaSender.StopTyping(ctx, msg.ChatID)
	return p.wahaSender.SendText(ctx, msg.ChatID, msg.Text)
}
