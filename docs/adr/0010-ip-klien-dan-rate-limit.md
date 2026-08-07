# ADR-0010: IP klien dihitung dari kanan, dan rate limit berlapis

**Status:** Accepted
**Tanggal:** 2026-08-07
**Pengambil keputusan:** pemilik proyek
**Melengkapi:** [ADR-0005](0005-autentikasi-sesi-cookie.md), yang mewajibkan
rate limit tanpa menetapkan angkanya maupun kunci penghitungnya.

## Konteks

Mekanisme rate limit sudah ada sejak PR #24 — fixed window di Redis, dengan
script Lua — tapi dua hal belum diputuskan dan keduanya memblokir Langkah 18
dan 25:

1. **Angka.** Tidak ada satu pun dokumen yang menuliskan berapa percobaan login
   yang diizinkan.
2. **Kunci.** Membatasi "per IP" tidak berarti apa-apa sampai ada definisi
   tentang IP mana. Aplikasi ini akan berada di belakang Caddy.

Keduanya sulit dibalik dengan cara yang berbeda. Definisi IP klien adalah
**batas kepercayaan**: salah menetapkannya berarti siapa pun bisa memalsukan
identitas jaringannya, dan kesalahan itu tidak terlihat sampai ada yang
memanfaatkannya. Angka rate limit lebih mudah diubah, tapi bentuk penegakannya
— throttle atau lockout — menentukan apakah fitur ini bisa dipakai untuk
menyerang pengguna lain.

## Keputusan bagian 1 — IP klien

**Hanya alamat socket yang tidak bisa dipalsukan.** `X-Forwarded-For`,
`X-Real-IP`, dan `True-Client-IP` semuanya dikirim klien dan bisa berisi apa
saja.

`X-Forwarded-For` bertambah dari kiri ke kanan, jadi bagian kiri justru yang
paling tidak dipercaya:

```
XFF: 1.2.3.4, 5.6.7.8, 10.0.0.5
                       ^ ditambahkan LB kami — tepercaya
              ^ IP klien sebenarnya
     ^ palsu, dikirim klien
```

Karena itu perhitungannya **dari kanan**:

```
kalau alamat socket bukan proxy tepercaya  → pakai alamat socket, abaikan header
kalau tepercaya                            → jalan mundur di rantai XFF,
                                             hop tak-tepercaya pertama = klien
```

**Jumlah proxy tepercaya harus tetap dan diketahui.** Kalau tidak, penyerang
tinggal mengirim XFF yang panjang untuk menggeser posisi yang kami baca.

Konsekuensi konfigurasi, yang jatuh tempo di Langkah 25:

- Caddy sudah menulis `X-Forwarded-For`. Daftar CIDR yang dipercaya aplikasi
  adalah **alamat Caddy saja**, ditulis eksplisit di environment, bukan
  "semua alamat privat".
- Kalau suatu saat ada CDN di depan, origin **wajib** difirewall agar hanya
  menerima koneksi dari rentang CDN itu. Tanpa itu penyerang melewati CDN dan
  mengirim header sendiri.
- `Forwarded` (RFC 7239) adalah standar yang lebih baik tapi dukungannya masih
  kalah luas; XFF yang dipakai.

Untuk keperluan rate limit, IP dinormalisasi sebelum jadi kunci: buang port,
buang kurung siku IPv6, huruf kecil, dan tolak nilai yang bukan IP.

**IPv6 diagregasi ke /64.** Satu pelanggan biasanya menerima /64 utuh —
miliaran alamat gratis untuk memutar limit yang dihitung per alamat.

**IPv4 di Indonesia sering CGNAT.** Satu alamat bisa mewakili ribuan pengguna
seluler, jadi IP tidak pernah menjadi satu-satunya kunci: akun, IP, dan prefiks
IP adalah ember yang terpisah.

**IP adalah data pribadi.** Untuk log jangka panjang ia disimpan sebagai hash
bergaram atau dipotong — /24 untuk IPv4, /48 untuk IPv6 — dengan masa retensi
yang ditetapkan. Ini juga yang menjelaskan kenapa `sessions.ip_hash` masih
kosong: kolom itu menunggu digest berkunci, bukan SHA-256 polos yang hanya
punya empat miliar preimage.

