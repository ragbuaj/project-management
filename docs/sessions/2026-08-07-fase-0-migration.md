---
type: session
project: project-management
phase: build
date: 2026-08-07
---

# Sesi: Migration pertama, dan gerbang yang mulai menggigit

Lanjutan dari [2026-08-06](2026-08-06-fase-0-fondasi.md). Dari skema kosong
sampai sepuluh tabel.

## Dikerjakan

Enam PR ter-merge, semuanya dengan CI hijau:

| PR | Isi | Commit |
|---|---|---|
| #6 | Pool `pgx`, `/readyz`, restrukturisasi CI | `5be8ccd` |
| #7 | Catatan batas platform GitHub | `216672e` |
| #8 | `progress.md` + log sesi pertama | `ac65098` |
| #9 | Rekonsiliasi progres | `52239fc` |
| #10 | goose, `cmd/migrate`, migration `00001` identitas | `c9a5ea1` |
| #11 | Migration `00002` project dan board | `f438d97` |

Langkah 8–11 dipecah jadi tujuh PR (A–G) atas permintaan pemilik. A dan B
selesai; C–G belum.

## Diverifikasi

Pada `f438d97`:

```
$ pnpm dlx squawk-cli --pg-version=18.0 backend/db/migrations/*.sql
Found 0 issues in 2 files 🎉

$ golangci-lint run
0 issues.

$ go test ./...
ok  	internal/config	1.045s
ok  	internal/httpx	2.725s
ok  	internal/postgres	0.527s

$ go run ./cmd/migrate status
    Applied At                  Migration
    =======================================
    Fri Aug  7 03:57:10 2026 -- 00001_identity.sql
```

CI PR #11, tiga job hijau — `migrations.yml` ikut menyala karena PR-nya
menyentuh `backend/db/migrations/**`, dan diam di PR yang tidak.

Test yang menjaga tabel yang belum ada, tanpa diubah sama sekali:

```
--- PASS: TestLiveViewsMatchTheirTables/boards
--- PASS: TestLiveViewsMatchTheirTables/projects
--- PASS: TestLiveViewsMatchTheirTables/users
```

## Belum selesai

Langkah 9b, 10, 11a, 11b, 11c — migration `00003` sampai `00007`. Lalu 12–26.

## Penghalang

Tidak ada.

## Berikutnya

**PR C — migration `00003`: `sprints`, `cards`, FK komposit.** Tabel terpenting
di sistem ini. Yang harus ada di dalamnya:

- `position` sebagai fractional index `text COLLATE "C"`, unik per
  `(project_id, status_id)` untuk baris yang belum dihapus dan belum diarsipkan
  (ADR-0003)
- FK komposit `(status_id, project_id) REFERENCES statuses (id, project_id)` —
  `statuses` sudah membawa `UNIQUE (id, project_id)` sejak `00002`
- Kolom nullable yang disiapkan sekarang untuk fase jauh: `sprint_id`,
  `epic_id`, `parent_card_id`, `start_date`, `estimate_points`
- `search_tsv` sebagai generated column **`STORED` eksplisit** — di PostgreSQL
  18 nilai bawaannya `VIRTUAL`, dan kolom virtual tidak bisa diindeks GIN
- Lima invarian yang tidak bisa jadi constraint dan harus ditegakkan di service,
  didaftar ulang sebagai komentar di migration supaya tidak hilang
- `cards_live` dengan daftar kolom eksplisit

Setelah PR D, lakukan rekonsiliasi progres lagi.

## Keputusan yang diambil

- **Langkah 8–11 dipecah jadi tujuh PR.** Menjawab pertanyaan ukuran PR yang
  menggantung sejak 2026-08-06.
- **Penyaringan CI pindah ke level workflow.** Job kontrak API dan migration
  punya berkas sendiri dengan `on.pull_request.paths`, jadi runner-nya tidak
  dinyalakan untuk PR yang tidak menyentuh berkasnya.
- **`GO_VERSION` dihapus dari CI** — semua job baca `go-version-file:
  backend/go.mod`, jadi versi Go punya satu sumber.
- **`.squawk.toml`** mengecualikan `prefer-robust-stmts` saja, dengan alasan
  tertulis. `require-concurrent-index-creation` dan `prefer-bigint-over-int`
  dimatikan per-baris, sehingga tetap menyala di tempat yang berbahaya.
- **Constraint `UNIQUE` dideklarasikan di dalam `CREATE TABLE`**, bukan lewat
  `ALTER TABLE` — yang terakhir mengambil `ACCESS EXCLUSIVE` lock.
- **Migration wajib membuka dengan `SET lock_timeout` dan
  `SET statement_timeout`.** Ditemukan squawk, dan benar.

## Yang gerbangnya tangkap di sesi ini

Ditulis supaya tidak ada yang menganggap gerbang ini formalitas:

| Gerbang | Temuan |
|---|---|
| `govulncheck` | `GO-2026-5970` di `golang.org/x/text` lewat `pgxpool` — menolak push |
| `squawk` | `lock_timeout`/`statement_timeout` hilang; `ALTER TABLE ADD CONSTRAINT UNIQUE` mengambil lock berat |
| `golangci-lint` | Bersih setelah sesi sebelumnya |
| Test | **`sql.Open` tidak mem-parse DSN** — versi pertama `Migrate` akan mencetak password database ke log deployment mana pun yang start dengan `DATABASE_URL` salah bentuk |
| Test | Bug di test saya sendiri: PostgreSQL membatalkan seluruh transaksi setelah satu pernyataan gagal, jadi butuh savepoint |

## Yang perlu diingat sesi berikutnya

- `.env` lokal memakai **port 55432 dan 56379**. Service Windows
  `postgresql-x64-18` membayangi container di 5432, dengan galat menyesatkan
  `password authentication failed`.
- **Docker Desktop mati setiap kali mesin restart.** Nyalakan sebelum menjalankan
  test yang menyentuh database; tanpa itu delapan test gagal dengan galat
  koneksi yang terlihat seperti masalah kode.
- Komentar kode Go **bahasa Inggris** — `misspell` menolak bahasa Indonesia,
  dan menolak ejaan Britania (`cancelled`, `honoured`).
- Pesan commit multi-baris di PowerShell 5.1: `git commit -F berkas`, bukan
  here-string, kalau memuat tanda kutip ganda.
- Pragma `-- squawk-ignore` harus tepat di atas **baris yang dilaporkan**,
  bukan di atas pernyataannya. JSON squawk memakai indeks baris **0-based**.
- Container compose ditinggalkan hidup.
