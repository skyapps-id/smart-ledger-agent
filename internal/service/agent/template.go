package agent

import "strings"

// OnboardingTemplate dikirim saat user meminta bantuan tanpa topik.
// Sengaja ringkas: satu contoh inti per kebutuhan, detail ada di
// bantuan per topik (HelpTopics).
const OnboardingTemplate = `Halo! Saya pencatat keuangan & stok kamu 👋
Cukup kirim pesan biasa, contoh:

1️⃣ *Daftarkan barang dulu* (sekali saja)
> tambah barang beras satuan kg

2️⃣ *Catat transaksi*
> beli beras 5kg 75rb
> gaji masuk 10jt

3️⃣ *Pakai & cek stok*
> pakai beras 1 kg
> stok beras

4️⃣ *Lihat laporan*
> pengeluaran hari ini
> ringkasan bulan ini

📖 Panduan lengkap per topik:
*bantuan barang* · *bantuan transaksi* · *bantuan stok* · *bantuan konsumsi* · *bantuan laporan*`

// HelpTopics berisi panduan detail per topik ("bantuan stok", dst).
// Key = nilai params.topic dari klasifikasi intent.
var HelpTopics = map[string]string{
	"barang": `📦 *Bantuan: Master Barang*
Setiap barang wajib didaftarkan sebelum dicatat. Satuan & konversinya diambil dari sini.

*Daftarkan*
> tambah barang galon satuan galon
> tambah barang galon satuan galon 1 galon 15lt
> tambah barang bensin satuan liter non stok

*Lihat*
> master barang
> info barang galon

*Ubah*
> set 1 galon 19lt _(konversi)_
> set satuan beras jadi kg
> set kategori galon jadi MINUMAN
> set stok bensin tidak _(pembelian tidak menambah stok)_`,

	"transaksi": `💸 *Bantuan: Transaksi*
Sebut nominalnya (15rb / 500k / 1,5jt), barangnya harus sudah terdaftar.

*Pengeluaran*
> beli beras 5kg 75rb
> bayar listrik 200rb
> beli kopi 15rb kemarin
> beli gula 1kg 18rb 01/08

*Pemasukan*
> gaji masuk 10jt
> jual sepeda 250rb
> saldo awal 5jt _(posisi kas awal)_

*Riwayat beli*
> pembelian terakhir beras
> harga beli terakhir galon`,

	"stok": `📦 *Bantuan: Cek Stok*
> stok _(ringkasan per kategori)_
> stok beras _(per barang)_
> stok minuman _(per kategori)_
> masih ada susu?

Barang stok bertambah otomatis saat dibeli dan berkurang saat dipakai.`,

	"konsumsi": `🔄 *Bantuan: Pemakaian & Konsumsi*
*Catat pemakaian*
> pakai beras 1 kg
> pakai galon 3lt _(dikonversi via master)_
> galon sudah habis _(tutup batch + hitung rata-rata/hari)_

*Lihat*
> konsumsi _(barang yang sedang dipakai)_
> konsumsi susu
> history konsumsi susu

*Koreksi*
> terpakai susu (AUG-12-152714) 100ml`,

	"laporan": `📊 *Bantuan: Laporan*
> pengeluaran hari ini
> total pemasukan bulan ini
> pengeluaran per item kemarin
> ringkasan minggu lalu
> sisa uang berapa
> pengeluaran 01/08 s/d 11/08`,
}

// HelpMessage mengembalikan panduan untuk topik tertentu; topik kosong
// atau tidak dikenal → panduan utama.
func HelpMessage(topic string) string {
	if msg, ok := HelpTopics[strings.ToLower(strings.TrimSpace(topic))]; ok {
		return msg
	}
	return OnboardingTemplate
}

// PreInitMessage dikirim ke pengirim yang belum melakukan init eksplisit.
const PreInitMessage = `Halo! Akun kamu belum aktif.
Ketik "init" untuk mulai mengaktifkan pencatatan keuangan & inventaris.
Opsional: "init <nama ledger>" (mis. "init personal cash flow").`

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

// Note: parseInitCommand, isHelpCommand, isInfoCommand functions have been removed
// as they are now handled by LLM-based intent classification in the refactored Agent.Process() method.
