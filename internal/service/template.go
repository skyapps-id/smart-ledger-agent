package service

import "strings"

// OnboardingTemplate dikirim saat user pertama kali mengontak
// atau meminta bantuan. Menjelaskan format pencatatan.
const OnboardingTemplate = `Halo! Saya asisten pencatat keuangan & inventaris kamu.

Cukup kirim pesan seperti biasa, contoh:

PENGELUARAN:
> Beli bensin 50rb
> Beli susu UHT 1 dus isi 50pcs harga 500rb
> Bayar listrik 200rb

PEMASUKAN:
> Gaji masuk 10jt
> Jual barang 250rb
> Saldo awal 5jt (catat posisi kas awal, muncul terpisah dari pemasukan)

PEMAKAIAN STOK:
> Ambil susu UHT 2 pcs
> Pakai air mineral 1 pcs

LAPORAN (tanya aja):
> Pengeluaran hari ini berapa?
> Total pemasukan bulan ini
> Pengeluaran per item kemarin
> Barang saya apa aja? (sisa stok)
> Hari ini pakai apa aja? (stok keluar)
> Ringkasan kemarin

Tips:
- Sebut nominal (50rb / 500k / 5jt) -> tercatat pengeluaran.
- Sebut grosir (1 dus isi 50pcs) -> dikonversi otomatis.
- Tanpa nominal (ambil/pakai) -> tercatat pemakaian stok.

Ketik "bantuan" kapan saja untuk lihat panduan ini lagi.
Ketik "info" untuk melihat detail sesi/chat (debugging).
Init ulang dengan nama: init <nama ledger> (mis. "init project bangunan 1").`

// PreInitMessage dikirim ke pengirim yang belum melakukan init eksplisit.
const PreInitMessage = `Halo! Akun kamu belum aktif.
Ketik "init" untuk mulai mengaktifkan pencatatan keuangan & inventaris.
Opsional: "init <nama ledger>" (mis. "init project bangunan 1").`

// InitSuccessMessage dikirim saat pengirim berhasil melakukan init.
const InitSuccessMessage = `Akun aktif! Selamat datang.
Ketik "bantuan" kapan saja untuk melihat format pencatatan.`

// InitSuccessNamedMessage dikirim saat init disertai nama ledger.
const InitSuccessNamedMessage = `Akun aktif! Selamat datang.
Ledger: %s
Ketik "bantuan" kapan saja untuk melihat format pencatatan.`

// SmallTalkMessage dikirim saat pesan bukan transaksi (sapaan/chitchat)
// agar tidak dipaksakan menjadi pencatatan palsu oleh LLM.
const SmallTalkMessage = `Halo! Saya pencatat keuangan & inventaris.
Ketik "bantuan" untuk lihat cara pakai, atau langsung kirim pesan seperti "beli kopi 15rb".`

// parseInitCommand mendeteksi keyword aktivasi ledger dan mengekstrak nama
// opsional. Mengembalikan (true, name) bila pesan adalah command init;
// name bisa kosong.
//
// Contoh:
//
//	"init"                -> (true, "")
//	"init project bangunan 1" -> (true, "project bangunan 1")
//	`init "project bangunan 1"` -> (true, "project bangunan 1")   // quote dikupas
//	"mulai kas rumah"      -> (true, "kas rumah")
//	"bantuan"              -> (false, "")
func parseInitCommand(text string) (bool, string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return false, ""
	}
	lower := strings.ToLower(t)
	for _, p := range []string{"init", "mulai", "start", "daftar", "aktivasi"} {
		if lower == p {
			return true, ""
		}
		if strings.HasPrefix(lower, p+" ") {
			name := strings.TrimSpace(t[len(p):])
			name = stripMatchingQuotes(name)
			return true, name
		}
	}
	return false, ""
}

// stripMatchingQuotes mengupas sepasang quote/kutip tunggal di awal & akhir
// bila ada, agar `init "nama panjang"` dan `init 'nama'` bekerja natural.
func stripMatchingQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// isHelpCommand mendeteksi keyword yang meminta panduan.
func isHelpCommand(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "bantuan", "bantu", "panduan", "menu", "format", "help":
		return true
	}
	return false
}

// isInfoCommand mendeteksi keyword yang meminta metadata sesi/chat.
// Dipakai untuk diagnostic: user bisa ketik "info" kapan saja (bahkan pre-init)
// untuk melihat chat_id, sender_phone, status init, nama session WAHA, bot ID,
// dan jumlah transaksi pada chat tersebut.
func isInfoCommand(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "info", "sesi", "session", "identitas", "debug":
		return true
	}
	return false
}
