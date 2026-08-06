# ADR-0002: Event lewat tabel outbox PostgreSQL, bukan Kafka

**Status:** Proposed
**Tanggal:** 2026-08-06
**Pengambil keputusan:** pemilik proyek

## Konteks

Enam fitur membutuhkan hal yang sama: mengetahui bahwa sesuatu terjadi, lalu
bereaksi.

| Fitur | Butuh apa dari event |
|---|---|
| B9 activity log per kartu | Riwayat urut, tak bisa diubah |
| I5 audit trail global | Sama, lintas project |
| G4 notifikasi in-app | Reaksi per pengguna |
| E2 aturan otomatis | Pemicu, dengan syarat dan aksi |
| G5 realtime | Fanout ke koneksi WebSocket yang berhak |
| D5/D7 burndown & CFD | Rekonstruksi keadaan historis |

Pemilik menyebut Kafka sebagai kemungkinan. Pertanyaannya: transport seperti
apa yang tepat untuk kebutuhan di atas, pada skala puluhan pengguna dan satu
VPS.

Perkiraan volume: 25 pengguna aktif menghasilkan kira-kira 2.000–5.000 event
per hari. Puncaknya beberapa event per detik.

## Opsi yang dipertimbangkan

### Opsi A — Apache Kafka (atau Redpanda)

Log durabel, banyak consumer group independen, replay dari offset mana pun,
retensi terkonfigurasi.

Kekurangan pada konteks ini:
- Satu broker menambah kira-kira 1 GB RAM pada VPS 4 GB yang sudah menampung
  PostgreSQL, Redis, API, dan worker.
- Menulis ke Kafka **tidak** berada dalam transaksi database yang sama. Kartu
  bisa tersimpan sementara event-nya hilang, atau sebaliknya. Memperbaikinya
  membutuhkan pola outbox — yaitu tabel PostgreSQL, yang berarti Kafka
  ditambahkan **di atas** solusi, bukan menggantikannya.
- Menambah satu sistem terdistribusi yang harus di-monitor, di-backup, dan
  di-debug oleh satu orang.
- Tidak memberi satu pun kemampuan yang belum tersedia pada volume ini.

### Opsi B — Redis Streams

Lebih ringan dari Kafka, sudah ada di stack untuk pub/sub.

Kekurangan: durabilitas Redis lebih lemah dari PostgreSQL dan konfigurasinya
mudah salah. Tetap tidak transaksional dengan penulisan data. Untuk data yang
merangkap audit trail (I5), ini pertukaran yang salah.

### Opsi C — Tabel `activity_events` + tabel `outbox` di PostgreSQL

Event ditulis dalam transaksi yang **sama** dengan perubahan datanya. Kalau
transaksi gagal, tidak ada event palsu. Kalau berhasil, event pasti ada.
Pengiriman ke konsumen dilakukan terpisah oleh worker yang membaca `outbox`,
dengan `LISTEN/NOTIFY` sebagai pemicu supaya tidak polling ketat.

Konsumen: penulis notifikasi (G4), mesin automation (E2), penyiar realtime
(G5), pengirim webhook keluar.

Kekurangan: kapasitas terbatas oleh PostgreSQL. Replay panjang butuh query,
bukan seek offset. Tabel harus dipangkas berkala.

## Keputusan

**Opsi C.** Alasan penolakan yang lain:

- Kafka ditolak karena ia **tidak menghapus** kebutuhan akan tabel outbox —
  penulisan lintas-sistem yang atomik tidak ada. Menambahkannya berarti
  menanggung ongkos operasional Kafka *dan* tetap menulis outbox.
- Redis Streams ditolak karena durabilitasnya tidak pantas untuk data yang
  merangkap audit trail.

Batas ditarik lewat satu interface di sisi konsumen (`rules/20-go.md`:
interface didefinisikan di sisi konsumen, kecil):

```go
// internal/events

type Publisher interface {
    // Publish menulis event ke outbox dalam transaksi yang diberikan.
    // Pemanggil bertanggung jawab atas commit.
    Publish(ctx context.Context, tx pgx.Tx, e Event) error
}

type Subscriber interface {
    Subscribe(ctx context.Context, topics []string, h Handler) error
}
```

Implementasi Fase 1: `events/outbox`. Kalau suatu saat Kafka benar-benar
dibutuhkan, yang berubah adalah `Subscriber` dan sebuah relay
outbox→Kafka — bukan kode pemanggil.

**Kapan keputusan ini ditinjau ulang:**

- Lag pengiriman outbox p95 melewati 5 detik pada beban normal, **atau**
- `activity_events` tumbuh melewati 50 juta baris, **atau**
- Muncul konsumen di luar proses ini yang butuh replay independen.

Kalau pemilik ingin memakai Kafka sebagai bahan belajar, tempat masuknya yang
paling wajar adalah Fase 9 sebagai relay pengiriman webhook keluar — di sana ia
berdiri sendiri, punya alasan (retry dan ordering per repositori), dan
kegagalannya tidak menjatuhkan aplikasi inti. Itu keputusan produk, bukan
teknis, dan perlu ADR sendiri.

## Konsekuensi

### Yang menjadi lebih mudah

- Satu transaksi menjamin data dan event konsisten. Tidak ada activity log yang
  bohong.
- Debugging: seluruh riwayat bisa dibaca dengan `SELECT`.
- Backup mencakup event, otomatis, tanpa mekanisme kedua.
- Burndown dan CFD (D5, D7) dihitung dari sumber yang sama dengan audit trail —
  tidak ada dua versi kebenaran.

### Yang menjadi lebih sulit

- **Pemangkasan wajib dijadwalkan.** `activity_events` tumbuh selamanya. Butuh
  kebijakan retensi (lihat [data-model.md](../data-model.md)) dan partisi per
  bulan sejak awal, karena menambahkan partisi ke tabel besar itu mahal.
- **Beban tulis menumpuk di PostgreSQL yang sama** dengan beban baca aplikasi.
  Pada skala ini tidak masalah; pada skala lain, ini batas pertama yang kena.
- **Urutan hanya dijamin per entitas**, bukan global. Konsumen tidak boleh
  mengandalkan urutan lintas-project.

### Yang perlu diawasi

- Kedalaman antrean `outbox` yang belum terkirim — ini metrik kesehatan nomor
  satu di sistem ini. Alarm kalau > 1.000 baris tertunda lebih dari 1 menit.
- Ukuran `activity_events` per bulan.
- Event yang gagal diproses berulang. Harus masuk dead-letter dan **terlihat di
  antarmuka**, bukan hanya di log — sesuai prinsip "rusak dengan berisik" di
  [product-brief.md](../product-brief.md).
