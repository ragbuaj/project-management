# ADR-0012: Hak dari peran akun, keanggotaan hanya menentukan jangkauan

**Status:** Accepted
**Tanggal:** 2026-08-08
**Pengambil keputusan:** pemilik proyek

**Mengamandemen:** [ADR-0011](0011-folder-dan-pewarisan-keanggotaan.md) bagian
peran efektif.

## Konteks

Instalasi ini dipakai satu perusahaan, dan penggunanya adalah pegawai. Akun
tidak lahir dari pendaftaran — owner menambahkan pegawai sekaligus membuatkan
akunnya. Pendaftaran mandiri adalah non-goal sejak awal
([product-brief.md](../product-brief.md)).

Rancangan sebelumnya menaruh hak pada **keanggotaan**: seseorang bisa `admin`
di satu project dan `viewer` di project lain. Itu model produk SaaS yang
melayani banyak organisasi asing satu sama lain. Untuk satu perusahaan yang
pegawainya sudah punya jabatan, ia menduplikasi jabatan itu ke setiap baris
keanggotaan — dan menyisakan pertanyaan yang tidak punya jawaban benar: kalau
seorang manajer proyek diundang ke project lain, ia diundang sebagai apa.

[glossary.md](../glossary.md) mencatat `user_role` sebagai kolom di `users`
sebagai pola yang **ditolak**. Keputusan ini membalikkannya, dengan alasan yang
tidak ada waktu itu: penggunanya bukan pengguna umum, melainkan pegawai yang
perannya sudah ditetapkan sebelum ia menyentuh aplikasi ini.

## Keputusan

### Hak datang dari peran akun

`users.role`, satu nilai per akun:

| Peran akun | Boleh membuat folder & project | Di dalam yang ia jadi anggotanya |
|---|---|---|
| `owner` | ya | segalanya, **dan tidak perlu jadi anggota** |
| `maintainer` | ya | mengatur bentuknya: status, board, sprint, anggota |
| `contributor` | tidak | mengubah pekerjaan: kartu, komentar, catatan waktu |
| `viewer` | tidak | membaca saja — tidak berkomentar, tidak mencatat waktu |

Satu owner per instalasi, ditegakkan indeks unik parsial — aturan yang sudah
ada, dipindahkan dari `is_owner` ke `role`.

`users.is_owner` **dihapus**. Dua kolom yang menjawab "siapa owner" adalah dua
kolom yang suatu saat akan menjawabnya berbeda.

### Keanggotaan hanya menentukan jangkauan

`project_members` dan `folder_members` tetap ada dan tetap menjadi daftar siapa
ikut apa. **Kolom `role`-nya dihapus.** Keanggotaan sekarang biner: seseorang
anggota, atau bukan.

Sehingga setiap keputusan izin punya dua bagian yang terpisah bersih:

> **Jangkauan** — apakah pemanggil anggota folder atau project ini?
> **Hak** — apakah peran akunnya boleh melakukan aksi ini?

Keduanya harus terpenuhi. Owner melewati bagian pertama.

### Keanggotaan folder tetap diwariskan

Bagian [ADR-0011](0011-folder-dan-pewarisan-keanggotaan.md) yang menjadikan
folder wadah, dan yang mewariskan keanggotaannya ke project di dalamnya, tetap
berlaku: anggota sebuah folder adalah anggota setiap project di dalamnya.

Yang **dibatalkan** adalah aturan peran efektifnya — "yang tertinggi antara
peran folder dan peran project". Tidak ada lagi dua peran untuk dibandingkan.
Yang diwariskan sekarang keanggotaan, bukan peran, dan penggabungan yang dulu
memerlukan penjelasan panjang menjadi satu kata: anggota.

### Owner di dalam authz

Owner tidak perlu keanggotaan. Jalur pintasnya hidup di **satu tempat** — tempat
keanggotaan diperiksa — bukan disebar sebagai pengecualian di setiap aturan.
Pengecualian yang tersebar adalah pengecualian yang suatu saat terlewat di satu
tempat, dan di lapisan ini satu kelewatan berarti seseorang membaca yang bukan
haknya.

