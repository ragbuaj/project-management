# ADR-0003: Urutan kartu memakai fractional index bertipe `text`

**Status:** Accepted
**Tanggal:** 2026-08-06
**Pengambil keputusan:** pemilik proyek

## Konteks

Drag & drop (A4) adalah interaksi paling sering dipakai di seluruh aplikasi.
Setiap perpindahan harus menghasilkan urutan baru yang tersimpan, dan sejak
Fase 8 (G5) dua orang bisa memindahkan kartu di kolom yang sama pada saat yang
hampir bersamaan.

Skema urutan sulit diubah setelah ada data — mengubahnya berarti migrasi seluruh
tabel `cards` sambil menjaga urutan yang terlihat pengguna tidak berubah.
Karena itu keputusan ini diambil di Fase 0.

## Opsi yang dipertimbangkan

### Opsi A — `position integer`, rapat (0, 1, 2, 3, …)

Sederhana dan mudah dibaca.

Kekurangan: menyisipkan kartu di posisi 2 mengharuskan `UPDATE` pada semua
kartu sesudahnya. Kolom berisi 200 kartu berarti 198 baris ter-update untuk satu
drag. Dengan realtime, dua orang yang menggeser bersamaan menghasilkan dua
`UPDATE` massal yang saling menimpa, dan hasil akhirnya tidak dapat diprediksi.

### Opsi B — `position integer` dengan celah (100, 200, 300, …)

Menyisipkan berarti mengambil nilai tengah. Menunda masalah, tidak
menghilangkannya: celah habis setelah beberapa puluh penyisipan di titik yang
sama, dan saat itu terjadi tetap dibutuhkan penomoran ulang seluruh kolom —
di jalur permintaan pengguna, tanpa peringatan.

### Opsi C — `position double precision`, ambil nilai tengah

Tidak perlu penomoran ulang sampai presisi habis. Tapi presisi `float64` habis
setelah kira-kira 50 penyisipan berturut-turut di titik yang sama, dan
kegagalannya senyap: dua kartu mendapat nilai identik dan urutannya jadi acak.
`rules/25-postgresql.md` juga melarang `float` untuk nilai yang harus tepat.

### Opsi D — Fractional index bertipe `text` (algoritma yang dipakai Figma)

Posisi adalah string base62 yang selalu bisa disisipi: antara `"a0"` dan `"a1"`
selalu ada `"a0V"`. Satu drag = satu `UPDATE` pada satu baris, apa pun ukuran
kolomnya. Dua penulisan bersamaan menghasilkan dua string berbeda — hasilnya
mungkin bukan urutan yang diinginkan salah satu pengguna, tapi tidak pernah
rusak dan tidak pernah kehilangan kartu.

Kekurangan: string memanjang kalau penyisipan berulang di titik yang sama.
Butuh job rebalance berkala. Tidak terbaca manusia.

## Keputusan

**Opsi D.**

```sql
position text NOT NULL CHECK (position <> '')
```

- Urutan dijamin unik per lingkup: `UNIQUE (project_id, status_id, position)
  WHERE deleted_at IS NULL AND archived_at IS NULL`.
- Perbandingan memakai kolasi biner (`COLLATE "C"`) supaya urutan string
  deterministik dan tidak bergantung locale server.
- Lingkup urutan adalah `(project_id, status_id)`, **bukan** `(board_id, ...)`.
  Beberapa board dari satu project berbagi urutan yang sama. Ini
  penyederhanaan sadar: urutan per-board akan membutuhkan tabel posisi
  tersendiri, dan tidak ada kebutuhan yang menuntutnya.
- Algoritma penghasil kunci ditulis sekali di `internal/fracdex`, dengan test
  properti: hasil `Between(a, b)` selalu berada di antara `a` dan `b`, untuk
  ribuan pasangan acak.
- Job `rebalance_positions` dijalankan harian per `(project_id, status_id)`
  yang panjang `position` maksimumnya melewati 40 karakter. Job ini menulis
  ulang seluruh posisi di lingkup itu dalam satu transaksi.

Penolakan opsi lain: A dan B ditolak karena biaya tulis dan perilaku yang rusak
saat bersamaan; C ditolak karena kegagalannya senyap dan melanggar
`rules/25-postgresql.md`.

Urutan `board_columns`, `checklist_items`, `checklists`, dan `statuses` memakai
skema yang **sama**. Konsistensi lebih murah daripada dua mekanisme.

## Konsekuensi

### Yang menjadi lebih mudah

- Satu drag = satu baris ter-update, konstan terhadap jumlah kartu.
- Optimistic update di frontend jadi sederhana: klien bisa menghitung
  `position` yang sama dengan yang akan dihitung server.
- Fase 8 (realtime) tidak butuh penanganan konflik khusus untuk urutan.

### Yang menjadi lebih sulit

- **Tidak terbaca manusia.** Debugging urutan butuh `ORDER BY position` dan
  membandingkan, bukan membaca angka.
- **Butuh job rebalance.** Satu mekanisme tambahan yang harus ada dan dipantau.
- **Ekspor JSON (H6)** harus menyertakan `position` apa adanya supaya impor
  ulang menghasilkan urutan yang sama.

### Yang perlu diawasi

- Panjang `position` maksimum per lingkup. Kalau ada yang melewati 40 karakter
  di antara dua jalannya job rebalance, jadwalnya perlu dipercepat.
- Waktu jalan job rebalance saat sebuah kolom berisi ribuan kartu.
