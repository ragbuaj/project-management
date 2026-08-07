---
type: session
project: project-management
phase: build
date: 2026-08-07
---

# Sesi: Skema Fase 0 ditutup

Lanjutan dari [sesi migration](2026-08-07-fase-0-migration.md), yang berhenti
setelah PR B. Sesi ini menyelesaikan C sampai G.

## Dikerjakan

Lima PR ter-merge, semuanya dengan CI hijau:

| PR | Isi | Commit |
|---|---|---|
| #13 | Migration `00003` — `sprints`, `cards`, FK komposit | `9fc7bb8` |
| #14 | Migration `00004` — `activity_events` berpartisi, `outbox` | `e22adf7` |
| #15 | Migration `00005` — comments, checklists, links, card_labels | `1572586` |
| #16 | Migration `00006` — notifikasi, waktu, filter, automation | `ba87e12` |
| #17 | Migration `00007` — token, share link, VCS | `ee404a7` |

Langkah 8–11 selesai. Skema Fase 0 lengkap: 33 tabel, 5 view `*_live`,
57 test di `internal/postgres`.

## Diverifikasi

Pada `ee404a7`:

```
$ pnpm dlx squawk-cli --pg-version=18.0 backend/db/migrations/*.sql
Found 0 issues in 7 files 🎉

$ golangci-lint run
0 issues.

$ go test ./...
ok  	internal/config	(cached)
ok  	internal/httpx	(cached)
ok  	internal/postgres	1.477s

$ go run ./cmd/migrate up
OK   00007_integration.sql (41.96ms)
goose: successfully migrated database to version: 7
```

Test yang menjaga view soft-delete, tanpa diubah sama sekali sejak PR #11 —
`cards` dan `comments` masuk sendiri begitu tabelnya ada:

```
--- PASS: TestLiveViewsMatchTheirTables/boards
--- PASS: TestLiveViewsMatchTheirTables/cards
--- PASS: TestLiveViewsMatchTheirTables/comments
--- PASS: TestLiveViewsMatchTheirTables/projects
--- PASS: TestLiveViewsMatchTheirTables/users
```

Test dibuktikan menggigit, bukan sekadar hijau. FK komposit di `cards` diganti
sementara dengan FK kolom-tunggal di database lokal:

```
$ ALTER TABLE cards DROP CONSTRAINT cards_status_same_project;
$ ALTER TABLE cards ADD CONSTRAINT cards_status_same_project
    FOREIGN KEY (status_id) REFERENCES statuses (id);
$ go test -run TestACardCannotCarryAnotherProjectsStatus
--- FAIL: TestACardCannotCarryAnotherProjectsStatus (0.05s)
    schema_cards_test.go:58: a card took a status belonging to another project
```

Constraint dikembalikan dan diperiksa dengan `pg_get_constraintdef`:

```
FOREIGN KEY (status_id, project_id) REFERENCES statuses(id, project_id)
```

## Belum selesai

Langkah 12–26. Berikutnya Langkah 12: `sqlc` dan view `*_live`.

## Penghalang

Tidak ada.

## Keputusan yang diambil

- **Satu FK komposit untuk `cards.status_id`, bukan dua.** Model data
  menuliskan FK kolom-tunggal *dan* komposit. Yang komposit sudah menolak
  penghapusan status yang dipegang kartu, jadi yang kedua hanya menambah
  pemeriksaan di setiap insert.
- **`NO ACTION`, bukan `RESTRICT`,** untuk FK ke induk yang ikut terhapus dalam
  satu pernyataan cascade. Lihat temuan di bawah.
- **`activity_events` punya partisi `DEFAULT`.** Event ditulis dalam transaksi
  yang sama dengan perubahan yang memicunya, jadi event yang tidak bisa
  dirutekan akan menggagalkan permintaan pengguna. Tabel riwayat tidak boleh
  bisa menjatuhkan aplikasi. Ongkosnya ditulis di migration.
- **Batas partisi dikunci ke UTC,** bukan ke `TimeZone` sesi.
- **Invarian yang tidak bisa jadi constraint ditulis sebagai komentar di dalam
  migration,** di sebelah kolomnya. Lima di `00003`, dua di `00006`.

## Yang gerbangnya tangkap di sesi ini

| Gerbang | Temuan |
|---|---|
| Uji langsung ke PostgreSQL | Alasan awal saya untuk `NO ACTION` **salah**. Saya menulis bahwa `RESTRICT` akan menolak penghapusan project; ternyata lolos, karena `cards` kebetulan dihapus sebelum `statuses`. Komentarnya diperbaiki: `NO ACTION` benar bukan karena `RESTRICT` gagal, tapi karena `RESTRICT` bergantung pada urutan yang tidak dijanjikan |
| `time_logs_period` | Bug di test saya sendiri: di dalam transaksi, `now()` mengembalikan waktu **transaksi dimulai**, yang lebih awal daripada `started_at` yang baru ditulis dari sisi Go |
| PostgreSQL parser | `CHECK (timezone IN (SELECT ...))` ditolak — subquery dilarang di `CHECK`. Dijadikan invarian milik service |
| Hitung ulang | Deskripsi PR #17 menulis "34 tabel"; hitungan sebenarnya 33. Dikoreksi sebelum merge |

## Yang perlu diingat sesi berikutnya

- **`main` tidak punya branch protection.** `gh pr merge --auto` karena itu
  **langsung merge**, bukan menunggu CI — auto-merge hanya menunggu *required
  check*, dan tidak ada satu pun yang wajib. PR #13 ter-merge sebelum CI-nya
  selesai. Sejak #14, urutannya: tunggu `gh pr checks --watch`, baru merge.
- **`board_columns.status_id` di `00002` masih `ON DELETE RESTRICT`.** Diuji di
  sesi ini: ia lolos hari ini karena urutan cascade yang sama — yaitu
  bergantung pada hal yang tidak dijanjikan. Layak diseragamkan jadi
  `NO ACTION`. **Belum diperbaiki**, karena di luar cakupan PR mana pun di sesi
  ini.
- **Partisi bulanan hanya menyediakan landasan empat bulan.** Job bulanan yang
  memperpanjangnya baru ada di Fase 1. Kalau Fase 1 mundur lebih dari empat
  bulan, event mulai jatuh ke `DEFAULT` — tidak ada yang rusak, tapi
  memisahkannya kembali butuh pekerjaan manual.
- `.env` lokal memakai **port 55432 dan 56379**. Service Windows
  `postgresql-x64-18` membayangi container di 5432.
- Test skema butuh `TEST_DATABASE_URL`, bukan `DATABASE_URL`. Tanpa itu semua
  test skema **skip**, dan hasilnya tetap terlihat hijau.
- `cmd/migrate` butuh `APP_ENV`, `APP_BASE_URL`, dan `DATABASE_URL` di
  environment; ia tidak membaca `.env`.
- Komentar kode Go **bahasa Inggris**.
- Pesan commit multi-baris di PowerShell 5.1: `git commit -F berkas`.
