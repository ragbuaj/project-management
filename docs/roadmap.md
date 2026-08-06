# Roadmap

Prinsip pembagian fase: **setiap fase menghasilkan aplikasi yang bisa dipakai**,
dan tidak ada fase yang memaksa membongkar fase sebelumnya. ID fitur merujuk
[feature-catalog.md](feature-catalog.md).

Perkiraan durasi mengasumsikan satu orang, paruh waktu. Angkanya untuk
perencanaan, bukan janji.

## Ringkasan

| Fase | Nama | Isi | Perkiraan | Milestone |
|---|---|---|---|---|
| 0 | Fondasi | Dokumen 1–7, skema DB inti, auth, CI, pipeline deploy | 1–2 mgg | Aplikasi kosong, bisa login, ter-deploy |
| 1 | Kanban dasar | A1–A5, B1, B2, B3, B5, C1, I1, I2, H2, H6 | 3–4 mgg | **Dipakai untuk kerja nyata** |
| 2 | Kolaborasi | G1, G2, G3, B8, B9, G4, I3, I5 | 3–4 mgg | **Rekan bisa diundang** |
| 3 | Struktur & pencarian | B4, B10, C2, C5, C7, I6, H1, I4 | 3–4 mgg | Board 200+ kartu tetap terkelola |
| 4 | Perencanaan | D1, D2, D3, D4, C6 | 3–4 mgg | Kerja per sprint |
| 5 | Tampilan & laporan | C3, C4, D5, D6, D7, F4 | 4–5 mgg | Beban kerja & progres terlihat |
| 6 | Waktu | F1, F2, F3 | 2 mgg | Waktu tercatat |
| 7 | Otomatisasi | Infra job, E1, E3, E4, E2, E5 | 5–6 mgg | Papan bekerja sendiri |
| 8 | Realtime & Telegram | G5, H3 | 3–4 mgg | Kolaborasi langsung |
| 9 | Integrasi | H8, H4, H4b, H5, H7 | 4–5 mgg | Terhubung ke luar |
| 10 | Offline | H9 | 3–4 mgg | Jalan tanpa koneksi |

---

## Yang harus diputuskan di Fase 0 walau dibangun jauh kemudian

Enam hal ini masuk skema database sejak awal. Semuanya murah sekarang dan mahal
nanti — retrofit berarti migrasi data dan menyisir setiap query.

| # | Keputusan | Kalau ditunda | Dokumen |
|---|---|---|---|
| 1 | `position` sebagai fractional index bertipe `text`, bukan integer | Satu drag mengubah puluhan baris; dua orang menggeser bersamaan saling menimpa | [ADR-0003](adr/0003-urutan-kartu-fractional-index.md) |
| 2 | Status adalah kolom di kartu; Column hanya tampilan | Fase 7 (E5) menjadi migrasi besar | [ADR-0004](adr/0004-status-terpisah-dari-column.md) |
| 3 | `deleted_at` + `archived_at` di semua entitas sejak awal | Menyisir setiap query yang sudah ditulis, satu terlewat = kebocoran data | [data-model.md](data-model.md) |
| 4 | Tabel `activity_events` + `outbox` ditulis sejak Fase 1 | Riwayat yang tidak dicatat tidak bisa dipanggil kembali | [ADR-0002](adr/0002-event-transport-outbox.md) |
| 5 | `project_members` + peran ditegakkan sejak Fase 1 | Menambahkan otorisasi ke sistem yang dibangun single-user berarti menyentuh setiap handler, dan satu pasti terlewat — kelas kerentanan IDOR | [authorization.md](authorization.md) |
| 6 | Kolom nullable `sprint_id`, `epic_id`, `parent_card_id`, `start_date`, `estimate_points` disiapkan di skema | Migrasi tambahan di Fase 4–5 | [data-model.md](data-model.md) |

Ada satu keputusan ketujuh yang bukan skema tapi sama mahalnya: **state server
dan state lokal dipisahkan tegas di frontend sejak Fase 1** (TanStack Query
untuk server, Zustand hanya untuk UI). Fase 8 menambahkan realtime dengan cara
menulis ke cache TanStack Query dari WebSocket. Kalau state server tercampur ke
dalam store lokal, Fase 8 berarti menulis ulang setiap layar.

---

## Fase 0 — Fondasi

**Tujuan:** tidak ada fitur, tapi setiap fitur sesudahnya jadi murah.

- Dokumen 1–7 disetujui
- Monorepo: `backend/` (Go), `frontend/` (React), `deploy/`
- Skema DB inti dijalankan lewat migration (goose) — termasuk tabel yang
  fiturnya baru muncul di Fase 4–7
- Autentikasi: sesi cookie `HttpOnly`, undangan pengguna, ganti password
- Fondasi HTTP: request ID, structured log, recovery, rate limit, bentuk error
  seragam, `http.MaxBytesReader`, timeout server
- Lapisan otorisasi (`internal/authz`) beserta test-nya, walau baru dipakai
  oleh sedikit endpoint
