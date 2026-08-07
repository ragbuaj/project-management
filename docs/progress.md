# Progres — Project Management Tool

**Terakhir direkonsiliasi:** 2026-08-06 terhadap `main` di commit `2216f84`.

Sumber kebenaran progres adalah keadaan repo, bukan berkas ini. Sebuah item
disebut `selesai` hanya kalau kodenya ada, gerbangnya hijau, **dan PR-nya
sudah ter-merge ke `main`** — bukan kalau seseorang menuliskannya begitu.

Item diturunkan dari [roadmap.md](roadmap.md) dan dari rencana Fase 0 yang
disetujui pada 2026-08-06. Setiap item menaut kembali ke sumbernya.

## Ringkasan

**12 selesai · 4 sedang · 21 belum**

Fase 0 dari 26 langkah: 5 selesai, 4 sedang, 17 belum.

## Gerbang dokumen (sebelum baris kode pertama)

| # | Item | Status | Sumber | Bukti |
|---|---|---|---|---|
| D1 | Product brief dengan non-goals | selesai | `system-design-docs` | PR #2 |
| D2 | Glosarium domain | selesai | `system-design-docs` | PR #2 |
| D3 | Arsitektur C4 level 1–2 | selesai | `system-design-docs` | PR #2, direvisi PR #3 |
| D4 | ADR untuk keputusan sulit dibalik | selesai | `system-design-docs` | PR #1 (0001–0006), PR #3 (0007–0008) |
| D5 | Model data + DDL + indeks + retensi | selesai | `system-design-docs` | PR #2, direvisi PR #3 |
| D6 | Kontrak API OpenAPI (Fase 0–1) | selesai | `system-design-docs` | PR #2 |
| D7 | Matriks otorisasi | selesai | `system-design-docs` | PR #2 |
| D8 | NFR dengan angka dan persentil | selesai | `system-design-docs` | PR #2 |
| D9 | Threat model STRIDE | selesai | `system-design-docs` | PR #2 |
| D10 | Environments & konfigurasi | selesai | `system-design-docs` | PR #2 |

