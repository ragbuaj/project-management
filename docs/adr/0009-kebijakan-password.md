# ADR-0009: Kebijakan password — panjang, bukan komposisi

**Status:** Accepted
**Tanggal:** 2026-08-07
**Pengambil keputusan:** pemilik proyek
**Melengkapi:** [ADR-0005](0005-autentikasi-sesi-cookie.md), yang menetapkan
Argon2id tapi tidak menetapkan apa pun tentang password itu sendiri.

## Konteks

[ADR-0005](0005-autentikasi-sesi-cookie.md) menuliskan "Password: Argon2id" dan
berhenti di situ. Langkah 15a sudah mengirimkan hashing-nya, dan saat menulis
Langkah 15c — layar dan endpoint login — pertanyaannya tidak bisa dihindari
lagi: password sepanjang apa yang diterima, dan aturan apa yang berlaku.

Pertanyaan ini terbuka di [progress.md](../progress.md) sejak 2026-08-07 dan
memblokir Langkah 15c.

Kebijakan password sulit dibalik ke arah yang lebih ketat: menaikkan panjang
minimum berarti memaksa seluruh pengguna yang sudah ada mengganti password,
dan aturan komposisi yang sudah terpasang akan tercermin di ribuan password
yang tidak bisa diperiksa ulang. Karena itu ia diputuskan sekarang, bukan saat
layar login ditulis.

## Acuan

NIST SP 800-63B dan OWASP ASVS. Keduanya bergerak ke arah yang sama dalam satu
dekade terakhir, dan arah itu berlawanan dengan yang diajarkan kebanyakan
aplikasi: **panjang adalah satu-satunya syarat yang benar-benar menambah
entropi**; sisanya menghasilkan `Password1!`.

## Keputusan

| Aspek | Nilai | Alasan |
|---|---|---|
| Panjang minimum | **12 karakter** | Minimum praktis OWASP ASVS. NIST menetapkan 8 sebagai batas absolut dan menyarankan 15+; 12 adalah titik yang bisa ditegakkan tanpa membuat orang menuliskan password di kertas |
| Panjang maksimum | **≥ 64 karakter**, dipotong **tidak pernah diam-diam** | Batas teknis kami 1024 byte, jauh di atas 64. Password yang dipotong diam-diam adalah password yang berbeda dari yang diketik, dan pemiliknya tidak akan pernah tahu kenapa login gagal di perangkat lain |
| Karakter yang diterima | **Semua yang printable** — ASCII, Unicode, spasi | Melarang karakter berarti mengecilkan ruang pencarian yang sedang kita perbesar |
| Normalisasi | **NFKC sebelum hashing dan sebelum verifikasi** | Tanpa itu, password yang sama yang diketik di papan ketik berbeda menghasilkan byte berbeda dan gagal login |
| Aturan komposisi | **Tidak ada** | Terbukti menghasilkan `Password1!`. Ia memindahkan beban dari penebak ke pengguna tanpa menambah entropi nyata |
| Rotasi berkala | **Tidak ada** | Ganti dipaksa hanya kalau ada indikasi kompromi. Rotasi berkala menghasilkan `Password1!` → `Password2!` |
| Blocklist | Password yang pernah bocor, lewat **HIBP Pwned Passwords dengan k-anonymity** (kirim 5 karakter pertama SHA-1, bukan password), ditambah daftar kata terkait merek dan domain | Password sepanjang 12 karakter yang sudah ada di daftar bocor tidak lebih aman daripada password 6 karakter |
| Paste dan "lihat password" | **Diizinkan** | Melarang paste melarang password manager, yaitu melarang password yang baik |
| Pertanyaan keamanan | **Tidak dipakai** | Jawabannya biasanya bisa dicari, dan ia menjadi jalur masuk yang lebih lemah daripada password yang dilindunginya |

Penyimpanan tetap seperti ADR-0005 dan seperti yang sudah dikirim di PR #29:
**Argon2id, m=19 MiB, t=2, p=1** — persis parameter minimum yang dirujuk acuan
di atas.

### Yang tidak dipilih, dan kenapa

- **bcrypt.** Diizinkan `rules/40-security.md` dengan cost ≥ 12, tapi punya
  batas 72 byte. Mengizinkan password panjang di atasnya menuntut pra-hash,
  dan pra-hash dengan SHA-256 polos membuka *password shucking* — pra-hash
  yang benar adalah HMAC-SHA-256 berkunci, yang berarti satu secret lagi untuk
  dikelola. Argon2id tidak punya persoalan ini sama sekali.
- **scrypt** (N=2^17, r=8, p=1). Setara secara keamanan, tapi tidak ada alasan
  untuk mengganti yang sudah terkirim.

## Konsekuensi

### Yang menjadi lebih mudah

- Aturan yang bisa dijelaskan dalam satu kalimat: minimal 12 karakter, apa saja
  boleh. Layar login tidak perlu daftar syarat.
- Password manager bekerja tanpa perlawanan.

### Yang menjadi lebih sulit

- **Blocklist butuh panggilan keluar.** HIBP adalah dependensi eksternal di
  jalur pendaftaran dan penggantian password. Ia harus punya timeout dan harus
  **gagal terbuka** — layanan yang mati tidak boleh menghentikan orang
  mengganti password, karena itu justru yang dilakukan orang setelah curiga
  akunnya bocor.
- **NFKC harus konsisten selamanya.** Kalau normalisasi berubah, seluruh hash
  yang ada berhenti cocok. Ia bukan detail implementasi yang bisa diganti diam-
  diam.
- 12 karakter akan terasa panjang bagi sebagian orang. UI perlu mendorong
  passphrase, bukan sekadar menolak.

### Yang perlu diawasi

- Berapa banyak pendaftaran yang tertolak blocklist. Angka tinggi berarti
  petunjuk di UI belum jelas.
- Waktu jawab HIBP. Ia berada di jalur permintaan pengguna.

### Yang ditunda, dan disebut di sini supaya tidak hilang

**Passkey (WebAuthn) atau MFA adalah mitigasi terbesar untuk credential
stuffing** — lebih besar daripada pengetatan aturan password mana pun. Tidak
ada di Fase 0 sampai Fase 9 mana pun saat ini. Kalau roadmap memungkinkan,
investasi ke sana bernilai lebih tinggi daripada seluruh tabel di atas.