### Jejak

Akses owner ke folder atau project yang **bukan keanggotaannya** dicatat di
`activity_events` dengan penanda khusus, dan terlihat di audit trail project
itu. Owner tetap bisa segalanya; yang berubah hanya bahwa aksesnya meninggalkan
jejak.

Itu yang membedakan keputusan ini dari sekadar "owner bisa apa saja".
Kekuasaan tanpa jejak tidak bisa ditinjau — termasuk oleh owner sendiri, saat
suatu hari ia perlu tahu apa yang pernah ia lihat.

## Yang hilang, dan diterima

**Tidak ada cara mengundang seseorang sebagai pembantu biasa.** Seorang
`maintainer` yang diundang ke project orang lain adalah manajer penuh di
sana: ia bisa mengubah status, menutup sprint, dan mengeluarkan anggota lain —
termasuk yang mengundangnya.

Ini ditanyakan eksplisit dan diterima pemilik pada 2026-08-08. Alasannya bisa
diterima: di satu perusahaan, jabatan seseorang tidak berubah karena ia pindah
project, dan orang yang tidak dipercaya memegang kendali sebuah project
seharusnya tidak berperan `maintainer` sejak awal.

Kalau suatu saat ternyata perlu, jalan kembalinya adalah satu kolom `role` di
`project_members` yang **menurunkan** peran akun untuk project itu — tidak
pernah menaikkannya. Menaikkan berarti keanggotaan bisa memberi hak yang tidak
dipunyai akunnya, dan itu membuat peran akun berhenti berarti apa pun.

## Alternatif yang ditolak

**Peran di keanggotaan, seperti rancangan sebelumnya.** Ditolak pemilik: ia
menduplikasi jabatan pegawai ke setiap baris keanggotaan, dan membuat kata
`contributor` punya dua arti — tingkat pegawai dan peran di dalam project.

**`users.may_create_workspaces` sebagai kemampuan, bukan peran.** Menghindari
tabrakan kata sepenuhnya, tapi tidak cukup untuk membedakan `contributor` dari
`viewer`, yang bedanya bukan soal membuat melainkan soal menulis.

## Konsekuensi

### Yang menjadi lebih mudah

- Satu tempat menjawab "orang ini boleh apa". Tidak ada penggabungan peran,
  tidak ada peran efektif, tidak ada dua sumber kebenaran.
- Menambah pegawai adalah satu langkah: buat akun, tetapkan peran akunnya.
- Mengundang ke project adalah satu baris tanpa keputusan tambahan — sejalan
  dengan keputusan 2026-08-08 bahwa penambahan anggota berlaku langsung tanpa
  persetujuan.
- Kata `contributor` kembali punya satu arti.

### Yang menjadi lebih sulit

- **Satu akun bisa membaca seluruh isi instalasi.** Kalau akun owner jatuh,
  semuanya jatuh bersamanya. Password owner bukan lagi sekadar password
  pribadi, dan 2FA untuk akun owner naik dari "kalau sempat" menjadi hal yang
  layak dijadwalkan.
- Kerahasiaan antar-rekan berkurang dengan sengaja. Yang tersisa adalah
  akuntabilitas, lewat jejak di atas.
- **Pekerjaan yang sudah masuk harus dibongkar.** `internal/authz` (PR #53,
  #54, #55) dibangun untuk peran keanggotaan dan peran efektif; keduanya tidak
  ada lagi. Kolom `role` di `project_members` (`00002`), `folder_members`
  (`00008`), dan `invitations` (`00001`) ikut berubah.

### Yang perlu diawasi

- **Berapa sering owner mengakses di luar keanggotaannya.** Kalau sering, itu
  tanda ia sebenarnya perlu menjadi anggota.
- Jumlah akun `maintainer`. Setiap satu adalah orang yang bisa membuat
  ruang kerja yang tidak terlihat siapa pun sampai ia mengundang orang — dan
  yang menjadi manajer penuh di setiap project yang mengundangnya.
