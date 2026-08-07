# Environments & Konfigurasi

Dokumen yang paling sering dibutuhkan orang baru di hari pertama — termasuk
Anda sendiri, enam bulan lagi, di mesin yang berbeda.

## Toolchain

| Alat | Versi | Catatan |
|---|---|---|
| Go | 1.25.12 | [ADR-0001](adr/0001-stack-react-go-postgresql.md) menyebut 1.24; versi minor Go bukan keputusan yang butuh ADR. Patch ini menutup 9 kerentanan pustaka standar yang ditemukan `govulncheck`. **`backend/go.mod` adalah satu-satunya sumbernya** — CI membacanya lewat `go-version-file`, jadi cukup naikkan di satu tempat |
| Node | 24.x | |
| pnpm | 10.x | Package manager frontend. Diaktifkan lewat `corepack enable` |
| PostgreSQL | 18 | [ADR-0007](adr/0007-postgresql-18.md) |
| Redis | 7 | |
| goose | 3.27+ | Migration, maju saja |
| sqlc | 1.31.1 | Satu entri per modul ([ADR-0008](adr/0008-struktur-modular-backend.md)). Versinya dipatok, bukan `1.x`: sqlc versi berbeda menghasilkan berkas berbeda, dan `sqlc diff` di CI membandingkan berkas. Angkanya ada di `.github/workflows/sqlc.yml` |
| golangci-lint · gitleaks · squawk · lefthook | terbaru | Gerbang otomatis, lihat `references/quality-gates.md` |

## Daftar lingkungan

| Lingkungan | Tujuan | Data | Di mana |
|---|---|---|---|
| `local` | Pengembangan | Seed, termasuk seed berukuran penuh 50.000 kartu untuk uji performa | Docker Compose di mesin Anda |
| `production` | Pemakaian nyata | Nyata | VPS |

Target deploy **VPS + Docker Compose disetujui pemilik, 2026-08-06.** Pilihan
ini yang membuat WebSocket persisten (G5), worker background (E2, E3, H3), dan
Redis bisa hidup di satu tempat tanpa layanan tambahan.

**Tidak ada staging.** Untuk satu pengembang, staging adalah lingkungan ketiga
yang harus dirawat dan yang akan menyimpang dari produksi tanpa ada yang tahu.
Penggantinya: feature flag dan kemampuan rollback yang benar-benar diuji.
Kalau suatu saat ada perubahan yang tidak bisa diuji secara aman lewat kedua
mekanisme itu, staging sementara dibuat untuk perubahan itu saja lalu dibuang.

## Variabel environment

Aplikasi **gagal saat start** kalau ada variabel wajib yang kosong atau tidak
valid, bukan gagal nanti saat variabel itu dipakai pertama kali.

Tabel di bawah adalah daftar lengkap untuk sistem yang sudah jadi. Sebuah
variabel baru benar-benar **ditegakkan** saat kode yang membacanya sudah ada —
lihat `.env.example`, yang isinya selalu sama dengan yang berlaku hari ini.
Memaksa orang mengisi nilai untuk sesuatu yang belum dibaca hanya melahirkan
isian asal-asalan, dan isian asal-asalan sampai ke produksi.

### Inti

| Nama | Wajib | Rahasia | Keterangan |
|---|:--:|:--:|---|
| `APP_ENV` | ya | — | `local` \| `production` |
| `APP_BASE_URL` | ya | — | Origin publik, contoh `https://pm.example.com`. Dipakai untuk tautan di email dan Telegram |
| `HTTP_ADDR` | — | — | Default `:8080` |
| `LOG_LEVEL` | — | — | `debug` \| `info` \| `warn` \| `error`. Default `info` |
| `DATABASE_URL` | ya | ✅ | Koneksi PostgreSQL |
| `DATABASE_MAX_CONNS` | — | — | Default 20. Dibatasi sadar — sepuluh instance dengan pool 50 berarti 500 koneksi |
| `REDIS_URL` | ya | ✅ | Koneksi Redis |
| `TRUSTED_PROXIES` | — | — | Daftar CIDR yang dipisah koma, contoh `10.0.0.5/32`. **Kosong berarti tidak ada header `X-Forwarded-For` yang pernah dipercaya**, dan itu nilai yang benar untuk server yang dihubungi langsung. Ditulis eksplisit sebagai alamat Caddy saja, bukan "semua alamat privat" ([ADR-0010](adr/0010-ip-klien-dan-rate-limit.md)) |
| `SESSION_HASH_KEY` | ya | ✅ | 32 byte base64. Kunci HMAC untuk hash token sesi |
| `ENCRYPTION_KEY` | ya | ✅ | 32 byte base64. AES-GCM untuk `vcs_connections.credential_enc` |

