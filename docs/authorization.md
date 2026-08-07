# Matriks Otorisasi

Dokumen yang paling sering hilang dan paling mahal ketika hilang. Tanpa daftar
eksplisit, pengecekan kepemilikan diputuskan per endpoint oleh orang yang
berbeda di fase yang berbeda, dan salah satunya akan lupa.

**Setiap baris di matriks ini harus punya test yang membuktikannya.** Test
positif (yang berhak bisa) dan test negatif (yang tidak berhak dapat `404`).

## Peran

| Peran | Keterangan | Sumber kebenaran |
|---|---|---|
| `guest` | Belum autentikasi | — |
| `share` | Pemegang share link. Baca-saja, satu board, tanpa identitas | `share_links` (hash token, belum kedaluwarsa, belum dicabut) |
| `viewer` | Anggota project, hanya membaca | Peran efektif — lihat di bawah |
| `member` | Anggota project, bisa mengubah pekerjaan | Peran efektif — lihat di bawah |
| `admin` | Anggota project dengan hak pengaturan | Peran efektif — lihat di bawah |
| `owner` | Pemilik instalasi. Satu orang | `users.is_owner = true` |

**Peran atas sebuah project punya dua sumber sejak
[ADR-0011](adr/0011-folder-dan-pewarisan-keanggotaan.md):** `project_members`
dan `folder_members` dari folder tempat project itu berada.

> **Peran efektif adalah yang tertinggi di antara keduanya.** Urutan:
> `viewer` < `member` < `admin`.

Yang tertinggi, bukan yang lebih spesifik. Undangan ke folder dimaksudkan
memberi akses; kalau peran project yang lebih rendah menimpanya, menambahkan
seseorang ke sebuah folder akan diam-diam mencabut hak yang sudah ia punya di
salah satu project di dalamnya.

Konsekuensi yang harus dinyatakan terang: **`admin` sebuah folder adalah
`admin` setiap project di dalamnya**, dan memindahkan project ke sebuah folder
memberi akses kepada seluruh anggota folder itu.

**Peran dibaca dari data server pada setiap permintaan, tidak pernah dari klaim
di dalam token.** Ini konsekuensi langsung dari
[ADR-0005](adr/0005-autentikasi-sesi-cookie.md): sesi opak, bukan JWT, sehingga
pencabutan dan perubahan peran berlaku seketika.

`owner` **bukan** superuser terhadap isi project. Owner yang bukan anggota
sebuah project tidak bisa membaca kartunya. Yang bisa dilakukan owner ada di
tabel terpisah di bawah. Ini disengaja: instalasi ini menyimpan pekerjaan rekan,
dan "saya pemilik server" bukan alasan untuk membaca semuanya tanpa jejak.

Kolom `owner` di matriks karena itu **hanya** diisi untuk baris yang muncul di
daftar tertutup itu. Sampai 2026-08-08 tiga baris pengelolaan anggota keliru
memberinya `ya` sekaligus, padahal daftar tertutupnya tidak memuatnya —
ditemukan saat baris-baris ini diterjemahkan menjadi tabel di `internal/authz`,
dan diselesaikan dengan memenangkan daftar tertutupnya. Owner yang perlu
mengelola anggota sebuah project menambahkan dirinya sebagai `admin` lebih
dulu: langkah yang tercatat dan yang memberi tahu anggotanya.

## Aturan yang berlaku umum

Berlaku di seluruh matriks, tidak diulang per baris:

1. **Sumber daya yang bukan hak pemanggil dikembalikan sebagai `404`, bukan
   `403`** — supaya keberadaannya tidak bocor. `403` hanya dipakai ketika
   pemanggil terbukti berhak melihat sumber daya tapi tidak berhak melakukan
   aksinya (contoh: `viewer` mencoba mengubah kartu yang boleh ia lihat).
2. **Otorisasi ditegakkan di server, selalu.** Kontrol di antarmuka adalah
   kenyamanan, bukan keamanan.
