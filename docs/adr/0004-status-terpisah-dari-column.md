# ADR-0004: Status adalah milik Project; Column hanya tampilan board

**Status:** Accepted
**Tanggal:** 2026-08-06
**Pengambil keputusan:** pemilik proyek

## Konteks

Di Trello, kolom **adalah** keadaan kartu — sebuah kartu berada "di" kolom
Done. Di Jira, status adalah properti kartu dan papan hanya menampilkannya;
satu status bisa muncul di beberapa papan, dan sebuah papan bisa
menyembunyikan status tertentu.

Cakupan yang dipilih memuat E5 (workflow dengan transisi yang dibatasi), D5–D7
(burndown, velocity, CFD), C2/C3/C4 (tampilan tabel, kalender, Gantt yang tidak
punya kolom sama sekali), dan E1 (WIP limit).

Model Trello lebih sederhana untuk Fase 1. Model Jira menjadi wajib paling
lambat di Fase 7. Pertanyaannya: bayar sekarang atau bayar lewat migrasi nanti.

## Opsi yang dipertimbangkan

### Opsi A — Kolom adalah keadaan (`cards.column_id`)

Paling sederhana untuk Fase 1. Satu tabel lebih sedikit.

Kekurangan:
- Tampilan tabel, kalender, dan Gantt tidak punya konsep kolom, tapi tetap
  harus menampilkan dan menyaring keadaan kartu. Keadaan harus diturunkan dari
  join ke kolom yang mungkin sudah dihapus.
- E5 (transisi yang dibatasi) berarti mendefinisikan transisi antar-kolom.
  Menghapus atau menyusun ulang kolom board lalu merusak definisi workflow —
  dua konsep yang berbeda umurnya jadi terikat.
- Burndown dan CFD mengukur perpindahan **kategori** (todo → in progress →
  done). Tanpa kategori yang eksplisit, "Done" hanya bisa ditebak dari nama
  kolom.
- Beberapa board per project (yang sudah ada di Fase 1 lewat A1) langsung
  bermasalah: satu kartu tidak bisa berada di dua kolom.

### Opsi B — Status milik project; Column memetakan ke Status

`cards.status_id` menunjuk `statuses`, yang dimiliki project. `board_columns`
menunjuk `status_id` dan hanya mengatur tampilan: nama tampil, urutan, WIP
limit, dan apakah kolom itu ditampilkan.

Kekurangan: satu tabel dan satu lapisan pemetaan lebih banyak di Fase 1.

## Keputusan

**Opsi B.**

```
statuses        (id, project_id, name, category, position)
                 category ∈ {todo, in_progress, done}

board_columns   (id, board_id, status_id, name, position, wip_limit)
                 UNIQUE (board_id, status_id)
cards           (..., status_id → statuses.id, position, ...)
```

Aturan yang ikut ditetapkan sekarang:

1. **Kartu selalu punya status**, walau tidak ditampilkan di board mana pun.
2. **`status.category` adalah sumber kebenaran untuk "selesai"**, bukan nama
   status. Burndown, velocity, CFD, dan penanda terlambat membaca `category`.
   Ini mencegah bug klasik: seseorang membuat status "Done ✅" dan seluruh
   laporan berhenti menghitungnya.
3. **Setiap project punya minimal satu status per kategori.** Ditegakkan saat
   pembuatan project dan saat penghapusan status.
4. **Menghapus status hanya boleh kalau tidak ada kartu yang memakainya**
   (`ON DELETE RESTRICT`), atau lewat pemindahan eksplisit.
5. **Menghapus kolom board tidak menyentuh kartu.** Kartunya masih ada, dengan
   status yang sama, hanya tidak terlihat di board itu.
6. **`cards.position` bercakupan `(project_id, status_id)`**, bukan per kolom —
   lihat [ADR-0003](0003-urutan-kartu-fractional-index.md).

Di Fase 1, antarmuka menyembunyikan perbedaan ini sepenuhnya: membuat kolom
otomatis membuat status baru dengan nama yang sama. Pengguna tidak melihat dua
konsep sampai Fase 7 membutuhkannya. Yang dibayar sekarang hanya satu tabel
dan satu kolom kunci asing.

Opsi A ditolak karena penghematannya berumur pendek (kira-kira satu tabel
selama enam fase) sementara biayanya adalah migrasi tabel `cards` di Fase 7
plus penulisan ulang setiap query yang menyaring berdasarkan keadaan.

## Konsekuensi

### Yang menjadi lebih mudah

- Tampilan tanpa kolom (C2, C3, C4, C7) menyaring `status_id` secara langsung.
- E5 mendefinisikan transisi antar `statuses` — struktur yang stabil, tidak
  ikut berubah saat board disusun ulang.
- Beberapa board per project bekerja tanpa perlakuan khusus.
- Laporan (D5–D7) membaca `category`, sehingga tahan terhadap penggantian nama
  status.

### Yang menjadi lebih sulit

- Satu lapisan tak terlihat di Fase 1 yang harus dijelaskan ke pembaca kode.
  Glosarium menanggung beban ini.
- Membuat project baru harus menyemai status default, bukan hanya kolom.
- Antarmuka pengaturan kolom di Fase 1 harus memilih: membuat status baru atau
  menampilkan status yang sudah ada. Fase 1 selalu memilih yang pertama;
  pilihannya baru muncul di Fase 7.

### Yang perlu diawasi

- Apakah pengguna (Anda) pernah benar-benar butuh dua board dengan susunan
  status berbeda. Kalau setelah Fase 5 jawabannya tidak pernah, ADR ini
  tetap benar karena E5 sudah menuntutnya — tapi antarmukanya boleh tetap
  menyembunyikan pemetaan selamanya.