### Email (undangan & reset password)

| Nama | Wajib | Rahasia | Keterangan |
|---|:--:|:--:|---|
| `SMTP_HOST` | ya | — | |
| `SMTP_PORT` | ya | — | |
| `SMTP_USERNAME` | ya | ✅ | |
| `SMTP_PASSWORD` | ya | ✅ | |
| `SMTP_FROM` | ya | — | Alamat pengirim |

### Telegram (Fase 8)

| Nama | Wajib | Rahasia | Keterangan |
|---|:--:|:--:|---|
| `TELEGRAM_BOT_TOKEN` | Fase 8 | ✅ | Dari BotFather |
| `TELEGRAM_WEBHOOK_SECRET` | Fase 8 | ✅ | Diverifikasi lewat header `X-Telegram-Bot-Api-Secret-Token` |

### Batas & pengaman

| Nama | Wajib | Rahasia | Keterangan |
|---|:--:|:--:|---|
| `RATE_LIMIT_LOGIN_PER_MIN` | — | — | Default 5 per akun, 20 per IP |
| `MAX_REQUEST_BYTES` | — | — | Default 1 MB |
| `SESSION_IDLE_DAYS` | — | — | Default 14 |
| `SESSION_ABSOLUTE_DAYS` | — | — | Default 90 |

Kredensial GitHub dan GitLab **tidak** ada di daftar ini — keduanya per project,
disimpan terenkripsi di `vcs_connections`, bukan di environment.

## Rahasia

| Aspek | Keputusan |
|---|---|
| Penyimpanan | Berkas `.env` di VPS, mode `0600`, dimuat Docker Compose |
| Kenapa bukan secret manager | Satu mesin, satu operator. Secret manager menambah dependensi yang harus tersedia saat boot, tanpa mengurangi permukaan pada topologi ini |
| Di repo | `.env` masuk `.gitignore`. `.env.example` memuat **nama variabel dengan nilai kosong**, tidak pernah nilai contoh yang terlihat sah |
| Rotasi | `SESSION_HASH_KEY` — rotasi memaksa semua orang login ulang, boleh kapan saja. `ENCRYPTION_KEY` — butuh enkripsi ulang `vcs_connections`; sediakan perintah `cmd/rotate-key` sebelum Fase 9 |
| Kalau pernah ter-commit | Rahasia itu **sudah bocor**. Rotasi. Menghapusnya dari riwayat git tidak cukup |
| Di log | Tidak pernah. Termasuk saat panik dan saat mencetak konfigurasi ketika start |

## Batas platform GitHub

Repositori ini pribadi dan privat — bukan milik organisasi maupun enterprise.
Sejumlah gerbang di sisi GitHub karena itu tidak tersedia, dan catatan ini ada
supaya tidak ada yang menganjurkannya lagi sebagai "tinggal dinyalakan".

| Fitur GitHub | Ketersediaan | Pengganti di repo ini |
|---|---|---|
| **Secret scanning + push protection** | Gratis hanya untuk repo **publik**. Di repo privat ia bagian dari GitHub Advanced Security / Secret Protection — produk berbayar. **Tidak tersedia di sini** | `gitleaks` di `pre-commit` **dan** di CI atas seluruh riwayat (`fetch-depth: 0`) |
| **Branch protection / rulesets** | Belum diverifikasi. Buka `Settings → Branches` atau `Settings → Rules`; kalau opsinya tidak ada, itu batas paket | Setiap perubahan lewat PR, CI hijau sebelum merge, `main` tidak pernah disentuh langsung |
| **Code scanning (CodeQL)** | Bagian dari GHAS pada repo privat | `golangci-lint` dengan `gosec`, dan `govulncheck` di `pre-push` dan CI |
| **Dependabot alerts** | Belum diverifikasi untuk paket ini. Periksa `Settings → Code security` | `govulncheck` + `pnpm audit` di CI, lockfile di-commit |