## Keputusan bagian 2 — rate limit

**Beberapa ember sekaligus, dan throttling lebih dulu daripada penguncian.**
Penguncian keras adalah vektor DoS terhadap pengguna lain: siapa pun yang tahu
alamat surel seseorang bisa mengunci akunnya.

NIST menetapkan plafon 100 percobaan gagal beruntun per akun. Itu plafon, bukan
target; angka di bawah ini jauh di bawahnya.

| Ember | Angka awal | Aksi saat terlampaui |
|---|---|---|
| Gagal per akun | 5 / 15 menit | backoff eksponensial: 1s, 2s, 4s… maksimum 30s |
| Gagal per akun (agresif) | 20 / jam | kunci sementara 15–30 menit + surel pemberitahuan |
| Gagal per IP | 30 / 10 menit | 429 + `Retry-After` |
| Gagal per IP (harian) | 100–300 / hari | blokir sementara atau tantangan CAPTCHA |
| Verifikasi OTP/TOTP | 5 percobaan per kode | kode dibatalkan, minta kirim ulang |
| Permintaan reset password | 3 / jam per akun, 10 / jam per IP | 429 |
| Pendaftaran | 5 / jam per IP | CAPTCHA |

Angka-angka ini adalah **titik awal yang bisa disetel**, bukan konstanta yang
suci. Yang tidak bisa disetel adalah bentuknya.

Yang mudah terlewat, dan karena itu ditulis di sini:

- **Penghitung gagal direset saat login berhasil**, tapi peristiwanya tetap
  dicatat untuk deteksi anomali.
- **CAPTCHA atau proof-of-work adaptif setelah tiga kegagalan**, sebelum
  penguncian dijatuhkan.
- **Deteksi credential stuffing butuh metrik global, bukan per akun.** Rasio
  gagal/sukses yang melonjak, atau satu IP/ASN yang menyentuh banyak akun
  berbeda — pola ini lolos dari setiap limit per akun.
- **Tidak boleh ada enumerasi akun.** Jawaban *dan waktu jawab* untuk "surel
  tidak terdaftar" harus identik dengan "password salah", **termasuk saat
  terkena rate limit**. Karena itu hash dummy tetap dijalankan ketika akun
  tidak ditemukan.
- **Sliding window atau token bucket**, bukan fixed window. Fixed window bisa
  dilewati dua kali limit di perbatasan jendela. Implementasi yang ada sekarang
  memakai fixed window, jadi ini adalah pekerjaan yang sudah diketahui.
- **429 dengan `Retry-After`**, dan penghitungan terpusat di Redis, bukan di
  memori tiap instance.

## Konsekuensi

### Yang menjadi lebih mudah

- Rate limit yang tidak bisa dipakai mengunci orang lain dari akunnya.
- Kunci yang berarti sesuatu di jaringan yang sebenarnya dipakai pengguna
  Indonesia, bukan hanya di jaringan yang rapi.

### Yang menjadi lebih sulit

- **Rate limiter yang ada harus diganti dari fixed window ke sliding window.**
  Itu pekerjaan tambahan di Langkah 18 yang sebelumnya tidak ada di rencana.
- Ember berlapis berarti beberapa penghitung per percobaan login, bukan satu.
- Daftar CIDR tepercaya menjadi konfigurasi yang salah-setelnya senyap: kalau
  terlalu longgar, XFF bisa dipalsukan; kalau kosong, semua pengguna terhitung
  sebagai satu IP.

### Yang perlu diawasi

- Jumlah 429 di jalur login. Nol berarti limitnya terlalu longgar; lonjakan
  berarti serangan atau salah setel.
- Berapa banyak akun berbeda yang disentuh satu IP/ASN dalam satu jam.
- Berapa banyak permintaan yang datang dengan XFF berisi lebih banyak hop
  daripada jumlah proxy yang kami punya. Selain nol, artinya ada yang mencoba.
