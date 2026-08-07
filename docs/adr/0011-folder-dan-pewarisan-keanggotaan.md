# ADR-0011: Folder sebagai wadah project, dengan keanggotaan yang diwariskan

**Status:** Accepted
**Tanggal:** 2026-08-07
**Pengambil keputusan:** pemilik proyek
**Diamandemen:** [ADR-0012](0012-peran-akun-dan-akses-owner.md) pada 2026-08-08
membatalkan seluruh bagian **Peran efektif** di bawah. Peran tidak lagi melekat
pada keanggotaan, sehingga tidak ada dua peran untuk digabungkan. Yang tetap
berlaku: folder sebagai wadah satu tingkat, `projects.folder_id` yang boleh
`NULL`, penghapusan folder yang melepaskan isinya, dan **pewarisan
keanggotaan** — anggota folder adalah anggota setiap project di dalamnya.

## Konteks

Model awal punya satu tingkat: `projects`, dengan `project_members` sebagai
satu-satunya sumber kebenaran otorisasi. Hanya `owner` instalasi yang boleh
membuat project ([authorization.md](../authorization.md)).

Pemilik menetapkan tiga hal pada 2026-08-07:

1. Setiap akun boleh membuat project dan menjadi pemiliknya.
2. Project dikelompokkan ke dalam **folder**.
3. Mengundang seseorang harus bisa dilakukan **di tingkat folder** — sekali,
   berlaku untuk seluruh isinya — atau di tingkat satu project saja.

Poin ketiga yang mahal. Ia berarti keanggotaan tidak lagi punya satu sumber,
dan setiap pemeriksaan izin harus tahu tentang keduanya.

## Keputusan

### Bentuk

| Aspek | Nilai |
|---|---|
| Kedalaman | **Satu tingkat.** Folder berisi project, tidak berisi folder lain |
| Keanggotaan project | Opsional. `projects.folder_id` boleh `NULL` |
| Peran folder | Sama dengan peran project: `viewer`, `member`, `admin` |
| Pembuat folder | Otomatis `admin` folder itu |
| Siapa boleh membuat | Setiap akun aktif, sama seperti project |

**Tidak bersarang, dan itu disengaja.** Pohon membuat peran efektif harus
dihitung rekursif, dan membuat "pindahkan folder" bisa mengubah akses puluhan
orang sekaligus lewat satu aksi yang tampak seperti merapikan. Satu tingkat
bisa dijawab satu join, dan akibat setiap perpindahan masih bisa dibayangkan
orang yang melakukannya.

**Project boleh berdiri sendiri.** Memaksa setiap project punya folder berarti
membuat folder "Umum" yang tidak berarti apa-apa, lalu memberinya anggota yang
tidak dimaksudkan siapa pun.

### Peran efektif

> **Peran efektif seseorang atas sebuah project adalah yang tertinggi antara
> peran folder-nya dan peran project-nya.** Urutan: `viewer` < `member` <
> `admin`.

Yang tertinggi, bukan yang terendah dan bukan yang lebih spesifik. Undangan ke
folder dimaksudkan **memberi** akses. Kalau yang terendah menang, menambahkan
seseorang ke folder sebagai `viewer` diam-diam menurunkan haknya di project
tempat ia `admin` — sebuah pencabutan hak yang tidak diminta siapa pun dan
tidak terlihat di layar mana pun.

Konsekuensinya harus dinyatakan terang: **`admin` sebuah folder adalah `admin`
setiap project di dalamnya.** Memindahkan project ke folder berarti memberi
akses kepada semua anggota folder itu.

### Yang ikut berubah karenanya

| Aksi | Aturan |
|---|---|
| Pindahkan project ke folder | Butuh `admin` efektif atas project **dan** `admin` atas folder tujuan. Tercatat di `activity_events` |
| Keluarkan project dari folder | Butuh `admin` efektif atas project. Project menjadi tanpa folder, bukan terhapus |
| Hapus folder | Project di dalamnya menjadi tanpa folder. **Tidak** ikut terhapus |
| Undang ke project | Menawarkan dua sasaran: folder ini, atau project ini saja |

