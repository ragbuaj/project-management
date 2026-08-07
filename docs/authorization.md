# Matriks Otorisasi

Dokumen yang paling sering hilang dan paling mahal ketika hilang. Tanpa daftar
eksplisit, pengecekan kepemilikan diputuskan per endpoint oleh orang yang
berbeda di fase yang berbeda, dan salah satunya akan lupa.

**Setiap baris di matriks ini harus punya test yang membuktikannya.** Test
positif (yang berhak bisa) dan test negatif (yang tidak berhak dapat `404`).

## Peran

Hak datang dari **peran akun** ([ADR-0012](adr/0012-peran-akun-dan-akses-owner.md)).
Keanggotaan tidak membawa peran; ia hanya menentukan folder dan project mana
yang berada dalam jangkauan seseorang.

| Peran akun | Boleh membuat folder & project | Di dalam yang ia jadi anggotanya | Sumber kebenaran |
|---|:--:|---|---|
| `owner` | ya | segalanya, **tanpa perlu jadi anggota** | `users.role = 'owner'` |
| `project_manager` | ya | mengatur bentuknya: status, board, sprint, anggota | `users.role` |
| `member` | — | mengubah pekerjaan: kartu, komentar, catatan waktu | `users.role` |
| `viewer` | — | membaca saja, tidak menulis apa pun | `users.role` |

Dua peran lain bukan peran akun dan tidak pernah tersimpan di sana:

| | Keterangan | Sumber kebenaran |
|---|---|---|
| `guest` | Belum autentikasi | — |
| `share` | Pemegang share link. Baca-saja, satu board, tanpa identitas | `share_links` (hash token, belum kedaluwarsa, belum dicabut) |

### Setiap keputusan izin punya dua bagian

> **Jangkauan** — apakah pemanggil anggota folder atau project ini?
> **Hak** — apakah peran akunnya boleh melakukan aksi ini?

Keduanya harus terpenuhi, dan keduanya diperiksa terpisah. Kolom peran di
matriks di bawah menjawab bagian kedua; kolom "Aturan kepemilikan" menjawab
bagian pertama.

**Anggota sebuah folder adalah anggota setiap project di dalamnya**
([ADR-0011](adr/0011-folder-dan-pewarisan-keanggotaan.md)). Yang diwariskan
adalah keanggotaan, bukan peran — peran tidak pernah berpindah karena orangnya
membawanya sendiri.

### Owner

**`owner` melewati bagian jangkauan.** Ia bisa melihat dan melakukan apa pun di
setiap folder dan project, tanpa perlu menjadi anggota. Karena itu ia tidak
punya kolom di matriks: jawabannya `ya` di setiap baris.

Yang menyertainya: **akses owner di luar keanggotaannya tercatat di
`activity_events` dengan penanda khusus**, dan terlihat di audit trail project
itu. Kekuasaan tanpa jejak tidak bisa ditinjau — termasuk oleh owner sendiri.

Sampai 2026-08-08 dokumen ini menyatakan sebaliknya, dengan daftar tertutup
yang menahan owner dari isi project. Itu model untuk layanan yang melayani
banyak organisasi asing satu sama lain; instalasi ini dipakai satu perusahaan,
dan pemiliknya bertanggung jawab atas pekerjaan di dalamnya. Alasan lengkapnya
di ADR-0012.

**Peran dibaca dari data server pada setiap permintaan, tidak pernah dari klaim
di dalam token.** Ini konsekuensi langsung dari
[ADR-0005](adr/0005-autentikasi-sesi-cookie.md): sesi opak, bukan JWT, sehingga
pencabutan dan perubahan peran berlaku seketika.

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

Kolom peran di bawah adalah peran **akun** pemanggil. Kolom terakhir yang
menyatakan syarat keanggotaannya.

