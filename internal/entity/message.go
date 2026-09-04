// Package entity berisi struct bisnis inti yang dipakai service untuk
// berkomunikasi dengan handler & worker. Dipisah dari implementasi service
// agar handler/worker dapat bergantung pada entity tanpa import seluruh
// package service.
package entity

// IncomingMessage merepresentasikan satu pesan masuk dari WhatsApp.
type IncomingMessage struct {
	UserPhone string // pengirim asli (audit); di group = participant sebenarnya
	ChatID    string // pemilik ledger (partition key): phone@c.us / id@g.us
	Text      string

	// TaskID adalah ID korelasi end-to-end (handler → worker → orchestrator
	// → sub-agent → reply). Dibuat di webhook, dibawa di semua log sehingga
	// satu pesan bisa dipantau lewat satu ID.
	TaskID string

	// Metadata sesi WAHA (audit/diagnostic). Dipakai command `info`
	// (lihat system agent, internal/service/system) untuk memperlihatkan identitas sesi ke user.
	SessionName string // p.Session (mis. "default")
	BotID       string // p.Me.ID (mis. "6281380211359@c.us")
	BotLid      string // p.Me.Lid (mis. "159948994543807@lid")
}