3. **Setiap pemeriksaan dilakukan terhadap baris data spesifik**, bukan sekadar
   "sudah login". Kalau ID bisa ditebak, asumsikan sudah ditebak.
4. **Semua izin diputuskan di `internal/authz`.** Handler bertanya, tidak
   memutuskan.
5. **Keanggotaan diwarisi ke seluruh isi project.** Hak atas sebuah kartu
   diturunkan dari `cards.project_id`, bukan disimpan di kartu. Sejak
   ADR-0011, hak atas project sendiri bisa diturunkan dari `projects.folder_id`
   — dan `authz.EffectiveRole` adalah satu-satunya tempat yang tahu itu.
6. **API token bertindak sebagai penggunanya, lalu dipersempit oleh `scopes`.**
   Token tidak pernah menambah hak; ia hanya bisa mengurangi.
7. **`share` tidak pernah menyentuh endpoint biasa.** Ia dilayani jalur
   terpisah `/api/v1/public/*` yang mengembalikan bentuk data yang sudah
   dipangkas — bukan endpoint biasa dengan pengecualian otorisasi.
8. **Peran `viewer` tidak bisa menulis apa pun**, termasuk komentar dan
   pencatatan waktu.

## Matriks

Legenda: **ya** = diizinkan · **—** = ditolak · **milik** = hanya baris miliknya

### Folder

Kolom peran di bawah adalah peran **di folder itu** (`folder_members.role`).

| Sumber daya | Aksi | guest | share | viewer | member | admin | owner | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|:--:|---|
| Folder | buat | — | — | ya | ya | ya | ya | Setiap akun aktif boleh. Pembuat otomatis jadi `admin` folder |
| Folder | lihat | — | — | ya | ya | ya | ya | Hanya folder tempat ia jadi anggota. Project di dalamnya yang bukan haknya tetap tidak terlihat |
| Folder | ubah nama | — | — | — | — | ya | — | — |
| Folder | hapus | — | — | — | — | ya | — | Project di dalamnya menjadi tanpa folder, **tidak** ikut terhapus |
| Anggota folder | lihat daftar | — | — | ya | ya | ya | — | — |
| Anggota folder | undang / ubah peran / keluarkan | — | — | — | — | ya | — | Aturan yang sama dengan anggota project: tidak boleh mengubah peran diri sendiri, tidak boleh menurunkan `admin` terakhir |
| Project | pindahkan ke folder | — | — | — | — | ya | — | Butuh `admin` efektif atas project **dan** `admin` atas folder tujuan. **Ini perubahan akses** — tercatat di `activity_events` |
| Project | keluarkan dari folder | — | — | — | — | ya | — | Butuh `admin` efektif atas project. Tercatat di `activity_events` |

### Project & keanggotaan

Kolom peran di bawah adalah **peran efektif** — yang tertinggi antara peran
project dan peran folder induknya.