| Sumber daya | Aksi | guest | share | viewer | member | project_manager | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|---|
| Folder | buat | — | — | — | — | ya | Hanya `project_manager` dan `owner`. Pembuat otomatis jadi anggota |
| Folder | lihat | — | — | ya | ya | ya | Hanya folder tempat ia jadi anggota. Project di dalamnya yang bukan haknya tetap tidak terlihat |
| Folder | ubah nama | — | — | — | — | ya | — |
| Folder | hapus | — | — | — | — | ya | Project di dalamnya menjadi tanpa folder, **tidak** ikut terhapus |
| Anggota folder | lihat daftar | — | — | ya | ya | ya | — |
| Anggota folder | tambahkan / keluarkan | — | — | — | — | ya | Harus anggota folder itu. Tidak boleh mengeluarkan diri sendiri lewat endpoint ini |
| Project | pindahkan ke folder | — | — | — | — | ya | Harus anggota project **dan** anggota folder tujuan. **Ini perubahan akses** — tercatat di `activity_events` |
| Project | keluarkan dari folder | — | — | — | — | ya | Harus anggota project. Tercatat di `activity_events` |

### Project & keanggotaan

Kolom peran di bawah adalah peran **akun** pemanggil. Kecuali `owner`, setiap
baris juga menuntut keanggotaan — langsung, atau lewat folder induknya.

| Sumber daya | Aksi | guest | share | viewer | member | project_manager | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|---|
| Project | lihat | — | — | ya | ya | ya | Hanya project tempat ia jadi anggota, langsung maupun lewat folder. `owner` hanya kalau anggota |
| Project | buat | — | — | — | — | ya | Hanya `project_manager` dan `owner`. Pembuat otomatis jadi anggota, dan tercatat di `projects.created_by`. Kalau dibuat di dalam folder, ia harus anggota folder itu |
| Project | ubah nama/deskripsi | — | — | — | — | ya | Harus anggota |
| Project | ubah `key` | — | — | — | — | — | **Tidak boleh siapa pun.** Mengubah `key` mengubah setiap nomor kartu yang pernah ditulis di commit, chat, dan dokumen |
| Project | arsipkan | — | — | — | — | ya | Harus anggota |
| Project | hapus permanen | — | — | — | — | — | Harus anggota. Butuh konfirmasi mengetik `key` |
| Anggota | lihat daftar | — | — | ya | ya | ya | Hanya anggota project itu |
| Anggota | tambahkan | — | — | — | — | ya | Hanya orang yang **sudah** punya akun. Berlaku langsung, tanpa persetujuan yang diundang |
| Anggota | keluarkan | — | — | — | — | ya | Tidak boleh mengeluarkan diri sendiri lewat endpoint ini; ada endpoint "keluar" terpisah |
| Pengguna | undang ke instalasi | — | — | — | — | — | Pendaftaran mandiri adalah non-goal |
| Pengguna | nonaktifkan | — | — | — | — | — | Tidak boleh menonaktifkan diri sendiri |
| Pengguna | ubah profil sendiri | — | — | milik | milik | milik | — |
| Pengguna | ganti password | — | — | milik | milik | milik | Wajib memasukkan password lama |
| Sesi | lihat & cabut | — | — | milik | milik | milik | Hanya sesinya sendiri, termasuk untuk `owner` |

### Board, kolom, status

| Sumber daya | Aksi | guest | share | viewer | member | project_manager | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|---|
| Board | lihat | — | ya | ya | ya | ya | `share`: hanya board yang ditunjuk tautannya |
| Board | buat / ubah / arsipkan | — | — | — | — | ya | — |
| Column | lihat | — | ya | ya | ya | ya | — |
| Column | buat / ubah / urutkan / hapus | — | — | — | — | ya | Menghapus kolom tidak menyentuh kartu ([ADR-0004](adr/0004-status-terpisah-dari-column.md)) |
| Column | ubah WIP limit | — | — | — | — | ya | — |
| Status | lihat | — | ya | ya | ya | ya | — |
| Status | buat / ubah nama / ubah kategori | — | — | — | — | ya | Kategori terakhir per jenis tidak boleh dikosongkan |
| Status | hapus | — | — | — | — | ya | Ditolak kalau masih ada kartu yang memakainya |
| Label | lihat | — | ya | ya | ya | ya | — |
| Label | buat / ubah / hapus | — | — | — | ya | ya | — |

