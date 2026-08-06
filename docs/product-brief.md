# Product Brief — Project Management Tool

**Status:** Disetujui pemilik, 2026-08-06
**Nama produk:** belum ditetapkan (dirujuk sebagai "aplikasi" di seluruh dokumen)

## Masalah

Pekerjaan pribadi dan pekerjaan bersama beberapa rekan tersebar di
beberapa tempat — catatan, chat, dan ingatan. Akibatnya:

- Tidak ada satu tempat yang menjawab "apa yang sedang saya kerjakan sekarang"
  dan "apa yang jatuh tempo minggu ini" lintas semua pekerjaan.
- Perkembangan pekerjaan tidak terekam, jadi tidak bisa ditinjau ulang: kapan
  sesuatu berubah, oleh siapa, dan kenapa.
- Alat yang ada tidak pas: Trello terlalu dangkal untuk perencanaan sprint dan
  ketergantungan antar-pekerjaan; Jira terlalu berat, mahal per pengguna, dan
  konfigurasinya menyita waktu sendiri.
- Data pekerjaan berada di server pihak lain, tanpa kontrol atas ekspor,
  retensi, maupun integrasi.

## Pengguna

| Pengguna | Jumlah | Frekuensi | Kebutuhan utama |
|---|---|---|---|
| Pengguna harian — pemilik **dan** rekan yang diundang | 3–11 | Harian, sepanjang hari | Semua fitur. Kecepatan input dan navigasi lebih penting dari kelengkapan tampilan |
| Pengamat (share link) | Sesekali | Jarang | Melihat status tanpa akun |

**Pemilik dan rekan adalah satu golongan pengguna yang sama**, dengan frekuensi
dan kebutuhan yang sama. Perbedaan di antara mereka hanya soal kewenangan
(lihat [authorization.md](authorization.md)), bukan soal cara memakai.

Ini menetapkan satu hal yang mengikat seluruh rancangan: **tidak ada fitur yang
boleh dirancang sebagai "mode ringan untuk rekan".** Setiap layar, pintasan,
dan pengoptimalan performa berlaku untuk semua pengguna harian. Kalau sebuah
alur hanya nyaman untuk pemilik, alur itu belum selesai.

Sistem dirancang untuk skala puluhan pengguna, bukan ribuan. Keputusan
arsitektur di [architecture.md](architecture.md) mengambil keuntungan dari
batas ini secara sengaja.

## Ruang lingkup

Cakupan penuh ada di [feature-catalog.md](feature-catalog.md); urutan
pengerjaannya di [roadmap.md](roadmap.md). Ringkasnya, aplikasi ini menyediakan:

- **Kanban** — board, kolom, kartu, drag & drop, label, prioritas, due date
- **Kedalaman kartu** — deskripsi Markdown, checklist, relasi antar-kartu,
  komentar, riwayat perubahan
- **Perencanaan agile** — backlog, sprint, epic, story point, burndown,
  velocity, cumulative flow
- **Beberapa cara melihat** — kanban, tabel, kalender, timeline/Gantt, filter
  tersimpan, "My Tasks" lintas proyek
- **Pencatatan waktu** — timer, log manual, laporan per proyek dan label
- **Otomatisasi** — WIP limit, kartu berulang, template, aturan otomatis,
  workflow dengan transisi yang dibatasi
- **Kolaborasi** — beberapa pengguna, peran per proyek, mention, notifikasi
  in-app, sinkronisasi realtime
- **Terhubung ke luar** — notifikasi Telegram, integrasi GitHub dan GitLab,
  API publik bertoken, share link read-only, impor/ekspor, PWA offline

## Non-goals

Bagian ini yang mencegah pelebaran cakupan tiga bulan lagi. Semua di bawah ini
**sengaja tidak dikerjakan**, beserta alasannya.