| Sumber daya | Aksi | guest | share | viewer | member | admin | owner | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|:--:|---|
| Project | lihat | — | — | ya | ya | ya | ya | Hanya project tempat ia jadi anggota, langsung maupun lewat folder. `owner` hanya kalau anggota |
| Project | buat | — | — | ya | ya | ya | ya | **Setiap akun aktif boleh**, bukan hanya `owner` instalasi. Pembuat otomatis jadi `admin`, dan tercatat di `projects.created_by`. Kalau dibuat di dalam folder, butuh `member` atau `admin` folder itu |
| Project | ubah nama/deskripsi | — | — | — | — | ya | — | Harus anggota |
| Project | ubah `key` | — | — | — | — | — | — | **Tidak boleh siapa pun.** Mengubah `key` mengubah setiap nomor kartu yang pernah ditulis di commit, chat, dan dokumen |
| Project | arsipkan | — | — | — | — | ya | — | Harus anggota |
| Project | hapus permanen | — | — | — | — | — | ya | Harus anggota. Butuh konfirmasi mengetik `key` |
| Anggota | lihat daftar | — | — | ya | ya | ya | ya | Hanya anggota project itu |
| Anggota | undang | — | — | — | — | ya | — | Undangan hanya menyasar orang yang **sudah** punya akun |
| Anggota | ubah peran | — | — | — | — | ya | — | **Tidak boleh mengubah peran diri sendiri.** Tidak boleh menurunkan `admin` terakhir |
| Anggota | keluarkan | — | — | — | — | ya | — | Tidak boleh mengeluarkan diri sendiri lewat endpoint ini; ada endpoint "keluar" terpisah |
| Pengguna | undang ke instalasi | — | — | — | — | — | ya | Pendaftaran mandiri adalah non-goal |
| Pengguna | nonaktifkan | — | — | — | — | — | ya | Tidak boleh menonaktifkan diri sendiri |
| Pengguna | ubah profil sendiri | — | — | milik | milik | milik | milik | — |
| Pengguna | ganti password | — | — | milik | milik | milik | milik | Wajib memasukkan password lama |
| Sesi | lihat & cabut | — | — | milik | milik | milik | milik | Hanya sesinya sendiri, termasuk untuk `owner` |

### Board, kolom, status

| Sumber daya | Aksi | guest | share | viewer | member | admin | owner | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|:--:|---|
| Board | lihat | — | ya | ya | ya | ya | — | `share`: hanya board yang ditunjuk tautannya |
| Board | buat / ubah / arsipkan | — | — | — | — | ya | — | — |
| Column | lihat | — | ya | ya | ya | ya | — | — |
| Column | buat / ubah / urutkan / hapus | — | — | — | — | ya | — | Menghapus kolom tidak menyentuh kartu ([ADR-0004](adr/0004-status-terpisah-dari-column.md)) |
| Column | ubah WIP limit | — | — | — | — | ya | — | — |
| Status | lihat | — | ya | ya | ya | ya | — | — |
| Status | buat / ubah nama / ubah kategori | — | — | — | — | ya | — | Kategori terakhir per jenis tidak boleh dikosongkan |
| Status | hapus | — | — | — | — | ya | — | Ditolak kalau masih ada kartu yang memakainya |
| Label | lihat | — | ya | ya | ya | ya | — | — |
| Label | buat / ubah / hapus | — | — | — | ya | ya | — | — |

### Kartu dan isinya

| Sumber daya | Aksi | guest | share | viewer | member | admin | owner | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|:--:|---|
| Card | lihat | — | ya | ya | ya | ya | — | `share`: hanya kartu yang statusnya tampil di board itu |
| Card | buat | — | — | — | ya | ya | — | — |
| Card | ubah judul/deskripsi/prioritas/tanggal | — | — | — | ya | ya | — | — |
| Card | pindahkan (status & posisi) | — | — | — | ya | ya | — | Transisi harus diizinkan `workflow_transitions` (Fase 7) |
| Card | assign ke orang lain | — | — | — | ya | ya | — | Target harus anggota project yang sama |
| Card | arsipkan | — | — | — | ya | ya | — | — |
| Card | buang ke sampah | — | — | — | ya | ya | — | — |
| Card | pulihkan dari sampah | — | — | — | ya | ya | — | — |
| Card | hapus permanen | — | — | — | — | ya | — | Sebelum 30 hari retensi habis |
| Comment | lihat | — | — | ya | ya | ya | — | **`share` tidak melihat komentar.** Isi diskusi bukan bagian dari status publik |
| Comment | tulis | — | — | — | ya | ya | — | — |
| Comment | ubah | — | — | — | milik | milik | — | **Admin pun tidak boleh mengubah tulisan orang lain** |
| Comment | hapus | — | — | — | milik | ya | — | Admin boleh menghapus (moderasi), tidak boleh mengubah |
| Checklist & item | lihat | — | ya | ya | ya | ya | — | — |
| Checklist & item | ubah / centang | — | — | — | ya | ya | — | — |
| Card link | lihat | — | ya | ya | ya | ya | — | Hanya kartu yang boleh dilihat pemanggil yang ditampilkan |
| Card link | buat / hapus | — | — | — | ya | ya | — | Kedua kartu harus berada di project yang boleh diakses pemanggil |
| Activity per kartu | lihat | — | — | ya | ya | ya | — | — |

