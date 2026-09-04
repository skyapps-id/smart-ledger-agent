package agent

import (
	"strconv"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"

	"smart-ledger-agent/internal/domain"
)

// PendingChoice adalah konfirmasi yang menunggu jawaban user: bot baru saja
// menampilkan daftar pilihan bernomor (batch aktif, kandidat barang, dll) dan
// pesan BERIKUTNYA dari chat yang sama boleh berupa nomor pilihan ("1") atau
// nilai lengkap. Sekali terjawab (atau user mengirim hal lain), pending
// dihapus — state hidup maksimal selama TTL.
type PendingChoice struct {
	Action    string                 // action tujuan, mis. "consumption"
	Params    map[string]interface{} // params dasar dari pesan asli
	OptionKey string                 // param yang diisi pilihan, mis. "batch_number"
	Options   []string               // nilai pilihan, urut sesuai nomor tampilan
	// OriginalText adalah teks pesan saat konfirmasi dibuat. Saat jawaban
	// ("1") di-resolve, pesan yang di-dispatch memakai teks ini lagi —
	// penting untuk jalur yang butuh ekstraksi ulang (record_transaction).
	OriginalText string
	// FreeTextKey: bila diisi, jawaban user berupa teks bebas (bukan pilihan
	// bernomor) dan diisikan ke param dengan key ini (mis. "15lt" untuk
	// pertanyaan faktor konversi kemasan).
	FreeTextKey string
}

// PendingConfirms menyimpan PendingChoice per chat (satu chat = satu
// konfirmasi aktif). Nilai-nilai sengaja sederhana agar aman di-mutex oleh
// go-cache dan otomatis kadaluarsa.
type PendingConfirms struct {
	store *cache.Cache
}

// NewPendingConfirms membuat store konfirmasi; TTL 5 menit.
func NewPendingConfirms() *PendingConfirms {
	return &PendingConfirms{store: cache.New(5*time.Minute, 10*time.Minute)}
}

// Set menyimpan konfirmasi untuk chatID.
func (p *PendingConfirms) Set(chatID string, pc PendingChoice) {
	p.store.Set(chatID, pc, cache.DefaultExpiration)
}

// Resolve mencocokkan jawaban user dengan konfirmasi pending chat-nya.
// Return (action siap-dispatch, teks asli pesan saat konfirmasi dibuat, true)
// bila jawaban valid ("1", "2", atau nilai opsi persis). Pesan yang tidak
// cocok MENGHAPUS pending (user pindah topik) dan dikembalikan false agar
// diproses normal.
func (p *PendingConfirms) Resolve(chatID, text string) (domain.ServiceAction, string, bool) {
	pcRaw, ok := p.store.Get(chatID)
	if !ok {
		return domain.ServiceAction{}, "", false
	}
	pc, ok := pcRaw.(PendingChoice)
	if !ok {
		p.store.Delete(chatID)
		return domain.ServiceAction{}, "", false
	}

	params := make(map[string]interface{}, len(pc.Params)+1)
	for k, v := range pc.Params {
		params[k] = v
	}

	// Jawaban teks bebas (mis. "15lt"): pesan berikutnya diterima apa adanya
	// sebagai nilai param FreeTextKey — tanpa pencocokan opsi bernomor.
	if pc.FreeTextKey != "" {
		answer := strings.TrimSpace(text)
		if answer == "" {
			p.store.Delete(chatID)
			return domain.ServiceAction{}, "", false
		}
		params[pc.FreeTextKey] = answer
		p.store.Delete(chatID)
		return domain.ServiceAction{Action: pc.Action, Params: params}, pc.OriginalText, true
	}

	idx := matchOption(pc.Options, strings.TrimSpace(text))
	if idx < 0 {
		// Bukan jawaban atas konfirmasi → batalkan pending, proses pesan normal.
		p.store.Delete(chatID)
		return domain.ServiceAction{}, "", false
	}

	params[pc.OptionKey] = pc.Options[idx]
	p.store.Delete(chatID)
	return domain.ServiceAction{Action: pc.Action, Params: params}, pc.OriginalText, true
}

// matchOption mengembalikan indeks pilihan: menerima nomor ("1", "2"),
// varian "batch 1"/"pilih 2", atau nilai opsi persis (case-insensitive).
func matchOption(options []string, text string) int {
	t := strings.ToLower(text)
	if t == "" {
		return -1
	}

	if n, err := strconv.Atoi(t); err == nil {
		if n >= 1 && n <= len(options) {
			return n - 1
		}
		return -1
	}

	// "batch 1", "pilih 2", "no 3"
	if fields := strings.Fields(t); len(fields) == 2 {
		if n, err := strconv.Atoi(fields[1]); err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
	}

	for i, opt := range options {
		if strings.EqualFold(opt, t) {
			return i
		}
	}
	return -1
}
