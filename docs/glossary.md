# Glosarium Domain

Satu konsep, satu nama, di semua tempat: tabel PostgreSQL, kode Go, kontrak
API, tipe TypeScript, dan antarmuka. Tanpa ini, satu benda akan punya tiga nama
dalam enam bulan.

## Aturan penamaan

| Lapisan | Bentuk | Contoh |
|---|---|---|
| Tabel & kolom PostgreSQL | `snake_case`, tabel jamak | `card_links`, `status_id` |
| Struct & field Go | `PascalCase` / `camelCase` | `CardLink`, `statusID` |
| Field JSON di API | `snake_case` | `"status_id"` |
| Tipe TypeScript | Digenerate dari OpenAPI, **tidak ditulis manual** | `components["schemas"]["Card"]` |
| Konversi `snake_case` → `camelCase` | Dilakukan di **satu** lapisan klien saja, bukan tersebar | `lib/api/transform.ts` |

Bahasa kode, nama variabel, dan komentar: Inggris. Antarmuka pengguna dan
dokumen: Indonesia.

## Istilah inti

| Istilah | Arti | Yang **bukan** artinya |
|---|---|---|
| **Project** | **Wadah teratas.** Memiliki kartu, status, workflow, sprint, epic, label, dan anggota. Punya `key` pendek (`PM`) yang membentuk nomor kartu (`PM-142`) | Bukan sama dengan Board. Satu project bisa punya beberapa board. Tidak ada wadah di atasnya — istilah "workspace" **tidak dipakai**, karena tabel berisi satu baris selamanya adalah antisipasi yang dilarang `rules/00-core.md` |
| **Owner** | Pemilik instalasi. Satu orang. Salah satu nilai `users.role` | Bukan sekadar akun dengan hak lebih. Owner melihat dan melakukan apa pun di **setiap** folder dan project tanpa perlu jadi anggota — dan setiap aksesnya di luar keanggotaan tercatat ([ADR-0012](adr/0012-peran-akun-dan-akses-owner.md)) |
| **Peran akun** | Nilai `users.role`: `owner`, `project_manager`, `member`, atau `viewer`. Satu-satunya sumber hak seseorang | Bukan peran per project. Seseorang membawa peran yang sama ke setiap folder dan project yang mengundangnya |
| **Keanggotaan** | Baris di `project_members` atau `folder_members`: siapa ikut apa | Tidak membawa peran. Ia hanya menentukan **jangkauan** — folder dan project mana yang terlihat oleh seseorang |
| **Board** | Sebuah **cara melihat** kartu milik satu project, tersusun dalam kolom | Board tidak memiliki kartu. Menghapus board tidak menghapus kartu |
| **Column** | Lajur vertikal di sebuah board. Setiap kolom memetakan tepat satu Status | Bukan "List" (istilah Trello). Kolom bukan tempat penyimpanan, hanya tampilan |
| **Status** | Keadaan sebenarnya sebuah kartu, dimiliki project. Contoh: `Todo`, `In Progress`, `In Review`, `Done` | Bukan properti board. Kartu punya status walau tidak ditampilkan di board mana pun |
| **Status Category** | Salah satu dari `todo`, `in_progress`, `done`. Melekat pada setiap Status | Bukan status itu sendiri. Kategori yang dipakai chart dan WIP limit, bukan nama status |
| **Card** | Satu unit pekerjaan. **Satu-satunya nama untuk konsep ini** | Bukan "issue", "task", "ticket", "item", atau "story". Semua istilah itu dilarang di kode, DB, dan API |
| **Card Type** | Salah satu dari `epic`, `story`, `task`, `bug`, `subtask`. Kolom di `cards` | Bukan tabel konfigurasi. Tipe kartu tetap, tidak bisa ditambah pengguna |
| **Epic** | Kartu bertipe `epic` yang menaungi kartu lain lewat `cards.epic_id` | Bukan tabel tersendiri |
| **Subtask** | Kartu bertipe `subtask` dengan `parent_card_id` terisi | Bukan Checklist Item. Subtask punya status, assignee, dan estimasi sendiri |
| **Checklist Item** | Baris centang di dalam sebuah kartu | Bukan kartu. Tidak punya status maupun nomor |
| **Sprint** | Iterasi berbatas waktu milik satu project. Keadaan: `planned`, `active`, `completed` | Bukan Board. Sprint aktif ditampilkan di board, tapi keduanya konsep terpisah |
| **Backlog** | Kartu milik project yang `sprint_id`-nya kosong dan belum diarsipkan | Bukan sebuah status. Kartu backlog tetap punya status |
| **Label** | Penanda berwarna milik project, bisa dipasang banyak di satu kartu | Bukan kategori atau tipe |
| **Card Link** | Hubungan berarah antar dua kartu: `blocks`, `relates_to`, `duplicates` | Bukan hubungan induk-anak. Itu `parent_card_id` dan `epic_id` |
| **Change Request** | Nama netral untuk pull request (GitHub) dan merge request (GitLab). Dipakai di DB, kode, dan API | Antarmuka tetap menampilkan istilah asli penyedia. Lihat [ADR-0006](adr/0006-abstraksi-vcs-provider.md) |
| **Position** | Urutan kartu di dalam satu status, disimpan sebagai *fractional index* bertipe `text` | Bukan bilangan bulat. Lihat [ADR-0003](adr/0003-urutan-kartu-fractional-index.md) |
| **Activity Event** | Catatan tak-bisa-diubah bahwa sesuatu terjadi: siapa, apa, kapan, pada entitas mana | Bukan notifikasi. Satu event bisa memunculkan nol atau banyak notifikasi |
| **Notification** | Sebuah Activity Event yang ditujukan ke seorang pengguna, punya status dibaca | Bukan event. Menghapus notifikasi tidak menghapus riwayat |
| **Workflow Transition** | Perpindahan status yang diizinkan: dari status A ke status B pada satu project | Bukan aturan otomatis. Transisi membatasi; automation bertindak |
| **Automation Rule** | Pemicu → syarat → aksi, dijalankan sebagai reaksi atas Activity Event | Bukan workflow |
| **Time Log** | Rentang waktu yang tercatat pada sebuah kartu oleh seorang pengguna | Bukan estimasi. Estimasi adalah `estimate_points` di kartu |
| **Saved Filter** | Kombinasi kriteria pencarian yang disimpan dan diberi nama | Bukan board dan bukan tampilan. Filter bisa dipakai di tampilan mana pun |
| **Share Link** | URL bertoken yang memberi akses baca-saja ke sebuah board tanpa akun | Bukan pengguna. Tidak punya identitas, tidak bisa berkomentar |
| **API Token** | Kredensial panjang-umur milik seorang pengguna untuk akses program | Bukan sesi. Tidak pernah dipakai browser |
| **VCS Connection** | Sambungan sebuah project ke satu repositori GitHub atau GitLab | Bukan integrasi per kartu |
| **VCS Link** | Kaitan antara satu kartu dan satu objek di repositori (issue, PR/MR, branch, commit) | — |