- CI: `go vet`, `golangci-lint`, `go test -race`, `tsc --noEmit`, `eslint`,
  `govulncheck`, `pnpm audit`
- Docker Compose (Postgres, Redis, api, worker, caddy) dan deploy ke VPS

**Selesai kalau:** bisa login di URL produksi, `GET /api/v1/me` mengembalikan
identitas Anda, dan pipeline deploy berjalan dari `git push`.

## Fase 1 — Kanban dasar

**Tujuan:** Anda berhenti memakai alat lama.

A1 project & board · A2 column · A3 card · A4 drag & drop · A5 login ·
B1 deskripsi Markdown · B2 label · B3 due date · B5 prioritas · C1 kanban ·
I1 dark mode · I2 archive & restore · H2 quick capture · H6 ekspor JSON

Teknis: fractional index beserta job rebalance, `activity_events` mulai ditulis
(UI-nya Fase 2), ekspor penuh sebagai jaring pengaman sebelum data jadi berharga.

**Selesai kalau:** Anda memindahkan seluruh pekerjaan nyata ke sini dan tidak
kembali ke alat lama selama seminggu. Kalau gagal di titik ini, berhenti dan
perbaiki — jangan lanjut ke Fase 2.

## Fase 2 — Kolaborasi

**Tujuan:** rekan bisa masuk tanpa Anda khawatir mereka melihat yang bukan haknya.

G1 pengguna & assign · G2 peran per project · G3 mention · B8 komentar ·
B9 activity log per kartu · G4 notifikasi in-app · I3 trash + undo ·
I5 audit trail global

Teknis: seluruh matriks di [authorization.md](authorization.md) ditegakkan dan
punya test. Ini fase dengan risiko keamanan tertinggi di seluruh roadmap —
jalankan skill `security-audit` sebelum merge.

## Fase 3 — Struktur & pencarian

**Tujuan:** board dengan ratusan kartu tetap bisa dikelola.

B4 checklist · B10 relasi antar kartu · C2 tampilan tabel · C5 filter &
pencarian · C7 My Tasks · I6 bulk action · H1 keyboard shortcut ·
I4 command palette

Teknis: full-text search PostgreSQL (`tsvector` + GIN), paginasi keyset, dan
bahasa filter yang dipakai ulang oleh C6 dan H8. Rancang bentuk query filter
dengan benar di sini — Fase 4 dan Fase 9 menumpang di atasnya.

## Fase 4 — Perencanaan

D1 backlog · D2 sprint · D3 story point · D4 epic · C6 saved filter

## Fase 5 — Tampilan & laporan

C3 calendar · C4 Gantt · D5 burndown · D6 velocity · D7 CFD · F4 dashboard

Teknis: burndown dan CFD dihitung dari `activity_events`, bukan dari snapshot
harian. Ini alasan tabel itu ditulis sejak Fase 1 — chart-nya langsung punya
data historis, bukan mulai dari nol.

## Fase 6 — Waktu

F1 timer · F2 log manual · F3 laporan waktu

## Fase 7 — Otomatisasi

Infrastruktur background job (River) · E1 WIP limit · E3 kartu berulang ·
E4 template · E2 aturan otomatis · E5 workflow transisi

Sengaja setelah Fase 4–6: E5 harus dirancang dari status dan sprint yang
benar-benar Anda pakai, bukan yang dibayangkan di bulan pertama. E2 dan E3
adalah konsumen pertama dari `outbox`.

**Risiko terbesar di fase ini:** aturan otomatis yang memicu dirinya sendiri.
Setiap eksekusi rule wajib punya batas kedalaman dan tercatat di
`automation_runs`, termasuk yang gagal.

## Fase 8 — Realtime & Telegram

G5 WebSocket · H3 notifikasi Telegram

Teknis: hub WebSocket di Go, fanout lewat Redis pub/sub, otorisasi per-channel
saat subscribe (bukan hanya saat handshake). Telegram menumpang infrastruktur
job dari Fase 7.

## Fase 9 — Integrasi

H8 API publik + token · H4 GitHub · H4b GitLab · H5 impor · H7 share link

Teknis: GitHub dan GitLab dibangun di atas satu interface `VcsProvider` —
lihat [ADR-0006](adr/0006-abstraksi-vcs-provider.md). Webhook masuk wajib
verifikasi signature dan idempoten. Share link (H7) adalah permukaan serangan
tanpa autentikasi; audit terpisah sebelum merge.

## Fase 10 — Offline

H9 PWA + mode offline

Ditaruh paling akhir dengan sengaja: offline berarti menulis ulang lapisan data
frontend agar tahan konflik, dan itu hanya masuk akal setelah bentuk datanya
berhenti berubah.

---

## Aturan berhenti

Roadmap ini boleh dipotong kapan saja. Yang tidak boleh adalah melanjutkan ke
fase berikutnya sementara fase sekarang belum benar-benar dipakai.

Kalau setelah sebulan sebuah fase tidak mengubah cara Anda bekerja, fase itu
salah pilih — dan sisa fase di bawahnya perlu ditinjau ulang, bukan dikerjakan
karena sudah tertulis.
