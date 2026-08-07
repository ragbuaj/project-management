---
type: session
project: project-management
phase: build
date: 2026-08-07
---

# Sesi: sqlc dan fondasi HTTP

Lanjutan dari [sesi skema](2026-08-07-fase-0-skema-lengkap.md), yang berhenti
setelah skema Fase 0 lengkap. Sesi ini menyelesaikan Langkah 12 dan 13.

## Dikerjakan

Enam PR ter-merge, semuanya dengan CI hijau:

| PR | Isi | Commit |
|---|---|---|
| #19 | `sqlc` + repository identity (Langkah 12) | `5980db1` |
| #21 | Perbaikan balapan data `goose` | `79f5a90` |
| #20 | Request ID + bentuk error | `aa9be4b` |
| #22 | Recovery + log permintaan + pemasangan di `cmd/api` | `bab44aa` |
| #23 | Klien Redis + `REDIS_URL` | `67c2fb4` |
| #24 | Rate limiter + middleware-nya (Langkah 13 tuntas) | `16b6a19` |

Branch `feat/migration-cards` yang tertinggal dari PR #13 dihapus setelah
diverifikasi isinya identik di `main`.

## Diverifikasi

Pada `16b6a19`:

```
$ go test ./...
ok  	internal/config	(cached)
ok  	internal/httpx	(cached)
ok  	internal/modules/identity/repository	0.576s
ok  	internal/postgres	2.582s
ok  	internal/redis	(cached)

$ golangci-lint run
0 issues.

$ sqlc diff
(exit 0)
```

110 test di `backend/internal`.

Tiga test dibuktikan menggigit, bukan sekadar hijau:

```
# view soft-delete diganti tabel di query sqlc
--- FAIL: TestASoftDeletedUserIsInvisibleToEveryRead
    GetUserByID() on a masked account returned <nil>, want no rows
    ListUsers() returned 2 users after the delete, want 1

# urutan middleware dibalik
--- FAIL: TestAPanickingRequestStillAppearsInTheRequestLog
    a panicking request produced no request log line

# advisory lock dilepas dari Migrate
--- FAIL: TestConcurrentMigrationsDoNotCollide
    duplicate key value violates unique constraint "pg_type_typname_nsp_index"
```

Aplikasi dijalankan terhadap Redis yang tidak ada, untuk membuktikan klaim
`nfr.md` bahwa aplikasi tetap jalan:

```
level=WARN msg="redis is not answering; realtime and caching are degraded
  and the login rate limit fails closed"
level=INFO msg="http server listening" addr=127.0.0.1:8099
healthz=200  readyz=200
X-Request-Id: 04d1cab4cef2aaed026652107960d04a
```

## Belum selesai

Langkah 14–26. Berikutnya Langkah 14: `internal/fracdex` + property test
(ADR-0003).

## Penghalang

Tidak ada.

## Keputusan yang diambil

- **`omit_unused_structs` menyala di `sqlc.yaml`.** Tanpa itu, satu skema
  bersama ditambah satu entri per modul berarti setiap modul mendapat struct
  untuk semua 33 tabel — `Card`, `Sprint`, dan `VcsConnection` di paket identity
  yang tidak pernah menanyakannya. Itu persis duplikasi yang ADR-0008 §4 cegah.
- **sqlc dipasang lewat `go install` berversi tetap, bukan action pihak ketiga.**
  Modul Go diverifikasi checksum database, jadi tidak ada hash yang perlu
  ditulis tangan.
- **Urutan middleware `RequestID → LogRequests → Recover`.** Versi pertama saya
  terbalik; lihat temuan di bawah.
- **`FailClosed` adalah nilai nol `OnLimiterFailure`.** Call site yang lupa
  memilih mendapat paruh yang aman.
- **Rate limiter memakai fixed window dengan script Lua.** `INCR` lalu `PEXPIRE`
  terpisah punya celah di mana key kehilangan expiry selamanya.
