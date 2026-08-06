# ADR-0007: PostgreSQL 18, menggantikan baris database di ADR-0001

**Status:** Proposed
**Tanggal:** 2026-08-06
**Pengambil keputusan:** pemilik proyek
**Menggantikan:** baris `Database` pada tabel keputusan
[ADR-0001](0001-stack-react-go-postgresql.md). Seluruh keputusan lain di
ADR-0001 tetap berlaku.

## Konteks

ADR-0001 menetapkan PostgreSQL 17 tanpa membandingkannya dengan 18 — versi
mayor dipilih sebagai "yang matang" tanpa analisis tersendiri.

Pertanyaan ini muncul kembali saat perencanaan Fase 0, pada satu-satunya saat
biayanya nol: repositori masih kosong, belum ada satu baris data. Setelah data
ada, turun versi mayor PostgreSQL praktis mustahil — `pg_upgrade` hanya
bergerak maju, dan mundur berarti dump, transformasi, dan restore dengan waktu
henti.

Konteks yang berlaku: satu VPS, satu instance, tanpa replika. Disk tunggal.
Puluhan pengguna. Beban baca didominasi pemindaian board dan filter; beban
tulis didominasi perpindahan kartu dan `activity_events`.

## Opsi yang dipertimbangkan

### Opsi A — PostgreSQL 17

Sudah lama beredar, dukungan ekosistem paling tebal, jalur yang paling banyak
ditempuh orang lain. Semua yang dibutuhkan `docs/data-model.md` tersedia:
`jsonb`, full-text search, partisi, `LISTEN/NOTIFY`, generated column `STORED`.

Kekurangan: tidak ada. Ia hanya tidak punya tiga hal yang ada di 18.

### Opsi B — PostgreSQL 18

Menambahkan tiga hal yang menyentuh sistem ini secara langsung:

1. **I/O asinkron.** Pembacaan dilakukan tanpa memblokir proses backend.
   Paling terasa pada mesin dengan disk tunggal dan tanpa replika baca —
   persis topologi kita.
2. **Skip scan pada indeks B-tree multikolom.** Indeks `(a, b)` kini bisa
   melayani query yang hanya menyaring `b`, selama kardinalitas `a` rendah.
   Ini melonggarkan aturan urutan kolom di `rules/25-postgresql.md` dan
   menyentuh beberapa indeks komposit kita — antara lain
   `cards_order_key (project_id, status_id, position)`.
3. **Fungsi bawaan `uuidv7()`.** Bukan pengganti pembuatan ID di aplikasi,
   melainkan nilai `DEFAULT` sebagai jaring pengaman. Lihat bagian Keputusan.

Kekurangan: lebih muda, jadi lebih sedikit jalur yang sudah ditempuh orang
lain. Beberapa alat pihak ketiga mungkin belum menyebutkannya secara eksplisit.

## Keputusan

**Opsi B — PostgreSQL 18.** Opsi A ditolak bukan karena buruk, tapi karena
tidak memberi apa pun yang tidak diberikan 18, sementara 18 memberi tiga hal
yang relevan. Pada repositori kosong, memilih yang lebih baru adalah pilihan
dengan risiko paling rendah; pada repositori berisi data, sebaliknya.

### Pembuatan ID tetap di aplikasi

Ini bagian yang paling mudah disalahpahami, jadi ditulis eksplisit.

**UUID v7 sudah menjadi keputusan sejak `docs/data-model.md` ditulis** — yang
berubah di sini hanya penambahan nilai `DEFAULT`.

| | Keputusan |
|---|---|
| Tipe primary key | `uuid` versi 7. Tidak berubah |
| Siapa yang menghasilkan | **Aplikasi (Go).** Tidak berubah |
| Nilai `DEFAULT` kolom | **Baru:** `DEFAULT uuidv7()` |

