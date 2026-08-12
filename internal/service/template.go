package service

// OnboardingTemplate dikirim saat user pertama kali mengontak
// atau meminta bantuan. Menjelaskan format pencatatan.
const OnboardingTemplate = `Halo! Saya asisten pencatat keuangan & inventaris kamu.

Cukup kirim pesan seperti biasa, contoh:

PENGELUARAN:
> Beli bensin 50rb
> Beli susu UHT 1 dus isi 50pcs harga 500rb
> Kecap 250ml X 5 botol 100rb
> Bayar listrik 200rb

PEMASUKAN:
> Gaji masuk 10jt
> Jual barang 250rb
> Saldo awal 5jt (catat posisi kas awal, muncul terpisah dari pemasukan)

PEMAKAIAN STOK:
> Ambil susu UHT 2 pcs
> Pakai air mineral 1 pcs

ANALISA KONSUMSI (ADVANCED):
> Kopi 100g beli 10pcs tanggal 11/08 tapi habis 500g di 30/08
> Susu 200ml beli 12 botol tanggal 01/08 habis 1 liter di 20/08
> Beras 5kg beli 2 karung tanggal 05/08 habis 3kg di 25/08

LAPORAN (tanya aja):
> Pengeluaran hari ini berapa?
> Total pemasukan bulan ini
> Pengeluaran per item kemarin
> Barang saya apa aja? (sisa stok)
> Hari ini pakai apa aja? (stok keluar)
> Ringkasan kemarin
> Pemakaian barang minggu ini
> Pemakaian barang per item kemarin

QUERY STOK (semua variations):
> stok
> sisa
> persediaan
> inventaris
> persedian (dengan typo)
> inventori (dengan typo)
> stok air (filter per item)
> sisa kecap (filter per item)
> barang popok (filter per item)
> persediaan susu (filter per item)
> daftar barang (semua stok)
> barang saya apa aja (semua stok)

Tips:
- Sebut nominal (50rb / 500k / 5jt) -> tercatat pengeluaran.
- Sebut grosir (1 dus isi 50pcs) -> dikonversi otomatis.
- Tanpa nominal (ambil/pakai) -> tercatat pemakaian stok.
- Analisa konsumsi -> otomatis hitung durasi & rate pemakaian per hari.

Ketik "bantuan" kapan saja untuk lihat panduan ini lagi.
Ketik "info" untuk melihat detail sesi/chat (debugging).
Init ulang dengan nama: init <nama ledger> (mis. "init personal cash flow").`

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
