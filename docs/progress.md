# Progres — Project Management Tool

**Terakhir direkonsiliasi:** 2026-08-07 terhadap `main` di commit `b67d79c`.

Sumber kebenaran progres adalah keadaan repo, bukan berkas ini. Sebuah item
disebut `selesai` hanya kalau kodenya ada, gerbangnya hijau, **dan PR-nya
sudah ter-merge ke `main`** — bukan kalau seseorang menuliskannya begitu.

Item diturunkan dari [roadmap.md](roadmap.md) dan dari rencana Fase 0 yang
disetujui pada 2026-08-06. Setiap item menaut kembali ke sumbernya.

## Ringkasan

**33 selesai · 0 sedang · 11 belum**

Fase 0 punya **31 sub-langkah**: Langkah 9, 11, dan 15 dipecah jadi beberapa PR
atas permintaan pemilik. Dari 31 itu, **20 selesai dan 11 belum**. Ditambah 10
item gerbang dokumen dan 3 item perubahan cakupan, semuanya selesai.

**Skema dan fondasi backend selesai.** Tujuh migration, 33 tabel, 5 view
`*_live`, `sqlc` terpasang dengan gerbang CI-nya, `internal/httpx` lengkap —
request ID, log terstruktur, recovery, bentuk error, rate limit — dan
`internal/fracdex` sebagai penghasil urutan kartu (ADR-0003).

Langkah 15 selesai: aplikasi sekarang bisa memasukkan orang, mengenalinya di
permintaan berikutnya, dan mengeluarkannya. Yang tersisa dimulai dari Langkah
16 (CSRF), lalu otorisasi dan seluruh frontend.

> **Angka ringkasan pernah dua kali salah, dan keduanya dicatat di sini.**
>
> Rekonsiliasi pertama menemukan `12 · 4 · 21` salah hitung — seharusnya 15
> selesai, bukan 12. Rekonsiliasi kedua menemukan `20 · 0 · 19` juga sudah
> basi: pemecahan Langkah 8–11 menambah tiga baris item, tapi totalnya tidak
> ikut dihitung ulang.
>
> Rekonsiliasi keempat dihitung ulang secara mekanis, bukan dengan mata:
> 19 baris `selesai` di tabel Fase 0 ditambah 10 baris gerbang dokumen = 29,
> melawan 13 yang belum. Paragraf di atasnya ternyata sudah basi lagi — ia
> masih menulis `15 selesai dan 14 belum` untuk 29 sub-langkah.
>
> **Angka ringkasan tetap tidak layak dipercaya tanpa menghitung ulang
> tabelnya.** Tabelnya yang benar; ringkasan hanya kenyamanan.
>
> Satu jebakan untuk siapa pun yang menghitungnya dengan skrip: baris 23 memuat
> pipe ter-escape (`--size=small\|full`), yang memecah kolomnya kalau barisnya
> dibelah dengan `|` begitu saja.
>
> Rekonsiliasi kelima (2026-08-07, `81bd24c`) dihitung dengan skrip yang
> menangani jebakan itu: 34 baris di tabel Fase 0 — 21 `selesai`, 13 `belum` —
> ditambah 10 gerbang dokumen, semuanya `selesai`. Jadi 31 · 0 · 13.
>
> Rekonsiliasi keenam (2026-08-07, `f63728e`) dihitung dengan skrip yang sama:
> 34 baris, 22 `selesai`, 12 `belum`. Jadi 32 · 0 · 12.
>
> Rekonsiliasi ketujuh (2026-08-07, `b67d79c`): 34 baris, 23 `selesai`,
> 11 `belum`. Jadi 33 · 0 · 11.

## Gerbang dokumen (sebelum baris kode pertama)