- **`REDIS_URL` wajib**, walau aplikasi tetap jalan saat Redis mati. Dua
  kegagalan yang berbeda.
- **Tidak ada helper `ClientIP`.** Pertanyaan `X-Forwarded-For` belum terjawab
  dan Caddy baru ada di Langkah 25; mengirimkannya sekarang mengunci jawaban
  yang mungkin salah.
- **Langkah 13 dipecah jadi empat PR.** Ditulis sekaligus jadi 873 baris.

## Yang gerbangnya tangkap di sesi ini

| Gerbang | Temuan |
|---|---|
| `go test -race` di CI | **Balapan data di `Migrate`.** Advisory lock dari PR #19 menserialkan skema antar-proses, tapi goose menyimpan dialect dan filesystem sebagai variabel tingkat paket yang ditulis di setiap panggilan. PR #19 lolos padahal kodenya sama — deteksi balapan bersifat sampling |
| CI, bukan lokal | **sqlc 1.31.1 menuntut Go 1.26**, sedangkan repo memakai 1.25.12 dan `setup-go` mengunci `GOTOOLCHAIN=local`. Lokal tidak kena karena binary saya prebuilt |
| `contextcheck` | Closure `defer` di `Recover` tidak meneruskan context. Diangkat jadi fungsi bernama — lebih rapi juga |
| `misspell` | Tujuh ejaan Britania (`serialises`, `catalogue`, `behaviour`) |
| `noctx` | `admin.Exec` tanpa context di test |
| `importas` | Test dari paketnya sendiri tetap wajib memakai alias `identityrepo` |
| Menjalankan test | go-redis menulis diagnostiknya ke stderr sebagai teks biasa, satu-satunya baris yang tidak bisa di-parse collector JSON — dan munculnya persis saat Redis bermasalah |
| Membaca ulang | Deskripsi PR #17 menulis "34 tabel"; hitungan sebenarnya 33 |

## Penalaran saya yang ternyata salah

Dicatat supaya tidak diulang dari komentar lama:

**Urutan middleware.** Saya menulis `RequestID → Recover → LogRequests` dengan
komentar yang mengklaim panic tetap tercatat sebagai permintaan selesai. Panic
meng-unwind **setiap** frame di atasnya, jadi `LogRequests` yang berada di dalam
`Recover` tidak pernah sampai ke baris log-nya. Ditemukan saat menulis test yang
seharusnya membuktikan klaim itu.

## Yang perlu diingat sesi berikutnya

- **`main` tidak punya branch protection.** `gh pr merge --auto` langsung merge,
  tidak menunggu CI. Urutan yang benar: `gh pr checks --watch` dulu, baru merge.
- **`go test -race` tidak bisa dijalankan lokal** — detektornya menuntut cgo dan
  tidak ada gcc di mesin ini. Untuk apa pun yang menyentuh konkurensi, CI-lah
  verifikasinya, dan satu run hijau bukan bukti.
- **Database test lokal sudah ter-migrate**, jadi balapan migrasi antar-paket
  tidak pernah muncul lokal. CI mulai dari database kosong.
- Test skema butuh `TEST_DATABASE_URL`; test Redis butuh `TEST_REDIS_URL`. Tanpa
  keduanya test-nya **skip** dan hasilnya tetap terlihat hijau.
- `cmd/api` sekarang juga butuh `REDIS_URL` di environment.
- `.env` lokal memakai **port 55432 dan 56379**.
- Komentar kode Go **bahasa Inggris**, dan `misspell` menolak ejaan Britania.
- Pesan commit multi-baris di PowerShell 5.1: `git commit -F berkas`.

## Yang menunggu jawaban pemilik

Tiga, semuanya ada di [progress.md](../progress.md):

1. Branch protection di `main` — belum ada sama sekali.
2. Angka rate limit untuk login, reset password, dan pencarian.
3. Cara menentukan IP klien di belakang Caddy.