### Kartu dan isinya

| Sumber daya | Aksi | guest | share | viewer | member | project_manager | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|---|
| Card | lihat | — | ya | ya | ya | ya | `share`: hanya kartu yang statusnya tampil di board itu |
| Card | buat | — | — | — | ya | ya | — |
| Card | ubah judul/deskripsi/prioritas/tanggal | — | — | — | ya | ya | — |
| Card | pindahkan (status & posisi) | — | — | — | ya | ya | Transisi harus diizinkan `workflow_transitions` (Fase 7) |
| Card | assign ke orang lain | — | — | — | ya | ya | Target harus anggota project yang sama |
| Card | arsipkan | — | — | — | ya | ya | — |
| Card | buang ke sampah | — | — | — | ya | ya | — |
| Card | pulihkan dari sampah | — | — | — | ya | ya | — |
| Card | hapus permanen | — | — | — | — | ya | Sebelum 30 hari retensi habis |
| Comment | lihat | — | — | ya | ya | ya | **`share` tidak melihat komentar.** Isi diskusi bukan bagian dari status publik |
| Comment | tulis | — | — | — | ya | ya | — |
| Comment | ubah | — | — | — | milik | milik | **Admin pun tidak boleh mengubah tulisan orang lain** |
| Comment | hapus | — | — | — | milik | ya | Admin boleh menghapus (moderasi), tidak boleh mengubah |
| Checklist & item | lihat | — | ya | ya | ya | ya | — |
| Checklist & item | ubah / centang | — | — | — | ya | ya | — |
| Card link | lihat | — | ya | ya | ya | ya | Hanya kartu yang boleh dilihat pemanggil yang ditampilkan |
| Card link | buat / hapus | — | — | — | ya | ya | Kedua kartu harus berada di project yang boleh diakses pemanggil |
| Activity per kartu | lihat | — | — | ya | ya | ya | — |

### Perencanaan, waktu, laporan

| Sumber daya | Aksi | guest | share | viewer | member | project_manager | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|---|
| Sprint | lihat | — | — | ya | ya | ya | — |
| Sprint | buat / ubah | — | — | — | ya | ya | — |
| Sprint | mulai / tutup | — | — | — | — | ya | Menutup sprint memindahkan sisa kartu — aksi yang sulit dibalik |
| Backlog | lihat / susun | — | — | ya (lihat) | ya | ya | — |
| Time log | lihat milik sendiri | — | — | — | milik | milik | — |
| Time log | lihat semua di project | — | — | — | — | ya | Jam kerja orang lain bukan informasi umum |
| Time log | catat / ubah / hapus | — | — | — | milik | milik | **Admin tidak boleh mengubah catatan waktu orang lain** |
| Laporan waktu | lihat agregat project | — | — | — | — | ya | — |
| Burndown / velocity / CFD | lihat | — | — | ya | ya | ya | — |
| Dashboard | lihat | — | — | milik | milik | milik | Hanya project tempat ia jadi anggota |
| Saved filter | lihat | — | — | milik + dibagikan | milik + dibagikan | milik + dibagikan | — |
| Saved filter | buat / ubah / hapus | — | — | milik | milik | milik | — |
| Audit trail project | lihat | — | — | — | — | ya | — |
| Ekspor JSON project | jalankan | — | — | — | — | ya | Tercatat di `activity_events` — siapa mengekspor apa dan kapan |

### Otomatisasi, integrasi, akses program

