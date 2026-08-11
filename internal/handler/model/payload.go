// Package model berisi DTO (Data Transfer Object) yang dipakai handler
// untuk parsing payload webhook WAHA. Dipisah dari logic handler agar
// struct murni dapat dipakai ulang / dites tanpa dependency Echo.
package model

// WahaPayload merepresentasikan payload webhook WAHA (subset field relevan).
// Catatan: engine NOWEB sering TIDAK mengisi field "type" / "hasText",
// jadi deteksi pesan teks mengandalkan "body" yang tidak kosong.
type WahaPayload struct {
	Event   string `json:"event"`
	Session string `json:"session"`
	Me      struct {
		ID  string `json:"id"`  // mis. "6281380211359@c.us"
		Lid string `json:"lid"` // mis. "159948994543807@lid"
	} `json:"me"`
	Payload struct {
		From         string   `json:"from"`        // chat: phone@c.us / @lid / groupid@g.us
		Author       string   `json:"author"`      // sender asli pada pesan group (sebagian engine)
		Participant  string   `json:"participant"` // sender asli pada pesan group (NOWEB)
		FromMe       bool     `json:"fromMe"`
		Body         string   `json:"body"`
		Type         string   `json:"type"`
		MentionedIds []string `json:"mentionedIds"` // top-level (sebagian engine)
		// _data berisi JID alternatif + mention pada path nested (NOWEB).
		Data struct {
			Key struct {
				RemoteJid      string `json:"remoteJid"`
				RemoteJidAlt   string `json:"remoteJidAlt"`
				Participant    string `json:"participant"`
				ParticipantAlt string `json:"participantAlt"` // nomor asli sender group
			} `json:"key"`
			Message struct {
				ExtendedTextMessage struct {
					ContextInfo struct {
						MentionedJid []string `json:"mentionedJid"` // mention NOWEB
					} `json:"contextInfo"`
				} `json:"extendedTextMessage"`
			} `json:"message"`
		} `json:"_data"`
	} `json:"payload"`
}