### Konsekuensi yang harus disadari

Tanpa push protection, tidak ada yang menolak rahasia **di sisi server** sebelum
ia menyentuh mesin GitHub. Pertahanan terakhir kita berjalan di mesin
pengembang, dan pertahanan di mesin pengembang bisa dilewati dengan
`--no-verify`.

Ini satu-satunya tempat di seluruh proyek yang bergantung pada disiplin
manusia, dan itu bukan mekanisme yang bisa diandalkan. Karena itu:

- **`--no-verify` hanya untuk keadaan darurat**, dan setiap pemakaiannya
  dinyatakan di PR — bukan didiamkan.
- Kalau sebuah rahasia tetap lolos, yang berlaku adalah aturan di
  [threat-model.md](threat-model.md): rahasia itu **sudah bocor**. Rotasi.
  Menghapusnya dari riwayat git tidak cukup.

Kalau suatu hari repositori ini pindah ke organisasi berbayar, tiga baris
pertama di tabel atas layak dinyalakan dan catatan ini diperbarui.

## Layanan eksternal

| Layanan | Dipakai untuk | Sandbox | Fase | Kalau mati |
|---|---|---|---|---|
| SMTP | Undangan, reset password | Mailpit di lokal | 0 | Tertunda, terlihat sebagai kegagalan |
| Telegram Bot API | Notifikasi | Bot terpisah untuk lokal | 8 | Retry backoff sampai 24 jam |
| GitHub API | Baca issue/PR | Repo pribadi untuk uji | 9 | Panel VCS menampilkan data terakhir |
| GitLab API | Baca issue/MR | Repo pribadi untuk uji | 9 | Sama |
| Let's Encrypt | TLS | — | 0 | Caddy memakai sertifikat yang masih berlaku |
| Object storage | Backup off-site | — | 0 | Backup lokal tetap jalan, alarm menyala |

## Menjalankan lokal

```bash
cp .env.example .env
docker compose up -d
```

`compose.yml` ada di akar repo, bukan di `deploy/`, supaya `docker compose`
menemukannya bersama `.env` tanpa flag tambahan. `deploy/` menyimpan artefak
produksi — Dockerfile, `compose.prod.yml`, Caddyfile — dan lahir di PR 9.

> **Kalau PostgreSQL sudah terpasang di mesin Anda**, port 5432 akan bentrok —
> dan bentroknya senyap. Windows mengizinkan satu proses mengikat `::` dan
> proses lain `0.0.0.0` di port yang sama, jadi container ikut menyala tapi
> yang menjawab koneksi Anda adalah PostgreSQL lokal, dengan galat "password
> authentication failed" yang menyesatkan. Pindahkan port container lewat
> `.env`:
>
> ```bash
> POSTGRES_PORT=55432
> REDIS_PORT=56379
> ```
>
> lalu sesuaikan `DATABASE_URL`. Jangan matikan PostgreSQL lokal Anda untuk
> ini — memindahkan port container jauh lebih murah dibatalkan.

```bash
cd backend && go run ./cmd/migrate up && go run ./cmd/seed --size=small
```

```bash
cd backend && go run ./cmd/api
```

```bash
cd frontend && pnpm install && pnpm dev
```

Seed berukuran penuh untuk uji performa — dipakai membuktikan angka di
[nfr.md](nfr.md), bukan untuk pemakaian sehari-hari:

```bash
cd backend && go run ./cmd/seed --size=full
```

## Perbedaan antar lingkungan

| Aspek | `local` | `production` |
|---|---|---|
| TLS | Tidak (`http://localhost:5173`) | Wajib, Let's Encrypt lewat Caddy |
| Cookie `Secure` | Dimatikan | Wajib |
| SPA | Vite dev server terpisah, proxy ke API | Di-embed ke binary, satu origin |
| Email | Mailpit, tidak pernah keluar | SMTP nyata |
| Log | Teks berwarna | JSON terstruktur |
| Migration | Manual | Otomatis saat start, sebelum menerima trafik |
| Backup | Tidak ada | Basebackup harian + WAL archiving |

Perbedaan cookie `Secure` adalah satu-satunya tempat konfigurasi keamanan
melemah di lokal, dan itu dikendalikan langsung oleh `APP_ENV` — bukan oleh
variabel terpisah yang bisa salah di-set di produksi.