| Sumber daya | Aksi | guest | share | viewer | member | project_manager | Aturan kepemilikan |
|---|---|:--:|:--:|:--:|:--:|:--:|---|
| Automation rule | lihat | — | — | — | ya | ya | — |
| Automation rule | buat / ubah / aktifkan | — | — | — | — | ya | — |
| Automation run (riwayat & error) | lihat | — | — | — | ya | ya | Kegagalan harus terlihat, bukan hanya oleh yang mengaturnya |
| Workflow transition | lihat | — | — | ya | ya | ya | — |
| Workflow transition | ubah | — | — | — | — | ya | — |
| Template & recurring | lihat | — | — | ya | ya | ya | — |
| Template & recurring | buat / ubah | — | — | — | ya | ya | — |
| Share link | lihat daftar | — | — | — | — | ya | — |
| Share link | buat / cabut | — | — | — | — | ya | Membuat tautan tanpa autentikasi — tercatat di audit |
| VCS connection | lihat | — | — | — | — | ya | Kredensial **tidak pernah** dikembalikan, bahkan ke yang memasangnya |
| VCS connection | pasang / cabut | — | — | — | — | ya | — |
| VCS link pada kartu | buat / hapus | — | — | — | ya | ya | — |
| API token | lihat daftar | — | — | milik | milik | milik | Nilai token tidak pernah ditampilkan ulang |
| API token | buat | — | — | milik | milik | milik | Scope tidak boleh melebihi hak penggunanya |
| API token | cabut | — | — | milik | milik | milik | Owner boleh mencabut token siapa pun — jalur darurat, tercatat di audit |
| Notification channel (Telegram) | pasang / ubah | — | — | milik | milik | milik | Verifikasi lewat bot sebelum aktif |
| Impor (Trello/CSV) | jalankan | — | — | — | — | ya | — |

### Yang hanya bisa dilakukan `owner`

Owner boleh segalanya di setiap folder dan project ([ADR-0012](adr/0012-peran-akun-dan-akses-owner.md)).
Yang berikut ini **hanya** ia yang boleh, dan karena itu tidak punya baris di
matriks di atas:

| Aksi | Alasan |
|---|---|
| Menambahkan & menonaktifkan pegawai, dan menetapkan peran akunnya | Pendaftaran mandiri adalah non-goal. Semua akun lahir dari sini |
| Mencabut API token milik siapa pun | Respons insiden |
| Menghapus project secara permanen | Aksi paling merusak yang ada di aplikasi ini |
| Menjalankan backup & ekspor seluruh instalasi | Operasional |

**Setiap akses owner ke folder atau project yang bukan keanggotaannya tercatat
di `activity_events` dengan penanda khusus**, dan terlihat di audit trail
project itu. Itu perbedaan antara kekuasaan yang bisa ditinjau dan kekuasaan
yang tidak.

## Penegakan

| Lapisan | Yang ditegakkan |
|---|---|
| `internal/httpx` middleware | Sesi valid, CSRF, rate limit. **Tidak** memutuskan izin |
| `internal/authz` | Satu-satunya tempat izin diputuskan. Menerima (peran akun, keanggotaan, aksi), mengembalikan boleh/tidak |
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
5. **Keanggotaan lewat folder** — [ADR-0011](adr/0011-folder-dan-pewarisan-keanggotaan.md)
   menjadikan anggota folder anggota setiap project di dalamnya. Minimal satu
   baris test membuktikan akses yang **hanya** datang dari keanggotaan folder
   itu berlaku.
6. **Perpindahan folder** — memindahkan project ke folder lain adalah
   perubahan akses. Test tersendiri: orang yang sebelumnya menerima `404`
   menerima `200` sesudahnya, dan sebaliknya saat project dikeluarkan.
7. **Owner tanpa keanggotaan** — owner menerima `2xx` di project yang bukan
   keanggotaannya, **dan** aksesnya meninggalkan baris di `activity_events`.
   Yang kedua ikut diuji: kekuasaan yang tidak tercatat sama saja dengan
   kekuasaan yang tidak diawasi.

Pola-pola ini ditulis sebagai table-driven test bersama (`rules/20-go.md`),
bukan disalin per endpoint. Menambah endpoint berarti menambah baris di tabel
test — kalau tidak ditambahkan, itu harus terlihat sebagai kegagalan, bukan
sebagai celah yang lolos diam-diam.

**Fase 2 adalah fase dengan risiko keamanan tertinggi di seluruh roadmap.**
Jalankan skill `security-audit` sebelum merge, dengan urutan pemeriksaan yang
dimulai dari otorisasi tingkat objek.
