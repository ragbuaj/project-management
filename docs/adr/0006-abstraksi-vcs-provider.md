# ADR-0006: Satu interface `VcsProvider` untuk GitHub dan GitLab

**Status:** Proposed
**Tanggal:** 2026-08-06
**Pengambil keputusan:** pemilik proyek

## Konteks

Fase 9 memuat H4 (integrasi GitHub) dan H4b (integrasi GitLab). Keduanya
diminta di tahap perencanaan, bukan sebagai tambahan belakangan — itu penting,
karena keputusan ini hanya murah kalau diambil sebelum yang pertama ditulis.

Kebutuhannya sama untuk kedua penyedia:

- Menautkan sebuah kartu ke issue, pull request / merge request, branch, atau
  commit
- Menerima webhook saat objek tertaut berubah, lalu memperbarui kartu
- Menyebut nomor kartu (`PM-142`) di judul branch, commit, atau MR/PR, lalu
  otomatis membuat tautan
- Menampilkan status objek tertaut di antarmuka kartu

Perbedaannya nyata tapi dangkal: penamaan (PR vs MR), bentuk payload webhook,
mekanisme verifikasi signature (HMAC-SHA256 di header `X-Hub-Signature-256`
untuk GitHub; token pembanding di header `X-Gitlab-Token` untuk GitLab), model
autentikasi (GitHub App atau PAT; GitLab PAT atau project access token), dan
penomoran (GitHub berbagi ruang nomor antara issue dan PR; GitLab tidak).

## Opsi yang dipertimbangkan

### Opsi A — Dua integrasi terpisah

Tulis GitHub dulu, GitLab menyusul dengan kodenya sendiri.

Kekurangan: aturan bisnis yang sama — parsing nomor kartu, idempotensi webhook,
pembuatan `vcs_links`, penulisan activity event, penanganan retry — akan
ditulis dua kali dan berbeda dalam detail. Perbedaan itulah yang jadi bug.

### Opsi B — Satu interface, dua implementasi tipis

Aturan bisnis ditulis sekali. Penyedia hanya menyediakan pemetaan.

Kekurangan: risiko abstraksi yang dipaksakan kalau perbedaan ternyata lebih
dalam dari dugaan. Dimitigasi dengan menyimpan payload mentah dan tidak
memaksa setiap konsep ke bentuk yang sama.

## Keputusan

**Opsi B**, dengan batas yang ditarik di titik yang sempit.

```go
// internal/vcs — interface didefinisikan di sisi konsumen (rules/20-go.md)

type Provider interface {
    // Name mengembalikan "github" atau "gitlab".
    Name() string

    // VerifyWebhook memeriksa keaslian permintaan masuk dan mengembalikan
    // payload yang sudah dinormalisasi. Body mentah tetap disimpan pemanggil.
    VerifyWebhook(r *http.Request, secret string) (Event, error)

    // FetchRef mengambil keadaan terkini satu objek yang ditautkan.
    FetchRef(ctx context.Context, conn Connection, ref Ref) (RefState, error)
}

// Ref menunjuk satu objek di repositori.
type Ref struct {
    Kind     RefKind // issue | change_request | branch | commit
    ExternalID string
}
```

Penetapan yang menyertainya:

1. **`change_request` adalah nama netral** untuk pull request dan merge
   request, dipakai di database, kode, dan API. Antarmuka menampilkan istilah
   asli penyedia. Ini masuk [glossary.md](../glossary.md).
2. **Parsing nomor kartu (`PM-142`) tinggal di luar penyedia**, di
   `internal/vcs`. Aturannya sama untuk semua penyedia dan tidak boleh
   berbeda.
3. **Idempotensi webhook ditegakkan di lapisan bersama**, memakai pengenal
   pengiriman dari penyedia (`X-GitHub-Delivery` / `X-Gitlab-Event-UUID`),
   disimpan di tabel dengan `UNIQUE`. Webhook pasti dikirim ulang; retry itu
   pasti terjadi, bukan mungkin.
4. **Payload mentah disimpan** di `vcs_webhook_deliveries` selama 30 hari.
   Kalau normalisasi ternyata salah, datanya masih ada untuk diproses ulang —
   tanpa ini, bug pemetaan berarti kehilangan data permanen.
5. **Verifikasi signature adalah tanggung jawab penyedia**, dan wajib
   dijalankan sebelum body disentuh logika mana pun.
6. **Kredensial per koneksi disimpan terenkripsi** (AES-GCM dengan kunci dari
   environment), bukan sebagai teks biasa, dan tidak pernah masuk log.
7. **GitHub ditulis lebih dulu, GitLab menyusul di fase yang sama.** Interface
   baru dianggap terbukti setelah implementasi kedua masuk tanpa mengubah
   tanda tangan method. Kalau ternyata harus berubah, itu sinyal abstraksinya
   salah dan lebih baik dipecah — bukan ditambal.

## Konsekuensi

### Yang menjadi lebih mudah

- Penyedia ketiga (Gitea, Bitbucket) menjadi satu berkas implementasi.
- Aturan idempotensi, retry, dan penulisan event diuji sekali.
- Test bisa memakai penyedia palsu tanpa menyentuh jaringan.

### Yang menjadi lebih sulit

- Fitur yang hanya ada di satu penyedia (misalnya GitHub Checks) tidak muat di
  interface ini. Kalau dibutuhkan, ia harus keluar dari abstraksi secara
  eksplisit, bukan dengan melebarkan interface sampai jadi gabungan
  semua penyedia.
- Ada satu lapisan tambahan yang harus dibaca saat menelusuri alur webhook.

### Yang perlu diawasi

- Jumlah method di `Provider`. Kalau tumbuh melewati lima, abstraksinya sedang
  gagal dan perlu ditinjau ulang.
- Rasio webhook yang ditolak signature-nya. Selain nol, artinya salah
  konfigurasi secret atau ada yang mengetuk.
- Rate limit API penyedia. GitHub dan GitLab keduanya membatasi; `FetchRef`
  harus menghormati header sisa kuota dan mundur, bukan mencoba terus.