Menghapus folder tidak boleh menghapus isinya. Satu aksi yang membuang
pekerjaan banyak orang tidak boleh punya nama sesederhana "hapus folder".

### Penegakan

Seluruh pewarisan hidup di satu tempat:

```
authz.EffectiveRole(ctx, userID, projectID) (Role, error)
```

Ini satu-satunya fungsi yang tahu bahwa folder ada. Handler tidak pernah
membaca `project_members` maupun `folder_members` langsung — aturan yang sudah
berlaku di [authorization.md](../authorization.md) §4, dan yang sekarang punya
alasan kedua: dua sumber kebenaran yang digabung di banyak tempat akan digabung
berbeda di salah satunya.

Aturan `404` untuk sumber daya yang bukan hak pemanggil tidak berubah. Yang
berubah hanya cara peran itu ditemukan.

## Opsi yang ditolak

**Folder sebagai label/pengelompokan visual saja.** Paling murah — `authz`
tidak berubah sama sekali. Ditolak karena tidak menjawab yang diminta:
mengundang orang ke sepuluh project tetap sepuluh kali kerja.

**Peran efektif = peran yang paling spesifik (project menimpa folder).**
Terdengar rapi, tapi berarti keanggotaan project yang lebih rendah membatalkan
keanggotaan folder yang lebih tinggi. Seseorang yang sudah `viewer` di satu
project tidak akan pernah mendapat hak dari undangan folder, dan tidak ada
apa pun di layar yang menjelaskan kenapa.

**Folder bersarang.** Ditolak dengan alasan di bagian Bentuk.

## Konsekuensi

### Yang menjadi lebih mudah

- Mengundang rekan ke sekelompok project sekaligus.
- Menata project menurut klien, tim, atau tahun tanpa menyalin keanggotaan.

### Yang menjadi lebih sulit

- **Setiap pemeriksaan izin menyentuh dua tabel.** Satu join tambahan pada
  jalur terpanas di aplikasi. Perlu indeks pada `folder_members (user_id)` dan
  `projects (folder_id)`, dan kalau terasa, cache per permintaan — bukan cache
  lintas permintaan, karena perubahan peran harus berlaku seketika
  ([ADR-0005](0005-autentikasi-sesi-cookie.md)).
- **Memindahkan project mengubah siapa yang bisa melihatnya.** Ini kelas bug
  kebocoran data yang tidak ada sebelumnya. Ia wajib punya test sendiri, bukan
  hanya baris di matriks.
- **Matriks otorisasi bertambah satu dimensi.** Setiap baris yang sebelumnya
  dijawab "peran di project" sekarang dijawab "peran efektif", dan keempat pola
  test wajib di [authorization.md](../authorization.md) harus dijalankan untuk
  hak yang datang dari folder maupun dari project.

### Yang perlu diawasi

- Folder dengan banyak anggota dan banyak project. Ia adalah satu tempat yang
  bisa membuka banyak hal sekaligus.
- Perpindahan project antar-folder. Setiap kejadiannya adalah perubahan akses.

## Waktu pelaksanaan

Skema folder ditambahkan **sekarang**, di Fase 0, sebelum `internal/authz`
ditulis — bukan nanti bersama endpoint-nya. Alasannya sama dengan
[ADR-0007](0007-postgresql-18.md): database masih kosong, dan ini satu-satunya
saat perubahannya berbiaya nol. Menulis `authz` tanpa folder lebih dulu berarti
menulis ulang seluruh test-nya begitu folder datang.

Endpoint folder sendiri menyusul di fase yang menangani project, mengikuti
aturan [openapi.yaml](../api/openapi.yaml): kontrak ditulis di PR yang sama
dengan implementasinya.