### Perencanaan, waktu, laporan

| Sumber daya | Aksi | guest | share | viewer | member | admin | owner | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|:--:|---|
| Sprint | lihat | — | — | ya | ya | ya | — | — |
| Sprint | buat / ubah | — | — | — | ya | ya | — | — |
| Sprint | mulai / tutup | — | — | — | — | ya | — | Menutup sprint memindahkan sisa kartu — aksi yang sulit dibalik |
| Backlog | lihat / susun | — | — | ya (lihat) | ya | ya | — | — |
| Time log | lihat milik sendiri | — | — | — | milik | milik | — | — |
| Time log | lihat semua di project | — | — | — | — | ya | — | Jam kerja orang lain bukan informasi umum |
| Time log | catat / ubah / hapus | — | — | — | milik | milik | — | **Admin tidak boleh mengubah catatan waktu orang lain** |
| Laporan waktu | lihat agregat project | — | — | — | — | ya | — | — |
| Burndown / velocity / CFD | lihat | — | — | ya | ya | ya | — | — |
| Dashboard | lihat | — | — | milik | milik | milik | milik | Hanya project tempat ia jadi anggota |
| Saved filter | lihat | — | — | milik + dibagikan | milik + dibagikan | milik + dibagikan | milik | — |
| Saved filter | buat / ubah / hapus | — | — | milik | milik | milik | milik | — |
| Audit trail project | lihat | — | — | — | — | ya | — | — |
| Ekspor JSON project | jalankan | — | — | — | — | ya | — | Tercatat di `activity_events` — siapa mengekspor apa dan kapan |

### Otomatisasi, integrasi, akses program

| Sumber daya | Aksi | guest | share | viewer | member | admin | owner | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|:--:|---|
| Automation rule | lihat | — | — | — | ya | ya | — | — |
| Automation rule | buat / ubah / aktifkan | — | — | — | — | ya | — | — |
| Automation run (riwayat & error) | lihat | — | — | — | ya | ya | — | Kegagalan harus terlihat, bukan hanya oleh admin |
| Workflow transition | lihat | — | — | ya | ya | ya | — | — |
| Workflow transition | ubah | — | — | — | — | ya | — | — |
| Template & recurring | lihat | — | — | ya | ya | ya | — | — |
| Template & recurring | buat / ubah | — | — | — | ya | ya | — | — |
| Share link | lihat daftar | — | — | — | — | ya | — | — |
| Share link | buat / cabut | — | — | — | — | ya | — | Membuat tautan tanpa autentikasi — tercatat di audit |
| VCS connection | lihat | — | — | — | — | ya | — | Kredensial **tidak pernah** dikembalikan, bahkan ke admin |
| VCS connection | pasang / cabut | — | — | — | — | ya | — | — |
| VCS link pada kartu | buat / hapus | — | — | — | ya | ya | — | — |
| API token | lihat daftar | — | — | milik | milik | milik | milik | Nilai token tidak pernah ditampilkan ulang |
| API token | buat | — | — | milik | milik | milik | milik | Scope tidak boleh melebihi hak penggunanya |
| API token | cabut | — | — | milik | milik | milik | ya | Owner boleh mencabut token siapa pun — jalur darurat, tercatat di audit |
| Notification channel (Telegram) | pasang / ubah | — | — | milik | milik | milik | milik | Verifikasi lewat bot sebelum aktif |
| Impor (Trello/CSV) | jalankan | — | — | — | — | ya | — | — |

### Yang bisa dilakukan `owner` di luar keanggotaan

Daftar tertutup. Selain ini, `owner` tidak punya hak istimewa terhadap isi
project. **Semuanya tercatat di `activity_events` dengan penanda khusus.**