Gerbang dinyatakan lewat pada 2026-08-06 (PR #2).

## Fase 0 — Fondasi

| # | Langkah | Status | Sumber | Bukti |
|---|---|---|---|---|
| 1 | Kerangka monorepo & manifest | selesai | Rencana Fase 0 §1 | PR #4 (`cafcb18`) |
| 2 | Gerbang lokal: lefthook, gitleaks, golangci-lint | selesai | Rencana Fase 0 §2 | PR #4 |
| 3 | CI GitHub Actions | selesai | Rencana Fase 0 §3 | PR #4 |
| 4 | `internal/config` — muat & validasi env, gagal saat start | selesai | Rencana Fase 0 §4 | PR #5 (`2216f84`) |
| 5 | `cmd/api` — timeout, graceful shutdown, `/healthz` | selesai | Rencana Fase 0 §5 | PR #5 |
| 6 | Compose lokal: PostgreSQL, Redis, Mailpit | **sedang** | Rencana Fase 0 §6 | PR #6, belum merge |
| 7 | Pool `pgx` + `/readyz` | **sedang** | Rencana Fase 0 §7 | PR #6, belum merge |
| — | Penyaringan CI per-path + `go-version-file` | **sedang** | Perubahan cakupan, lihat bawah | PR #6, belum merge |
| — | Catatan batas platform GitHub | **sedang** | Perubahan cakupan, lihat bawah | PR #7, belum merge |
| 8 | goose + `cmd/migrate` + migration `0001` identitas | belum | Rencana Fase 0 §8 | — |
| 9 | Migration `0002`–`0003` project, board, status, sprint, cards | belum | Rencana Fase 0 §9 | — |
| 10 | Migration `0004` `activity_events` berpartisi + `outbox` | belum | Rencana Fase 0 §10 | — |
| 11 | Migration `0005`–`0008` sisa 28 tabel | belum | Rencana Fase 0 §11 | — |
| 12 | `sqlc` + view `*_live` untuk soft delete | belum | Rencana Fase 0 §12 | — |
| 13 | `internal/httpx` — request ID, log, recovery, bentuk error, rate limit | belum | Rencana Fase 0 §13 | — |
| 14 | `internal/fracdex` + property test | belum | Rencana Fase 0 §14, ADR-0003 | — |
| 15 | `modules/identity` — Argon2id, sesi, login/logout/me | belum | Rencana Fase 0 §15, ADR-0005 | — |
| 16 | Middleware CSRF double-submit | belum | Rencana Fase 0 §16, ADR-0005 | — |
| 17 | `internal/authz` + table-driven test empat pola | belum | Rencana Fase 0 §17, [authorization.md](authorization.md) | — |
| 18 | Undangan, reset password, daftar & cabut sesi + OpenAPI | belum | Rencana Fase 0 §18 | — |
| 19 | **Gerbang manusia:** arah desain tertulis | belum | Rencana Fase 0 §19, `rules/15-ui-design.md` §2 | — |
| 20 | Scaffold Vite + shadcn + token | belum | Rencana Fase 0 §20 | — |
| 21 | Generate tipe dari OpenAPI + layar login empat state | belum | Rencana Fase 0 §21 | — |
| 22 | Embed SPA ke biner, satu origin | belum | Rencana Fase 0 §22, ADR-0001 | — |
| 23 | `cmd/seed --size=small\|full` | belum | Rencana Fase 0 §23 | — |
| 24 | `cmd/worker` minimal — pangkas sesi kedaluwarsa | belum | Rencana Fase 0 §24 | — |
| 25 | Dockerfile, `compose.prod.yml`, Caddy, workflow deploy | belum | Rencana Fase 0 §25 | — |
| 26 | Backup + uji restore sungguhan | belum | Rencana Fase 0 §26 | — |

## Penghalang

| Item | Penghalang | Menunggu | Sejak |
|---|---|---|---|
| 6, 7, dan dua item perubahan cakupan | CI tidak bisa hijau. GitHub Actions mengalami *degraded availability*; tiga run gagal di `Set up job` sebelum satu pun langkah kita berjalan | Pemulihan GitHub | 2026-08-06 |

**Semua gerbang untuk PR #6 sudah lolos di lokal** — lihat log sesi
2026-08-06. Yang tertahan hanya `go test -race`, yang hanya bisa dijalankan di
CI karena detektor balapan menuntut cgo.

## Keputusan yang menunggu jawaban pemilik

| Pertanyaan | Sejak | Memblokir |
|---|---|---|
| Peran default untuk rekan yang diundang: `admin` (saran saya) atau pindahkan sebagian aksi dari `admin` ke `member`? | 2026-08-06 | Fase 2, bukan Fase 0 |
| PR di atas 400 baris (PR #5 dan #6 masing-masing ~790) — dipecah atau tidak? | 2026-08-06 | Tidak memblokir |
| Ketersediaan branch protection di paket akun ini | 2026-08-06 | Tidak memblokir |

## Di luar cakupan (non-goals)

Ditulis di sini supaya tidak diam-diam dikerjakan. Alasan lengkap ada di
[product-brief.md](product-brief.md#non-goals).

B6 attachment · B7 cover image · B11 custom field · multi-tenancy komersial ·
billing · pendaftaran mandiri · editing kolaboratif CRDT · aplikasi mobile
native · pembuat laporan generik · SSO/SAML/LDAP · i18n

## Perubahan cakupan

| Tanggal | Perubahan | Alasan | Disetujui |
|---|---|---|---|
| 2026-08-06 | PostgreSQL 17 → 18 | Repo masih kosong; satu-satunya saat pindah versi mayor berbiaya nol. Lihat [ADR-0007](adr/0007-postgresql-18.md) | ya |
| 2026-08-06 | Struktur backend jadi per modul | Permintaan pemilik. Batas lapisan ditegakkan kompilator, bukan disiplin. Lihat [ADR-0008](adr/0008-struktur-modular-backend.md) | ya |
| 2026-08-06 | Frontend memakai pnpm dan shadcn/ui | Permintaan pemilik | ya |
| 2026-08-06 | `/readyz` **tidak** memeriksa Redis, hanya PostgreSQL | Rencana menulis "keduanya", tapi [nfr.md](nfr.md) menyatakan aplikasi tetap jalan saat Redis mati. Melaporkannya sebagai *not ready* akan menarik instance yang masih bekerja dari rotasi | belum ditinjau |
| 2026-08-06 | Klien Go untuk Redis ditunda ke Langkah 13 | Konsekuensi baris di atas — tidak ada kode yang membutuhkannya di Langkah 7 | belum ditinjau |
| 2026-08-06 | Penyaringan CI dipindah ke level workflow; `GO_VERSION` diganti `go-version-file` | Job kontrak API menyalakan runner di setiap PR hanya untuk menemukan spec tidak berubah | ya |
| 2026-08-06 | Catatan batas platform GitHub ditambahkan ke `environments.md` | Anjuran sebelumnya keliru: secret scanning tidak gratis untuk repo privat | ya |

## Cara memperbarui berkas ini

Rekonsiliasi dilakukan di akhir setiap fase, atau minimal mingguan:

1. Daftar PR yang ter-merge sejak `Terakhir direkonsiliasi`
2. Untuk setiap `selesai`: benarkah kodenya ada dan gerbangnya hijau?
3. Untuk setiap `sedang` yang tidak bergerak: tulis penghalangnya
4. Cari pekerjaan di repo yang tidak ada di daftar — itu pelebaran cakupan
5. Perbarui tanggal dan commit rekonsiliasi

Item yang ternyata belum benar-benar selesai **dikembalikan statusnya**.
