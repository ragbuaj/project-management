---
type: session
project: project-management
phase: build
date: 2026-08-06
---

# Sesi: Perencanaan penuh, gerbang dokumen, dan Fase 0 langkah 1–7

Sesi pertama. Dari repo kosong sampai server HTTP yang terhubung ke PostgreSQL.

## Dikerjakan

**Perencanaan** — katalog fitur, roadmap 11 fase, seluruh dokumen 1–10, dan
rencana Fase 0 berisi 26 langkah. Delapan ADR ditulis dan disetujui.

**Fase 0 langkah 1–5, ter-merge:**

- Monorepo, `lefthook.yml`, `.golangci.yml`, CI GitHub Actions — PR #4
- `internal/config` dan `cmd/api` dengan `/healthz` — PR #5

**Fase 0 langkah 6–7, belum merge:**

- `compose.yml` (PostgreSQL 18, Redis, Mailpit), pool `pgx`, `/readyz` — PR #6
- Catatan batas platform GitHub — PR #7

## Diverifikasi

Gerbang lokal untuk PR #6, semuanya pada commit `e020ce4`:

```
$ golangci-lint run
0 issues.

$ go test ./...
?   	.../backend/cmd/api	[no test files]
ok  	.../backend/internal/config	1.177s
ok  	.../backend/internal/httpx	3.628s
ok  	.../backend/internal/postgres	3.000s

$ go run golang.org/x/vuln/cmd/govulncheck@latest ./...
No vulnerabilities found.

$ gitleaks git --staged --no-banner
no leaks found
```

Test integrasi berjalan sungguhan, bukan skip — terhadap PostgreSQL 18.4 di
container:

```
$ go test -v -run TestNew ./internal/postgres/...
--- PASS: TestNewRejectsAMalformedURL (0.01s)
--- PASS: TestNewNeverEchoesTheConnectionString (0.01s)
--- PASS: TestNewFailsFastWhenTheDatabaseIsUnreachable (0.02s)
--- PASS: TestNewAppliesServerSideTimeouts (0.07s)
--- PASS: TestNewConnects (0.07s)
ok  	.../backend/internal/postgres	3.478s
```

Degradasi `/readyz` dibuktikan dengan mematikan PostgreSQL sungguhan:

```
--- PostgreSQL hidup ---
healthz : 200 {"status":"ok"}
readyz  : 200 {"status":"ok","checks":{"postgres":"ok"}}

--- PostgreSQL dimatikan ---
healthz : 200 {"status":"ok"}
readyz  : 503
   log server: readiness check failed check=postgres error="failed to connect
   to `user=pm database=pm` ... connection refused"

--- PostgreSQL dihidupkan lagi ---
readyz  : 200 {"status":"ok","checks":{"postgres":"ok"}}
```

Gagal cepat saat konfigurasi tidak valid, dengan seluruh masalah sekaligus:

```
$ ./api          # tanpa env
fatal: invalid configuration: APP_ENV is required
APP_BASE_URL is required
exit code: 1
```

## Belum selesai

- **PR #6 dan #7 belum ter-merge.** CI tidak bisa hijau.
- `go test -race` belum pernah berjalan untuk kode Langkah 6–7. Detektor
  balapan menuntut cgo, yang tidak ada di mesin Windows ini — hanya CI yang
  bisa membuktikannya.
- Langkah 8–26 belum disentuh.

## Penghalang

**GitHub Actions mengalami *degraded availability*** (dikonfirmasi lewat status
GitHub, 2026-08-06 16:33 UTC). Tiga run gagal di langkah `Set up job` dengan
`Failed to resolve action download info: Service Unavailable` — sebelum satu
pun langkah kita berjalan. Tidak ada yang bisa diperbaiki dari sisi repo.

## Berikutnya

1. Setelah GitHub pulih: jalankan ulang CI untuk PR #6 dan #7, lalu merge.
2. Lanjut ke Langkah 8 — goose, `cmd/migrate`, migration `0001` tabel
   identitas. Di sana gerbang `squawk` pertama kali menganalisis sesuatu.
3. Jawab tiga keputusan yang menggantung di
   [progress.md](../progress.md#keputusan-yang-menunggu-jawaban-pemilik).

## Keputusan yang diambil

- **PostgreSQL 18, bukan 17** — ADR-0007. Diambil selagi repo masih kosong.
- **Struktur backend per modul** — ADR-0008, atas permintaan pemilik. Biayanya
  dicatat apa adanya: alias impor wajib (ditegakkan linter `importas`) dan
  query lintas modul harus lewat service pemiliknya.
- **`/readyz` hanya memeriksa PostgreSQL.** Menyimpang dari rencana karena
  `nfr.md` menyatakan aplikasi tetap jalan saat Redis mati.
- **Pemilik dan rekan adalah satu golongan pengguna.** Tidak ada fitur yang
  boleh dirancang sebagai "mode ringan untuk rekan".
- **Toolchain Go naik dua kali karena `govulncheck` menolak push** — ke
  1.25.12 (9 kerentanan pustaka standar), lalu `golang.org/x/text` ke v0.39.0
  (GO-2026-5970, terjangkau lewat `pgxpool`).
- **Penyaringan CI dipindah ke level workflow.** Job kontrak API menyalakan
  runner di setiap PR hanya untuk menemukan spec tidak berubah.

## Yang perlu diingat sesi berikutnya

- `.env` lokal memakai **port 55432 dan 56379**, bukan bawaan. Mesin ini punya
  service Windows `postgresql-x64-18` yang membayangi container di 5432, dan
  gejalanya menyesatkan: `password authentication failed`.
- Container compose ditinggalkan dalam keadaan hidup.
- Komentar kode **bahasa Inggris** (`rules/00-core.md` §7) — `misspell` akan
  menolak bahasa Indonesia di komentar Go. Diskusi dan `docs/` tetap Indonesia.
- Pesan commit multi-baris di PowerShell 5.1: pakai `git commit -F berkas`,
  bukan here-string, kalau pesannya memuat tanda kutip ganda.