| Aksi | Alasan |
|---|---|
| Mengundang & menonaktifkan pengguna instalasi | Pendaftaran mandiri adalah non-goal. Undangan ke sebuah project hanya menyasar orang yang **sudah** punya akun; yang membuat akunnya tetap `owner` |
| Menambahkan dirinya sebagai `admin` ke project mana pun | Jalur pemulihan saat admin terakhir sebuah project hilang. **Tercatat, dan anggota project diberi notifikasi** |
| Mencabut API token milik siapa pun | Respons insiden |
| Menghapus project secara permanen | Harus ada yang bisa |
| Menjalankan backup & ekspor seluruh instalasi | Operasional |

Baris ketiga adalah kompromi yang sadar: `owner` **bisa** pada akhirnya membaca
project mana pun, tapi hanya lewat langkah yang meninggalkan jejak dan
memberitahu anggotanya. Itu perbedaan antara akses darurat dan akses diam-diam.

## Penegakan

| Lapisan | Yang ditegakkan |
|---|---|
| `internal/httpx` middleware | Sesi valid, CSRF, rate limit. **Tidak** memutuskan izin |
| `internal/authz` | Satu-satunya tempat izin diputuskan. Menerima (pemanggil, aksi, sumber daya), mengembalikan boleh/tidak |
| `internal/api` handler | Bertanya ke `authz` **sebelum** memanggil service. Tidak pernah memutuskan sendiri |
| Paket domain | Menegakkan invarian bisnis (transisi status, satu sprint aktif), bukan izin |
| PostgreSQL | Constraint, FK komposit `(status_id, project_id)`. Jaring pengaman terakhir |

Row-Level Security **tidak** dipakai. Alasannya: satu backend, satu identitas
koneksi database, dan RLS akan memindahkan logika izin ke tempat yang tidak
terlihat pembaca kode Go — sementara `authz` yang terpusat sudah memberi
jaminan yang sama dengan biaya debugging yang jauh lebih rendah. Kalau suatu
saat ada proses kedua yang menulis ke database yang sama, keputusan ini wajib
ditinjau ulang.

## Kewajiban test

Untuk setiap baris di matriks, minimal:

1. **Positif** — peran yang berhak menerima `2xx`.
2. **Negatif lintas-project** — pengguna yang bukan anggota menerima `404`,
   bukan `403`, dan bukan `200` dengan data kosong.
3. **Negatif peran** — peran yang tidak berhak menerima `403` untuk sumber daya
   yang boleh ia lihat, `404` untuk yang tidak.
4. **Kepemilikan baris** — untuk baris bertanda "milik": pengguna A tidak bisa
   menyentuh baris milik pengguna B walau keduanya anggota project yang sama.
5. **Asal hak** — sejak [ADR-0011](adr/0011-folder-dan-pewarisan-keanggotaan.md),
   setiap hak bisa datang dari dua arah. Minimal satu baris test membuktikan
   hak yang **hanya** datang dari folder berlaku, dan satu lagi membuktikan
   peran folder yang lebih tinggi menang atas peran project yang lebih rendah.
6. **Perpindahan folder** — memindahkan project ke folder lain adalah
   perubahan akses. Test tersendiri: orang yang sebelumnya menerima `404`
   menerima `200` sesudahnya, dan sebaliknya saat project dikeluarkan.

Empat pola ini ditulis sebagai table-driven test bersama (`rules/20-go.md`),
bukan disalin per endpoint. Menambah endpoint berarti menambah baris di tabel
test — kalau tidak ditambahkan, itu harus terlihat sebagai kegagalan, bukan
sebagai celah yang lolos diam-diam.

**Fase 2 adalah fase dengan risiko keamanan tertinggi di seluruh roadmap.**
Jalankan skill `security-audit` sebelum merge, dengan urutan pemeriksaan yang
dimulai dari otorisasi tingkat objek.
