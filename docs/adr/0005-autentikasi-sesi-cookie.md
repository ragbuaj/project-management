# ADR-0005: Sesi opak di cookie `HttpOnly` untuk browser, Bearer token untuk program

**Status:** Accepted
**Tanggal:** 2026-08-06
**Pengambil keputusan:** pemilik proyek

## Konteks

Ada tiga jenis pemanggil yang berbeda kebutuhannya:

| Pemanggil | Fase | Sifat |
|---|---|---|
| SPA di browser | 1 | Rentan XSS. Berbagi origin dengan API ([ADR-0001](0001-stack-react-go-postgresql.md)) |
| Skrip / integrasi pemilik (H8) | 9 | Tidak punya browser, butuh kredensial panjang umur |
| Pengunjung share link (H7) | 9 | Tanpa akun, baca-saja, satu board |

`rules/40-security.md` melarang menyimpan token di `localStorage` karena bisa
dibaca XSS, dan mewajibkan mekanisme pencabutan sesi yang benar-benar mematikan
sesi di sisi server.

Pendaftaran mandiri adalah non-goal — pengguna dibuat lewat undangan.

## Opsi yang dipertimbangkan

### Opsi A — JWT di `localStorage`

Stateless, tidak perlu penyimpanan sesi.

Kekurangan: bisa dibaca XSS; **dilarang** oleh `rules/40-security.md`.
Pencabutan menuntut daftar-cabut di server, yang menghapus satu-satunya
keunggulan stateless. **Ditolak.**

### Opsi B — JWT di cookie `HttpOnly`

Tidak bisa dibaca skrip. Tapi pencabutan tetap butuh daftar di server, jadi
tetap ada state — dengan tambahan kerumitan penandatanganan, rotasi kunci, dan
klaim yang bisa basi. Untuk satu backend yang memang punya database, tidak ada
yang dibeli dengan biaya itu.

### Opsi C — Sesi opak di cookie `HttpOnly`, state di PostgreSQL

Cookie hanya berisi pengenal acak 256-bit. Seluruh keadaan sesi ada di tabel
`sessions`. Pencabutan adalah satu `DELETE`. Peran dan izin dibaca dari data
server setiap permintaan, bukan dari klaim yang bisa basi — sesuai
`rules/40-security.md`.

Kekurangan: satu pembacaan sesi per permintaan. Pada skala ini, satu query
berindeks (atau cache Redis 30 detik) tidak terasa.

## Keputusan

**Opsi C untuk browser, Bearer token untuk program, token bertanda tangan
untuk share link.**

### Sesi browser

| Aspek | Nilai |
|---|---|
| Cookie | `__Host-session`, `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/` |
| Isi | 256-bit acak dari `crypto/rand`, base64url |
| Penyimpanan | Tabel `sessions`, **hanya hash SHA-256**-nya yang disimpan |
| Umur | Idle 14 hari, absolut 90 hari. Diperbarui geser saat dipakai |
| Pencabutan | `DELETE FROM sessions` — satu sesi, atau semua sesi milik pengguna |
| Password | Argon2id (`rules/40-security.md`) |

`SameSite=Lax` dipilih, bukan `Strict`, karena `Strict` merusak pembukaan
tautan kartu dari Telegram (H3) dan dari GitHub/GitLab (H4) — pengguna akan
mendarat di halaman login walau sudah masuk.

### CSRF

Karena `SameSite=Lax` tidak melindungi permintaan `POST` lintas-situs pada
semua peramban lama, ditambahkan pertahanan kedua: **double-submit token**.
Cookie `csrf` (tanpa `HttpOnly`) harus cocok dengan header `X-CSRF-Token` pada
setiap `POST`, `PATCH`, `PUT`, `DELETE`. Middleware menolak yang tidak cocok
dengan `403`.

Permintaan yang membawa header `Authorization: Bearer` **dikecualikan** dari
pemeriksaan CSRF — token bukan kredensial ambien, jadi tidak ada yang bisa
dipalsukan lintas-situs.

### API token (H8, Fase 9)

| Aspek | Nilai |
|---|---|
| Bentuk | `pmt_<32 byte base64url>`, dikirim sebagai `Authorization: Bearer` |
| Penyimpanan | Hanya hash SHA-256 di `api_tokens`. Nilai asli ditampilkan **satu kali** saat dibuat |
| Cakupan | Daftar scope eksplisit (`cards:read`, `cards:write`, …). Tidak ada token yang otomatis penuh |
| Kedaluwarsa | Wajib diisi saat pembuatan, maksimum 1 tahun |
| Jejak | `last_used_at` diperbarui paling sering sekali per menit |

Token **tidak pernah** dipakai browser dan tidak pernah diterima dari query
string — hanya dari header. Query string bocor ke log akses dan riwayat
peramban.

### Share link (H7, Fase 9)

Token acak per tautan, hash-nya disimpan di `share_links`, punya
`expires_at` opsional dan bisa dicabut. Memberi akses **baca-saja** ke satu
board. Tidak membentuk sesi, tidak punya identitas, tidak bisa berkomentar.
Endpoint yang melayaninya terpisah (`/api/v1/public/...`) dan hanya
mengembalikan bentuk data yang sudah dipangkas — bukan endpoint biasa dengan
pengecualian otorisasi.

### Rate limit

Wajib pada login, undangan, reset password, dan pencarian — dibatasi per akun
**dan** per IP. Respons login yang salah selalu sama untuk "email tidak
terdaftar" dan "password salah", supaya tidak terjadi enumerasi akun.

## Konsekuensi

### Yang menjadi lebih mudah

- Logout benar-benar mematikan sesi, seketika, tanpa daftar-cabut.
- Peran selalu segar: perubahan peran berlaku di permintaan berikutnya, bukan
  setelah token kedaluwarsa.
- Tidak ada token di JavaScript, jadi XSS tidak langsung berarti pengambilalihan
  akun.
- Tanpa CORS dan tanpa refresh-token flow, karena satu origin.

### Yang menjadi lebih sulit

- Satu pembacaan sesi per permintaan. Perlu indeks pada hash token dan, kalau
  terasa, cache Redis dengan TTL pendek.
- Middleware CSRF harus ada di setiap route yang mengubah data. Ditegakkan
  dengan menerapkannya di level router, bukan per handler — supaya lupa
  memasang bukan pilihan yang mungkin.
- Fase 10 (PWA offline) tidak bisa mengandalkan token yang tersimpan di klien
  untuk bekerja offline. Yang bisa disinkronkan offline hanya data, bukan
  autentikasi; saat sesi kedaluwarsa aplikasi harus meminta login ulang sebelum
  mengirim perubahan yang tertunda.

### Yang perlu diawasi

- Jumlah sesi aktif per pengguna. Pertumbuhan tak wajar berarti kebocoran
  cookie atau bot.
- Kegagalan pemeriksaan CSRF di produksi. Selain nol, artinya ada yang salah
  konfigurasi atau ada yang mencoba.
- Token yang `last_used_at`-nya tidak pernah terisi setelah 30 hari — kandidat
  untuk dicabut.
