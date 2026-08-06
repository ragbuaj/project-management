# ADR-0008: Struktur backend per modul, menggantikan tata letak paket di architecture.md

**Status:** Proposed
**Tanggal:** 2026-08-06
**Pengambil keputusan:** pemilik proyek
**Menggantikan:** bagian "Struktur backend" pada
[architecture.md](../architecture.md). Seluruh keputusan lain di ADR-0001 dan
`architecture.md` tetap berlaku.

## Konteks

`architecture.md` versi pertama menaruh setiap area bisnis sebagai satu paket
datar di bawah `internal/` — `internal/card`, `internal/project`,
`internal/sprint` — dengan lapisan (service, repository, handler) sebagai
berkas di dalam paket yang sama.

Pemilik meminta setiap modul punya foldernya sendiri, berisi folder `domain`,
`repository`, `service`, `handler`, dan `route`. Alasannya: batas antar-lapisan
terlihat dari struktur direktori, bukan dari kebiasaan penamaan berkas.

Keputusan ini diambil sekarang karena sulit dibalik: setelah dua puluh modul
lahir di Fase 1–10, memindahkannya berarti menyentuh setiap berkas impor di
seluruh backend.

## Opsi yang dipertimbangkan

### Opsi A — Paket datar, lapisan sebagai berkas

`internal/card/{service.go, repository.go, handler.go, domain.go}`, semuanya
`package card`.

Kelebihan: paling dekat dengan kebiasaan Go umum. Tidak ada tabrakan nama
paket, tidak ada alias impor. Pemanggilan terbaca wajar: `card.NewService(...)`.

Kekurangan: batas antar-lapisan hanya ditegakkan disiplin. Tidak ada yang
mencegah `handler.go` memanggil query database langsung — kompilator diam saja
karena semuanya satu paket.

### Opsi B — Folder per lapisan di dalam folder modul

`internal/modules/card/{domain,repository,service,handler,route}/`.

Kelebihan: **batas ditegakkan kompilator, bukan disiplin.** `handler` yang
mencoba menyentuh `repository` harus mengimpornya secara eksplisit, dan itu
terlihat di review. Menambah modul berarti menyalin bentuk yang sudah ada,
sehingga struktur seluruh backend seragam sejak modul kedua.

Kekurangan: lima paket bernama `domain` di lima modul berbeda memaksa alias
impor. Boilerplate per modul bertambah.

### Opsi C — Satu paket per modul dengan interface internal

Kompromi: paket datar, tapi setiap lapisan mengekspos interface. Ditolak karena
memberi kerumitan interface tanpa memberi penegakan kompilator — interface di
dalam satu paket tetap bisa dilewati.

## Keputusan

**Opsi B.**

```
backend/internal/
├── config/      postgres/   httpx/    authz/    fracdex/   events/
│                                                        ← paket bersama
└── modules/
    └── <modul>/
        ├── domain/       entitas + sentinel error. Tidak mengimpor apa pun dari repo ini
        ├── repository/   sqlc hasil generate + akses data
        ├── service/      logika bisnis, batas transaksi
        ├── handler/      decode → validasi → panggil service → encode
        └── route/        pendaftaran route + middleware khusus modul
```

Aturan yang menyertainya:

1. **Arah ketergantungan satu arah:**
   `route → handler → service → repository → domain`.
   Tidak ada panah balik. `domain` tidak mengimpor paket lain di repo ini.

2. **`authz` tetap di luar modul.** `docs/authorization.md` menetapkan satu-
   satunya tempat izin diputuskan, dan itu yang membuat matriksnya bisa diuji
   sebagai satu kesatuan alih-alih tersebar di puluhan modul. Untuk membaca
   keanggotaan, `authz` mendefinisikan interface kecil yang diimplementasikan
   modul `project` — sesuai `rules/20-go.md`: interface didefinisikan di sisi
   konsumen.

3. **Konvensi alias impor, ditegakkan linter.** Setiap impor lintas modul
   memakai alias berpola `<modul><lapisan>`:

   | Paket | Alias |
   |---|---|
   | `modules/card/domain` | `carddom` |
   | `modules/card/repository` | `cardrepo` |
   | `modules/card/service` | `cardsvc` |
   | `modules/card/handler` | `cardhttp` |
   | `modules/card/route` | `cardroute` |

   Ditegakkan lewat linter `importas` di `.golangci.yml`. Konvensi yang hanya
   tertulis akan menyimpang pada modul kelima.

4. **Modul tidak query tabel milik modul lain.** Pembacaan lintas modul lewat
   service pemiliknya. Ini yang mencegah `sqlc` melahirkan tipe `Card` di enam
   paket berbeda, dan yang membuat batas modul berarti sesuatu.

5. **`sqlc.yaml` punya satu entri per modul**, dengan output ke
   `modules/<m>/repository/`. Query `.sql` tinggal di
   `modules/<m>/repository/queries/`. Skema tetap satu, dibaca dari
   `db/migrations/`.

6. **Migration tetap terpusat** di `backend/db/migrations/`, tidak dipecah per
   modul. Urutan migration bersifat global dan tidak boleh bergantung pada
   urutan modul.

## Konsekuensi

### Yang menjadi lebih mudah

- Pelanggaran lapisan menjadi kesalahan kompilasi atau impor yang mencolok di
  diff, bukan temuan yang bergantung pada ketelitian reviewer.
- Modul baru dibuat dengan menyalin bentuk yang sama. Struktur seragam sejak
  modul kedua, bukan setelah refactor di bulan keenam.
- Menghapus sebuah fitur berarti menghapus satu folder.
- Batas modul memaksa kontrak antar-area bisnis dipikirkan lebih awal.

### Yang menjadi lebih sulit

- **Alias impor wajib di mana-mana.** Tanpa linter `importas`, impor akan
  menjadi campuran gaya dan sulit dibaca. Linter itu bagian dari keputusan ini,
  bukan tambahan opsional.
- **Boilerplate per modul bertambah** — lima folder dan minimal lima berkas
  untuk modul sekecil apa pun. Untuk modul yang benar-benar sepele, sebagian
  folder boleh kosong, tapi bentuknya tetap.
- **Query lintas modul jadi lebih mahal ditulis.** Laporan (D5–D7) dan
  pencarian (C5) menyentuh banyak area sekaligus. Keduanya kemungkinan besar
  perlu modul `report` dan `search` tersendiri yang membaca lewat service modul
  lain — dan itu akan terasa berputar dibanding satu `JOIN`. Ini harga yang
  disadari.
- Menyimpang dari tata letak Go yang paling umum. Orang yang baru masuk perlu
  membaca ADR ini lebih dulu.

### Yang perlu diawasi

- Jumlah panggilan antar-service. Kalau sebuah modul memanggil lebih dari tiga
  modul lain, batas modulnya kemungkinan salah tarik.
- Performa `report` dan `search` setelah data penuh. Kalau larangan query
  lintas modul membuat keduanya tidak memenuhi angka di `docs/nfr.md`, aturan
  nomor 4 perlu pengecualian yang tertulis — sebagai ADR baru, bukan sebagai
  kebiasaan yang menyelinap masuk.
- Rasio baris boilerplate terhadap baris logika. Kalau melewati satu banding
  satu, keputusan ini perlu ditinjau ulang.