| # | Item | Status | Sumber | Bukti |
|---|---|---|---|---|
| D1 | Product brief dengan non-goals | selesai | `system-design-docs` | PR #2 |
| D2 | Glosarium domain | selesai | `system-design-docs` | PR #2 |
| D3 | Arsitektur C4 level 1–2 | selesai | `system-design-docs` | PR #2, direvisi PR #3 |
| D4 | ADR untuk keputusan sulit dibalik | selesai | `system-design-docs` | PR #1 (0001–0006), PR #3 (0007–0008) |
| D5 | Model data + DDL + indeks + retensi | selesai | `system-design-docs` | PR #2, direvisi PR #3 |
| D6 | Kontrak API OpenAPI (Fase 0–1) | selesai | `system-design-docs` | PR #2 |
| D7 | Matriks otorisasi | selesai | `system-design-docs` | PR #2 |
| D8 | NFR dengan angka dan persentil | selesai | `system-design-docs` | PR #2 |
| D9 | Threat model STRIDE | selesai | `system-design-docs` | PR #2 |
| D10 | Environments & konfigurasi | selesai | `system-design-docs` | PR #2 |

Gerbang dinyatakan lewat pada 2026-08-06 (PR #2).

## Fase 0 — Fondasi

| # | Langkah | Status | Sumber | Bukti |
|---|---|---|---|---|
| 1 | Kerangka monorepo & manifest | selesai | Rencana Fase 0 §1 | PR #4 (`cafcb18`) |
| 2 | Gerbang lokal: lefthook, gitleaks, golangci-lint | selesai | Rencana Fase 0 §2 | PR #4 |
| 3 | CI GitHub Actions | selesai | Rencana Fase 0 §3 | PR #4 |
| 4 | `internal/config` — muat & validasi env, gagal saat start | selesai | Rencana Fase 0 §4 | PR #5 (`2216f84`) |
| 5 | `cmd/api` — timeout, graceful shutdown, `/healthz` | selesai | Rencana Fase 0 §5 | PR #5 |
| 6 | Compose lokal: PostgreSQL, Redis, Mailpit | selesai | Rencana Fase 0 §6 | PR #6 (`5be8ccd`) |
| 7 | Pool `pgx` + `/readyz` | selesai | Rencana Fase 0 §7 | PR #6 |
| — | Penyaringan CI per-path + `go-version-file` | selesai | Perubahan cakupan, lihat bawah | PR #6 |
| — | Catatan batas platform GitHub | selesai | Perubahan cakupan, lihat bawah | PR #7 (`216672e`) |
| — | `progress.md` + log sesi | selesai | Skill `progress-tracking` | PR #8 (`ac65098`); log sesi dilepas dari repo pada 2026-08-07 |
| 8 | goose + `cmd/migrate` + migration `00001` identitas | selesai | Rencana Fase 0 §8 | PR B/#10 (`c9a5ea1`) |
| 9a | Migration `00002` project, member, status, board, column, label | selesai | Rencana Fase 0 §9 | PR B/#11 (`f438d97`) |
| 9b | Migration `00003` sprints, cards, FK komposit | selesai | Rencana Fase 0 §9 | PR C/#13 (`9fc7bb8`) |
| 10 | Migration `00004` `activity_events` berpartisi + `outbox` | selesai | Rencana Fase 0 §10 | PR D/#14 (`e22adf7`) |
| 11a | Migration `00005` isi kartu: comments, checklists, links, card_labels | selesai | Rencana Fase 0 §11 | PR E/#15 (`1572586`) |
| 11b | Migration `00006` notifikasi, waktu, filter, automation | selesai | Rencana Fase 0 §11 | PR F/#16 (`ba87e12`) |
| 11c | Migration `00007` token, share link, VCS | selesai | Rencana Fase 0 §11 | PR G/#17 (`ee404a7`) |
| 12 | `sqlc` + view `*_live` untuk soft delete | selesai | Rencana Fase 0 §12 | PR #19 |
| 13 | `internal/httpx` — request ID, log, recovery, bentuk error, rate limit | selesai | Rencana Fase 0 §13 | PR #20, #22, #23, #24 |
| 14 | `internal/fracdex` + property test | selesai | Rencana Fase 0 §14, ADR-0003 | PR #26, #27, #28 (`90207d6`) |
| 15a | `identity/domain` — password Argon2id | selesai | Rencana Fase 0 §15, ADR-0005 | PR #29 (`81bd24c`) |
| 15b | `identity` — terbitkan, baca, dan cabut sesi | selesai | Rencana Fase 0 §15, ADR-0005 | PR #31, #32, #33, #34 (`f63728e`) |
| 15c | `identity` — `/login`, `/logout`, `/me` | selesai | Rencana Fase 0 §15, ADR-0005 | PR #38, #39, #40, #41 (`b67d79c`) |
| 16 | Middleware CSRF double-submit | belum | Rencana Fase 0 §16, ADR-0005 | — |
| 17 | `internal/authz` + table-driven test empat pola | belum | Rencana Fase 0 §17, [authorization.md](authorization.md) | — |
| 18 | Undangan, reset password, daftar & cabut sesi + OpenAPI | belum | Rencana Fase 0 §18 | — |
| 19 | **Gerbang manusia:** arah desain tertulis | belum | Rencana Fase 0 §19, `rules/15-ui-design.md` §2 | — |
| 20 | Scaffold Vite + shadcn + token | belum | Rencana Fase 0 §20 | — |
| 21 | Generate tipe dari OpenAPI + layar login empat state | belum | Rencana Fase 0 §21 | — |
| 22 | Embed SPA ke biner, satu origin | belum | Rencana Fase 0 §22, ADR-0001 | — |
| 23 | `cmd/seed --size=small\|full` | belum | Rencana Fase 0 §23 | — |
| 24 | `cmd/worker` minimal — pangkas sesi kedaluwarsa | belum | Rencana Fase 0 §24 | — |
| 25 | Dockerfile, `compose.prod.yml`, Caddy, workflow deploy | belum | Rencana Fase 0 §25 | — |
| 26 | Backup + uji restore sungguhan | belum | Rencana Fase 0 §26 | — |

## Penghalang

**Tidak ada.**

Penghalang sebelumnya — GitHub Actions *degraded availability* pada 2026-08-06
yang menggagalkan tiga run di langkah `Set up job` — sudah pulih. PR #6, #7,
dan #8 ter-merge pada 2026-08-07 dengan CI hijau, termasuk `go test -race`
yang tidak bisa dijalankan di mesin pengembangan karena detektor balapan
menuntut cgo.

## Keputusan yang menunggu jawaban pemilik

| Pertanyaan | Sejak | Memblokir |
|---|---|---|
| Peran default untuk rekan yang diundang: `admin` (saran saya) atau pindahkan sebagian aksi dari `admin` ke `member`? | 2026-08-06 | Fase 2, bukan Fase 0 |
| Pasang branch protection di `main`? Saat ini **tidak ada sama sekali** — lihat catatan di bawah | 2026-08-07 | Tidak memblokir |


Pertanyaan branch protection sudah terjawab sebagiannya, dan jawabannya tidak
menyenangkan: **`main` tidak punya proteksi apa pun.** Ini ditemukan bukan lewat
pemeriksaan pengaturan, melainkan lewat akibatnya — `gh pr merge --auto` pada
PR #13 langsung mengeksekusi merge, karena auto-merge hanya menunggu *required
check* dan tidak ada satu pun yang wajib. PR #13 karena itu ter-merge sebelum
CI-nya selesai. CI-nya kemudian hijau, tapi itu keberuntungan, bukan gerbang.

PR #14 sampai #17 menunggu CI selesai lebih dulu, baru di-merge.

Yang perlu diputuskan pemilik: apakah `main` diberi *required status check*
untuk tiga job CI. Kalau ya, `--auto` menjadi aman dan tidak ada PR yang bisa
masuk tanpa CI hijau.

Pertanyaan ukuran PR sudah terjawab pada 2026-08-07: Langkah 8–11 dipecah jadi
tujuh PR, masing-masing di bawah 250 baris yang ditulis tangan.

Tiga pertanyaan terjawab pada 2026-08-07 dan menjadi dua ADR:

| Pertanyaan | Jawaban | Tercatat di |
|---|---|---|
| Panjang password minimum dan kebijakannya | 12 karakter, maksimum jauh di atas 64, tanpa aturan komposisi, tanpa rotasi berkala, NFKC sebelum hashing, blocklist HIBP dengan k-anonymity | [ADR-0009](adr/0009-kebijakan-password.md) |
| Angka rate limit | Ember berlapis dengan backoff, bukan penguncian keras. Tabel angkanya di ADR | [ADR-0010](adr/0010-ip-klien-dan-rate-limit.md) |
| Cara menentukan IP klien di belakang Caddy | Dihitung dari kanan melewati proxy tepercaya yang jumlahnya tetap; IPv6 diagregasi ke /64; IP tidak pernah jadi kunci tunggal karena CGNAT | [ADR-0010](adr/0010-ip-klien-dan-rate-limit.md) |

Keduanya membawa pekerjaan yang sebelumnya tidak ada di rencana, dan itu
dicatat di tabel perubahan cakupan di bawah.

## Di luar cakupan (non-goals)

Ditulis di sini supaya tidak diam-diam dikerjakan. Alasan lengkap ada di
[product-brief.md](product-brief.md#non-goals).

B6 attachment · B7 cover image · B11 custom field · multi-tenancy komersial ·
billing · pendaftaran mandiri · editing kolaboratif CRDT · aplikasi mobile
native · pembuat laporan generik · SSO/SAML/LDAP · i18n

## Perubahan cakupan

| Tanggal | Perubahan | Alasan | Disetujui |
|---|---|---|---|
| 2026-08-06 | PostgreSQL 17 → 18 | Repo masih kosong; satu-satunya saat pindah versi mayor berbiaya nol. Lihat [ADR-0007](adr/0007-postgresql-18.md) | ya |
| 2026-08-06 | Struktur backend jadi per modul | Permintaan pemilik. Batas lapisan ditegakkan kompilator, bukan disiplin. Lihat [ADR-0008](adr/0008-struktur-modular-backend.md) | ya |
| 2026-08-06 | Frontend memakai pnpm dan shadcn/ui | Permintaan pemilik | ya |
| 2026-08-06 | `/readyz` **tidak** memeriksa Redis, hanya PostgreSQL | Rencana menulis "keduanya", tapi [nfr.md](nfr.md) menyatakan aplikasi tetap jalan saat Redis mati. Melaporkannya sebagai *not ready* akan menarik instance yang masih bekerja dari rotasi | ya — lewat merge PR #6, yang deskripsinya menjelaskan penyimpangan ini |
| 2026-08-06 | Klien Go untuk Redis ditunda ke Langkah 13 | Konsekuensi baris di atas — tidak ada kode yang membutuhkannya di Langkah 7 | ya — lewat merge PR #6 |
| 2026-08-06 | Penyaringan CI dipindah ke level workflow; `GO_VERSION` diganti `go-version-file` | Job kontrak API menyalakan runner di setiap PR hanya untuk menemukan spec tidak berubah | ya |
| 2026-08-06 | Catatan batas platform GitHub ditambahkan ke `environments.md` | Anjuran sebelumnya keliru: secret scanning tidak gratis untuk repo privat | ya |
| 2026-08-07 | Langkah 8–11 dipecah jadi tujuh PR (A–G), masing-masing di bawah 250 baris | Permintaan pemilik. Dua PR sebelumnya masing-masing ~790 baris, jauh di atas target 400 di `rules/50-git-workflow.md` | ya |
| 2026-08-07 | `cards.status_id` hanya punya FK komposit, bukan komposit **dan** kolom-tunggal seperti di [data-model.md](data-model.md) | FK kolom-tunggal tidak menegakkan apa pun yang belum ditegakkan yang komposit, termasuk menolak penghapusan status yang masih dipegang kartu. Ia hanya menambah satu pemeriksaan di setiap insert | ya — lewat merge PR #13 |
| 2026-08-07 | FK ke induk yang ikut terhapus memakai `NO ACTION`, bukan `RESTRICT` | Diuji langsung: `RESTRICT` lolos hari ini hanya karena `cards` kebetulan dihapus sebelum `statuses`. Urutan itu tidak dijanjikan | ya — lewat merge PR #13 |
| 2026-08-07 | `activity_events` punya partisi `DEFAULT`; batas partisi dikunci ke UTC | Event ditulis dalam transaksi yang sama dengan perubahan yang memicunya, jadi event yang tidak bisa dirutekan menggagalkan permintaan pengguna. Batas UTC supaya nama partisi dan rentangnya tidak bergeser mengikuti `TimeZone` server | ya — lewat merge PR #14 |
| 2026-08-07 | Urutan `checklists` dan `checklist_items` dijadikan **unik** per induk | Model data hanya menuliskan indeks biasa. Dua baris yang berbagi posisi mengurutkan diri berbeda antar-pembacaan | ya — lewat merge PR #15 |
| 2026-08-07 | `CHECK` nama zona waktu di `recurring_cards` dibatalkan | PostgreSQL melarang subquery di dalam `CHECK`. Validasi zona dan parsing RFC 5545 didaftar sebagai invarian milik service | ya — lewat merge PR #16 |
| 2026-08-07 | Langkah 13 dipecah jadi empat PR (#20, #22, #23, #24) | Ditulis sekaligus jadi 873 baris, di atas dua PR yang ditolak pemilik pada 2026-08-07 | ya |
| 2026-08-07 | `REDIS_URL` jadi variabel **wajib** | Redis yang tidak terjangkau ditangani saat runtime; Redis yang tidak pernah dikonfigurasi adalah deployment yang belum selesai. Deployment lama akan menolak start sampai variabel ini disetel | ya — lewat merge PR #23 |
| 2026-08-07 | Paket `internal/redis` ditambahkan, tidak ada di pohon `architecture.md` | Simetris dengan `internal/postgres`, dan Fase 8 (realtime) serta cache akan memakainya juga. `architecture.md` diperbarui | ya — lewat merge PR #23 |
| 2026-08-07 | Langkah 14 dipecah jadi tiga PR (#26 format & validator, #27 generator, #28 property test) | Ditulis sekaligus jadi 637 baris, di atas target 400 di `rules/50-git-workflow.md` dan di atas dua PR yang sudah ditolak pemilik | ya |
| 2026-08-07 | `fracdex.Between` menyimpang dari paket `fractional-indexing` di satu sudut: saat penyisipan di depan menyentuh dasar ruang integer, paket JS mengembalikan string yang ditolak validatornya sendiri; `Between` membagi pecahan supaya keluarannya selalu sah | Sudut itu butuh 62^26 penyisipan di depan untuk dicapai, jadi keduanya sepakat pada setiap kunci yang akan benar-benar dihitung salah satunya. Alternatifnya adalah menyimpan cacat yang membuat postcondition "keluaran selalu lolos `Validate`" tidak bisa dinyatakan | ya — lewat merge PR #27 |
| 2026-08-07 | Langkah 15 dipecah jadi tiga (15a password, 15b sesi, 15c endpoint), dan 15b sendiri jadi empat PR (domain, query, penerbitan, pembacaan) | Alasan yang sama dengan Langkah 13 dan 14. Versi utuh 15b sudah ditulis dan berukuran 571 baris sebelum dipecah | ya |
| 2026-08-07 | Sesi punya ambang penulisan satu jam sebelum tenggat idle digeser — tidak ada di ADR-0005 | Menggeser di setiap permintaan berarti satu `UPDATE` per permintaan untuk seluruh aplikasi. Biayanya: tenggat bisa sampai satu jam lebih awal daripada yang dijanjikan jendela idle | ya — lewat merge PR #34 |
| 2026-08-07 | `sessions.ip_hash` dibiarkan NULL untuk sekarang | SHA-256 telanjang dari IPv4 hanya empat miliar preimage, jadi butuh digest berkunci dan secret-nya; sementara cara menentukan IP klien di belakang Caddy masih pertanyaan terbuka | ya |
| 2026-08-07 | Rate limiter diganti dari fixed window ke sliding window, dan penghitungnya jadi berlapis (akun, IP, prefiks IP) | [ADR-0010](adr/0010-ip-klien-dan-rate-limit.md). Fixed window bisa dilewati dua kali limit di perbatasan jendela. Pekerjaan tambahan di Langkah 18 yang sebelumnya tidak ada di rencana | ya |
| 2026-08-07 | Blocklist password bocor lewat HIBP dengan k-anonymity ditambahkan ke jalur pendaftaran dan penggantian password | [ADR-0009](adr/0009-kebijakan-password.md). Dependensi eksternal baru di jalur permintaan pengguna; wajib bertimeout dan **gagal terbuka** | ya |
| 2026-08-07 | Repo dijadikan publik | Branch protection, ruleset, *secret scanning*, dan *push protection* semuanya digerbang plan: gratis hanya untuk repo publik. Push protection adalah kontrol yang sebelumnya tidak ada — ia menolak secret sebelum commit-nya mendarat, sedangkan `gitleaks` di CI baru berteriak setelah ter-push | ya — keputusan pemilik pada 2026-08-07 |
| 2026-08-07 | Log sesi (`docs/sessions/`) dilepas dari repo dan masuk `.gitignore` | Permintaan pemilik, menyusul repo jadi publik. Isinya catatan proses, bukan dokumentasi proyek. Konsekuensinya: klon baru tidak membawa riwayat sesi, jadi `progress.md` menjadi satu-satunya pembawa konteks antar-sesi yang ikut repo | ya |
| 2026-08-07 | `oasdiff` dipin ke v1.28.0 dan dipasang dengan `GOTOOLCHAIN=auto` | Gerbang kontrak berhenti bisa memasang alatnya: `@latest` menuntut Go 1.26. Sekaligus menghapus `@latest` — gerbang yang alatnya tidak dipin bisa berubah perilakunya tanpa satu pun commit di repo ini | ya — ditanyakan dan disetujui pemilik pada 2026-08-07 |
| 2026-08-07 | `password.minLength` di kontrak turun dari 8 ke 1, dengan `maxLength` 1024 | Kebijakan panjang ditegakkan saat password **dibuat**, bukan saat dipakai. Endpoint login yang menolak password pendek baru saja memberi tahu pemanggilnya apa aturannya. `oasdiff` tidak menganggapnya merusak | ya — lewat merge PR #39 |
| 2026-08-07 | Normalisasi NFKC sebelum hashing dan sebelum verifikasi password | [ADR-0009](adr/0009-kebijakan-password.md). Tanpa itu password yang sama dari papan ketik berbeda gagal login. Konsekuensinya: normalisasi tidak bisa diubah lagi setelah ada hash tersimpan | ya |
| 2026-08-07 | `golang.org/x/crypto` jadi dependency runtime langsung | Argon2id diwajibkan ADR-0005 dan `rules/40-security.md`, dan pustaka standar Go tidak punya implementasinya. Turunannya `golang.org/x/sys` sudah ada di `go.sum`. `govulncheck` bersih | ya — ditanyakan dan disetujui pemilik pada 2026-08-07 |

## Cara memperbarui berkas ini

Rekonsiliasi dilakukan di akhir setiap fase, atau minimal mingguan:

1. Daftar PR yang ter-merge sejak `Terakhir direkonsiliasi`
2. Untuk setiap `selesai`: benarkah kodenya ada dan gerbangnya hijau?
3. Untuk setiap `sedang` yang tidak bergerak: tulis penghalangnya
4. Cari pekerjaan di repo yang tidak ada di daftar — itu pelebaran cakupan
5. Perbarui tanggal dan commit rekonsiliasi

Item yang ternyata belum benar-benar selesai **dikembalikan statusnya**.
