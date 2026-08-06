# ADR-0001: React (Vite SPA) + Go + PostgreSQL + Redis, disajikan dari satu origin

**Status:** Accepted
**Tanggal:** 2026-08-06
**Pengambil keputusan:** pemilik proyek

## Konteks

Cakupan yang dipilih menuntut: sinkronisasi realtime (G5), background job dan
scheduler (E2, E3, H3), mesin aturan otomatis (E2), workflow state machine
(E5), RBAC per project (G2), webhook masuk dari GitHub/GitLab (H4, H4b), dan
API publik bertoken (H8).

Batasan yang berlaku: satu pengembang, paruh waktu, tanpa tenggat eksternal,
target infrastruktur satu VPS di bawah $15/bulan, skala puluhan pengguna.

Pemilik menetapkan React di frontend dan Go di backend. ADR ini mencatat
konsekuensinya dan memutuskan turunan-turunan yang belum ditentukan: meta-
framework frontend, cara akses database, dan topologi penyajian.

## Opsi yang dipertimbangkan

### Opsi A — Laravel + Inertia + React (monolit)

Queue, scheduler, broadcasting, Policy, Eloquent, dan migration tersedia bawaan.
Diperkirakan sekitar 40% infrastruktur di daftar kebutuhan tidak perlu ditulis.

Kekurangan: WebSocket jangka panjang dan pemrosesan webhook konkuren bukan
kekuatan PHP; keduanya ada di daftar. Jejak memori lebih besar untuk VPS kecil.

**Ditolak** — pemilik menetapkan Go setelah kekurangan ini dijelaskan.

### Opsi B — Go + React SPA (Vite), satu origin

Go menulis lebih banyak kode infrastruktur sendiri, tapi WebSocket, worker
pool, dan penanganan webhook konkuren adalah wilayah paling nyamannya. Satu
binary statis, satu container, jejak memori kecil.

Kekurangan: migration, akses DB, queue, scheduler, dan otorisasi ditulis
manual. Beban terbesar ada di Fase 0.

### Opsi C — Go + Next.js

Menambah runtime Node di produksi hanya untuk SSR yang tidak dibutuhkan:
seluruh aplikasi berada di balik login, tidak ada halaman publik yang perlu
diindeks kecuali share link read-only (H7, satu halaman). Menambah container,
memori, dan permukaan pembaruan tanpa manfaat yang sepadan.

**Ditolak** — biaya operasional tanpa manfaat pada konteks ini.

### Opsi D — Go + React, di-deploy terpisah (frontend di CDN, API di domain lain)

Memaksa CORS, memaksa token di penyimpanan yang bisa dibaca XSS atau
konfigurasi cookie lintas-domain yang rapuh, dan menambah satu pipeline deploy.

**Ditolak** — menambah kerumitan keamanan tanpa kebutuhan yang mendasarinya.

## Keputusan

**Opsi B.** Rinciannya:

| Lapisan | Pilihan | Alasan |
|---|---|---|
| Frontend | React 19 + TypeScript, Vite, SPA | Tidak ada kebutuhan SSR. Build menghasilkan berkas statis |
| Routing | TanStack Router | Type-safe, search-param sebagai state — cocok untuk filter (C5) dan saved filter (C6) yang harus bisa di-bookmark |
| State server | TanStack Query | Satu-satunya pemilik data server. Prasyarat agar Fase 8 (realtime) tidak berarti menulis ulang layar |
| State lokal | Zustand | Hanya untuk state UI: panel terbuka, mode tampilan, seleksi bulk |
| Backend | Go 1.24, `net/http` + `chi` | Pustaka standar sudah jauh. `chi` hanya untuk grup route dan middleware |
| Akses DB | `sqlc` + `pgx/v5` | SQL eksplisit, tipe digenerate. Tanpa ORM yang menyembunyikan query (`rules/20-go.md`) |
| Migration | `goose` | Maju saja, satu perubahan per berkas, tidak pernah diedit setelah merge |
| Database | PostgreSQL 17 | `jsonb`, full-text search, dan `LISTEN/NOTIFY` — tiga-tiganya dipakai |
| Cache & pub/sub | Redis 7 | Fanout WebSocket antar-instance, rate limit, cache hasil chart |
| Background job | `river` (antrean di PostgreSQL) | Enqueue job dalam transaksi yang sama dengan perubahan data. Lihat [ADR-0002](0002-event-transport-outbox.md) |
| WebSocket | `coder/websocket` | Kecil, tanpa dependensi, API sesuai `context` |
| Kontrak API | OpenAPI 3.1 ditulis tangan → generate tipe TS | Sumber kebenaran tunggal (`rules/30-api-contract.md`) |
| Penyajian | Binary Go menyajikan hasil build SPA dari `embed.FS`, di belakang Caddy | Satu origin, satu container, satu deploy |

**Satu origin** adalah bagian penting dari keputusan ini, bukan detail. Karena
SPA dan API berbagi origin: tidak ada CORS, cookie sesi `HttpOnly` +
`SameSite=Lax` cukup tanpa konfigurasi lintas-domain, dan tidak ada token yang
perlu disimpan di tempat yang bisa dibaca XSS. Lihat
[ADR-0005](0005-autentikasi-sesi-cookie.md).

## Konsekuensi

### Yang menjadi lebih mudah

- Fase 8 (realtime): hub WebSocket di Go dengan goroutine per koneksi adalah
  jalur yang sudah rata. Ini fitur termahal di roadmap dan stack ini
  memurahkannya.
- Fase 9 (webhook GitHub/GitLab): verifikasi signature dan pemrosesan konkuren
  tanpa proses tambahan.
- Deploy: satu binary statis. Rollback berarti menjalankan tag image sebelumnya.
- Biaya: seluruh sistem muat di VPS 2 vCPU / 4 GB.

### Yang menjadi lebih sulit

- **Fase 0 lebih berat.** Otorisasi, paginasi keyset, bentuk error seragam,
  rate limit, dan lapisan sesi ditulis sendiri. Ini pajak di depan; ADR ini
  menerimanya secara sadar.
- **Tidak ada admin panel gratis.** Setiap layar CRUD internal ditulis manual.
- **Duplikasi tipe.** Struct Go dan tipe TypeScript sama-sama diturunkan dari
  OpenAPI, tapi generatornya berbeda. Ketidaksesuaian mungkin terjadi dan harus
  ditangkap oleh test kontrak di CI, bukan oleh mata.
- **`sqlc` tidak bisa membangun query dinamis.** Filter (C5) dan saved filter
  (C6) butuh WHERE yang bervariasi. Ini akan ditulis dengan builder manual
  berparameter di satu paket khusus, bukan disebar. Batasnya perlu ditetapkan
  di Fase 3 sebelum kodenya menyebar.

### Yang perlu diawasi

- Jumlah baris kode infrastruktur di `internal/`. Kalau melewati kira-kira
  sepertiga total backend, keputusan ini perlu ditinjau ulang.
- Waktu yang dihabiskan Fase 0. Kalau lewat 3 minggu, itu sinyal pajak Go lebih
  mahal dari yang diperkirakan di sini.
- Ukuran bundel SPA. Gantt (C4) dan chart (D5–D7) adalah dependensi berat;
  keduanya harus di-*lazy load*, tidak masuk bundel awal.