## Istilah yang dilarang

Ditulis di sini supaya penolakannya bisa dirujuk saat review.

| Dilarang | Pakai ini | Alasan |
|---|---|---|
| `issue`, `ticket`, `task`, `item`, `story` sebagai nama entitas | `Card` | Lima nama untuk satu benda. `task` juga bentrok dengan Card Type |
| `list` untuk kolom board | `Column` | `list` bentrok dengan tampilan daftar (C2) dan dengan tipe data |
| `state` untuk status kartu | `Status` | `state` dipakai untuk keadaan Sprint dan Automation Run |
| `tag` | `Label` | Satu nama saja |
| `project_members.role`, `folder_members.role` | `users.role` | **Dibalik pada 2026-08-08.** Sebelumnya peran melekat pada keanggotaan; sekarang pada akun ([ADR-0012](adr/0012-peran-akun-dan-akses-owner.md)). Penggunanya pegawai, dan jabatan pegawai tidak berubah karena ia pindah project |
| `deleted` sebagai boolean | `deleted_at timestamptz` | Kapan dihapus selalu berguna; boolean membuang informasi |
| `utils`, `helpers`, `common`, `shared` sebagai nama paket Go | Nama benda yang sebenarnya | Tanda desain yang belum selesai (`rules/20-go.md`) |

## Istilah operasional

| Istilah | Arti |
|---|---|
| **Outbox** | Tabel tempat event ditulis dalam transaksi yang sama dengan perubahan data, lalu dikirim ke konsumen secara terpisah. Lihat [ADR-0002](adr/0002-event-transport-outbox.md) |
| **Hub** | Komponen Go yang memegang koneksi WebSocket aktif dan menyiarkan event ke klien yang berhak |
| **Rebalance** | Job berkala yang menulis ulang `position` seluruh kartu di satu status ketika fractional index sudah terlalu panjang |
| **Card Key** | Pengenal yang dibaca manusia: `<project.key>-<nomor urut>`, contoh `PM-142`. Tidak pernah dipakai sebagai kunci asing — itu tugas `id` (UUIDv7) |
