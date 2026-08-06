# Kebutuhan Non-Fungsional

Tanpa angka, "harus cepat" akan ditafsirkan berbeda setiap kali. Angka di sini
adalah ambang yang membuat sebuah perubahan boleh atau tidak boleh di-merge.

## Volume 12 bulan ke depan

Dasar seluruh target di bawah. Kalau salah satu terlampaui dua kali lipat,
dokumen ini perlu ditinjau ulang.

| Data | Perkiraan | Catatan |
|---|---|---|
| Pengguna aktif | 25 | Pemilik + rekan yang diundang |
| Project | 20 | |
| Kartu | 50.000 | Termasuk yang diarsipkan |
| Kartu per status pada board tersibuk | 500 | Ambang uji performa board |
| Komentar | 200.000 | |
| `activity_events` | 2.000.000 | ~5.000/hari. Dipartisi bulanan |
| `time_logs` | 500.000 | |
| Permintaan HTTP puncak | 20 req/detik | |
| Koneksi WebSocket bersamaan | 25 | Satu per tab aktif |

## Performa

Diukur dari sisi server, tidak termasuk latensi jaringan, kecuali baris yang
menyebut "di browser".

| Operasi | Target | Persentil | Kondisi |
|---|---|---|---|
| `GET /boards/{id}` | < 400 ms | p95 | Board 6 kolom, 500 kartu di kolom terbesar |
| `POST /cards/{id}/move` | < 150 ms | p95 | Kolom berisi 500 kartu |
| `PATCH /cards/{id}` | < 150 ms | p95 | |
| `GET /projects/{id}/cards` dengan filter | < 300 ms | p95 | 50.000 kartu di project |
| Pencarian teks penuh | < 500 ms | p95 | 50.000 kartu |
| Burndown / CFD | < 800 ms | p95 | Sprint 2 minggu, dari `activity_events`. Boleh di-cache Redis 60 detik |
| Perambatan realtime (commit → tiba di klien lain) | < 1 detik | p95 | Fase 8 |
| Lag pengiriman `outbox` | < 2 detik | p95 | **Metrik kesehatan nomor satu di sistem ini** |
| Ekspor JSON penuh satu project | < 30 detik | p95 | Berjalan sebagai job, bukan di jalur permintaan |

### Di browser

| Ukuran | Target | Kondisi |
|---|---|---|
| Board tampil dan bisa diinteraksi | < 2 detik | Muat dingin, koneksi 4G, board 500 kartu |
| Umpan balik drag & drop | < 50 ms | Optimistic update, tidak menunggu server |
| Bundel JS awal | < 250 KB gzip | Gantt, chart, dan editor Markdown **tidak** termasuk — ketiganya di-lazy load |
| Bundel per rute lazy | < 200 KB gzip | Per rute |

Ambang bundel ditegakkan di CI. Melewatinya adalah kegagalan build, bukan
peringatan — kalau hanya peringatan, ia akan terlampaui pelan-pelan dan tidak
ada yang tahu kapan.

### Aturan pengukuran

`EXPLAIN (ANALYZE, BUFFERS)` sebelum menyatakan sebuah query cepat atau lambat.
Angka di atas dibuktikan dengan data seed berukuran penuh (50.000 kartu), bukan
dengan database kosong. Seed itu bagian dari Fase 0.

## Ketersediaan

| Aspek | Nilai |
|---|---|
| Target | 99% per bulan (kira-kira 7 jam boleh mati) |
| Redundansi | **Tidak ada.** Satu VPS, satu instance PostgreSQL |
| Jendela pemeliharaan | Bebas, tanpa pengumuman |
| RPO (data yang boleh hilang) | 5 menit — dibatasi jeda WAL archiving |
| RTO (waktu pulih) | 4 jam dari backup |

Ini keputusan sadar untuk tool pribadi. High availability berarti minimal dua
mesin, replikasi, dan failover yang harus diuji — biaya yang tidak sebanding
dengan 7 jam downtime per bulan yang tidak merugikan siapa pun.

## Perilaku saat dependensi mati

Ditulis di depan supaya tidak diputuskan tergesa-gesa saat kejadian.

| Yang mati | Perilaku yang diharapkan |
|---|---|
| Redis | Aplikasi **tetap jalan**. Realtime berhenti (klien jatuh ke polling 30 detik), cache chart dihitung ulang setiap kali. **Rate limit endpoint autentikasi gagal-tertutup** — login ditolak sementara, karena menonaktifkan rate limit di jalur login lebih berbahaya daripada tidak bisa login |
| Worker | Aplikasi tetap jalan. Notifikasi, automation, dan realtime **tertunda, bukan hilang** — antreannya durabel di PostgreSQL |
| PostgreSQL | Aplikasi mati. Halaman status statis, bukan stack trace |
| Telegram API | Job retry dengan backoff eksponensial, maksimum 24 jam. Kegagalan permanen terlihat di antarmuka |
| GitHub / GitLab | Panel VCS di kartu menampilkan data terakhir beserta waktunya. Webhook yang gagal tetap tersimpan mentah dan bisa diproses ulang |
| SMTP | Undangan dan reset password tertunda. Ditampilkan ke admin sebagai kegagalan, bukan didiamkan |

Prinsip yang mengikat semuanya: **rusak dengan berisik, bukan diam-diam**.
Kegagalan job, webhook yang ditolak, dan aturan otomatis yang error harus
terlihat di antarmuka, bukan hanya di log.

## Retensi & privasi

Kebijakan lengkap dan daftar kolom data pribadi ada di
[data-model.md](data-model.md#kolom-yang-memuat-data-pribadi).

Ringkasnya: `activity_events` 24 bulan, sampah 30 hari, arsip selamanya,
log aplikasi 14 hari. Tidak satu pun kolom bertanda 🔒 boleh masuk log.

## Dukungan klien

| Klien | Dukungan |
|---|---|
| Peramban | Dua versi terakhir Chrome, Firefox, Safari, Edge |
| Layar | Desktop ≥ 1280px sebagai target utama; tablet dan ponsel didukung untuk membaca dan mengubah kartu, **bukan** untuk drag & drop board |
| Aplikasi mobile native | Non-goal |
| Versi API lama | Hanya `v1`. Karena klien satu-satunya di-deploy bersama server ([ADR-0001](adr/0001-stack-react-go-postgresql.md)), tidak ada klien lama yang perlu dilayani — **kecuali** setelah Fase 9, saat API token dipakai skrip di luar. Sejak saat itu aturan kompatibilitas `rules/30-api-contract.md` berlaku penuh |

Baris terakhir adalah tanggal berlakunya disiplin kontrak API. Sebelum Fase 9,
mengubah bentuk response adalah perubahan biasa. Sesudahnya, itu perubahan
yang merusak.

## Anggaran biaya

| Komponen | Perkiraan |
|---|---|
| VPS 2 vCPU / 4 GB / 80 GB SSD | $6–12 / bulan |
| Backup off-site (object storage) | $1–2 / bulan |
| Domain | ~$12 / tahun |
| TLS | $0 — Let's Encrypt lewat Caddy |
| **Total** | **di bawah $15 / bulan** |

Kalau sebuah keputusan teknis mendorong angka ini melewati $25/bulan, ia perlu
ADR sendiri.