| Tidak dikerjakan | Alasan |
|---|---|
| **Attachment / unggah berkas** (B6) | Menambah storage, verifikasi tipe berkas, kuota, dan penyajian ulang yang aman. Tautan ke penyimpanan yang sudah Anda pakai sudah cukup untuk v1 |
| **Cover image kartu** (B7) | Bergantung pada attachment |
| **Custom field per board** (B11) | Skema dinamis menyeret custom field ke filter, tampilan tabel, ekspor, dan API sekaligus. Biaya terbesar di seluruh katalog dengan manfaat paling tidak pasti untuk tim sekecil ini |
| **Multi-tenancy komersial** | Ini tool pribadi. Satu instalasi, satu workspace. Isolasi antar-tenant tidak dirancang dan **tidak boleh diklaim ada** |
| **Billing, paket berlangganan, onboarding publik** | Tidak dijual |
| **Pendaftaran mandiri (self-service signup)** | Pengguna dibuat lewat undangan oleh pemilik. Menutup seluruh kelas serangan pendaftaran massal |
| **Editing kolaboratif Figma-like (CRDT, live cursor)** | Realtime yang dipilih hanya sinkronisasi perubahan (G5). Resolusi konflik tingkat karakter tidak sepadan untuk alat manajemen proyek |
| **Aplikasi mobile native** | PWA (H9) dianggap cukup. Menghindari dua toko aplikasi, dua siklus rilis, dan kewajiban mendukung versi lama |
| **Laporan yang bisa dirancang sendiri** | Chart yang tersedia sudah ditetapkan (burndown, velocity, CFD, dashboard). Pembuat laporan generik adalah produk tersendiri |
| **SSO / SAML / LDAP** | Tidak ada organisasi yang memintanya |
| **Terjemahan antarmuka (i18n)** | Satu bahasa dulu. Struktur kode tidak akan menghalangi penambahannya nanti |

## Ukuran keberhasilan

| Ukuran | Target | Kapan diukur |
|---|---|---|
| Aplikasi dipakai untuk pekerjaan nyata, bukan hanya diuji | Pemilik memakainya ≥ 5 hari per minggu selama 4 minggu berturut-turut | 1 bulan setelah Fase 1 |
| Alat lama ditinggalkan | Tidak ada lagi pekerjaan yang dilacak di alat sebelumnya | 1 bulan setelah Fase 1 |
| Rekan memakainya sesering pemilik | Setiap rekan yang diundang memakainya ≥ 5 hari per minggu, tanpa diingatkan | 1 bulan setelah Fase 2 |
| Tidak memperlambat | Buka board dan pindahkan kartu terasa seketika — lihat target angka di [nfr.md](nfr.md) | Setiap fase |
| Tidak kehilangan data | Nol insiden kehilangan data. Restore dari backup pernah diuji | Setiap kuartal |

**Kegagalan yang paling mungkin, dan tandanya:** aplikasi selesai di 60% lalu
ditinggalkan karena belum cukup berguna untuk dipakai sehari-hari. Penangkalnya
ada di struktur roadmap — Fase 1 sengaja disusun agar sudah layak pakai, dan
setiap fase sesudahnya menambah nilai tanpa membongkar yang sebelumnya. Kalau
setelah Fase 1 Anda tidak memakainya setiap hari, itu sinyal untuk berhenti dan
memperbaiki, bukan untuk lanjut ke Fase 2.

## Batasan

| Batasan | Nilai |
|---|---|
| Tim | Satu orang |
| Perkiraan durasi cakupan penuh | 6–12 bulan paruh waktu. Fase 1 layak pakai di 4–6 minggu |
| Anggaran infrastruktur | Satu VPS. Target di bawah $15/bulan — lihat [environments.md](environments.md) |
| Tenggat | Tidak ada tenggat eksternal. Ini kekuatan: cakupan boleh dipotong, kualitas tidak |
| Kepatuhan | Tidak ada kewajiban formal. Tetap berlaku: data pribadi rekan (email, nama, isi komentar) tunduk pada `rules/45-privacy.md` |
| Stack | React + Go + PostgreSQL + Redis — lihat [ADR-0001](adr/0001-stack-react-go-postgresql.md) |

## Prinsip yang dipakai saat ragu

Dipakai untuk memutuskan hal kecil tanpa harus bertanya setiap kali:

1. **Kecepatan pengguna harian menang atas kelengkapan** — pemilik maupun
   rekan, tanpa dibedakan. Kalau sebuah fitur membuat alur harian jadi dua klik
   lebih panjang, fitur itu salah rancang.
2. **Data milik pemilik.** Ekspor penuh (H6) selalu berfungsi, di setiap fase.
   Tidak ada data yang hanya bisa keluar lewat antarmuka.
3. **Tidak ada konfigurasi yang tidak diminta.** Setiap opsi yang bisa diatur
   adalah keputusan yang ditunda dan harus dirawat selamanya.
4. **Rusak dengan berisik, bukan diam-diam.** Job yang gagal, webhook yang
   ditolak, dan aturan otomatis yang error harus terlihat di antarmuka —
   bukan hanya di log.