Aplikasi tetap menghasilkan ID karena nilainya dibutuhkan **sebelum** `INSERT`:
untuk menulis `activity_events` yang merujuk baris itu, membuat baris anak, dan
menyusun response — semuanya di dalam satu transaksi. Kalau ID dibuat
PostgreSQL, setiap penulisan menunggu `RETURNING` lebih dulu dan alurnya
berlapis tanpa perlu.

`DEFAULT uuidv7()` ada sebagai jaring pengaman untuk `INSERT` manual dari psql,
seed, migration, dan skrip perbaikan data — sekaligus menutup kelas bug
"aplikasi lupa mengisi ID". Nilainya gratis dan tidak pernah terpakai di jalur
normal.

### Kenapa v7, dan apa yang sebenarnya dipercepat

Dicatat di sini supaya tidak ditafsirkan ulang secara keliru di kemudian hari.

| | UUID v4 | UUID v7 |
|---|---|---|
| Pola penulisan B-tree | Acak, tersebar ke seluruh halaman indeks | Berurutan waktu, menempel di ujung kanan |
| Akibat | Page split sering, WAL membengkak, cache hit rendah | Sedikit page split, WAL kecil, cache hit tinggi |
| Scan rentang berdasarkan waktu lewat PK | Tidak bisa | Bisa |

**Yang tidak dipercepat UUID v7:** penyaringan berdasarkan `project_id`,
`status_id`, `assignee_id`, atau `due_date`. Kolom-kolom itu dilayani indeksnya
masing-masing, dan tipe primary key tidak ikut menentukan. Manfaat v7 ada di
sisi penulisan dan lokalitas indeks, bukan di sisi penyaringan.

Tempat pembuatan nilai — Go atau PostgreSQL — **tidak berpengaruh sama sekali**
pada performa query. Byte yang tersimpan identik.

### Yang ikut ditetapkan

- Versi disebut eksplisit di setiap pemeriksaan migration:
  `squawk --pg-version=18`.
- Generated column `search_tsv` tetap ditulis `STORED` secara eksplisit —
  di PostgreSQL 18 nilai bawaannya `VIRTUAL`, dan kolom `VIRTUAL` tidak bisa
  diindeks GIN.
- Image container: `postgres:18` di `local` maupun `production`. Versi mayor
  disamakan; perbedaan versi antar lingkungan adalah sumber kejutan yang
  hanya muncul di produksi.

## Konsekuensi

### Yang menjadi lebih mudah

- Baca konkuren pada satu disk membaik tanpa perubahan kode apa pun.
- Beberapa query yang tadinya butuh indeks tambahan bisa dilayani indeks yang
  sudah ada lewat skip scan — lebih sedikit indeks berarti penulisan lebih
  cepat dan tabel lebih kecil.
- `INSERT` manual dan seed tidak perlu menyediakan ID.

### Yang menjadi lebih sulit

- **Versi lebih muda.** Kalau ada bug spesifik PostgreSQL 18, jumlah orang yang
  sudah menemukannya lebih sedikit. Dimitigasi oleh sifat sistem ini: satu
  instance, data yang bisa diekspor penuh (H6), dan backup yang diuji restore.
- Alat yang memeriksa migration harus diberi tahu versinya. `squawk` tanpa
  `--pg-version` memakai asumsi yang salah dan melaporkan temuan yang keliru.
- **Skip scan membuat indeks yang buruk terasa cukup baik.** Ini justru risiko:
  urutan kolom yang salah tidak lagi langsung terasa lambat. `EXPLAIN (ANALYZE,
  BUFFERS)` tetap wajib sebelum menyatakan sebuah query cepat.

### Yang perlu diawasi

- Rasio cache hit dan waktu tunggu I/O setelah beban naik — untuk mengetahui
  apakah async I/O benar-benar memberi yang dijanjikan pada beban kita.
- Rencana eksekusi query board setelah data mencapai 50.000 kartu. Kalau
  perencana memilih skip scan di tempat yang tidak diharapkan, indeksnya perlu
  ditinjau, bukan dibiarkan.
