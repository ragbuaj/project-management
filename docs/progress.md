# Progres — Project Management Tool

**Terakhir direkonsiliasi:** 2026-08-08 terhadap `main` di commit `0159ab3`,
ditambah #96 yang menunggu CI.

Sumber kebenaran progres adalah keadaan repo, bukan berkas ini. Sebuah item
disebut `selesai` hanya kalau kodenya ada, gerbangnya hijau, **dan PR-nya
sudah ter-merge ke `main`** — bukan kalau seseorang menuliskannya begitu.

Item diturunkan dari [roadmap.md](roadmap.md) dan dari rencana Fase 0 yang
disetujui pada 2026-08-06. Setiap item menaut kembali ke sumbernya.

## Ringkasan

**38 selesai · 1 sedang · 8 belum**

Fase 0 punya **31 sub-langkah** bernomor: Langkah 9, 11, dan 15 dipecah jadi
beberapa PR atas permintaan pemilik. Dari 31 itu, **22 selesai, 1 sedang, dan 8
belum**. Ditambah 10 item gerbang dokumen dan 6 baris perubahan cakupan,
semuanya selesai — jadi 38 · 1 · 8.

**Skema dan fondasi backend selesai.** Tujuh migration, 33 tabel, 5 view
`*_live`, `sqlc` terpasang dengan gerbang CI-nya, `internal/httpx` lengkap —
request ID, log terstruktur, recovery, bentuk error, rate limit — dan
`internal/fracdex` sebagai penghasil urutan kartu (ADR-0003).

Langkah 15 dan 16 selesai: aplikasi bisa memasukkan orang, mengenalinya di
permintaan berikutnya, dan mengeluarkannya — dan sejak Langkah 16 setiap
permintaan yang mengubah data wajib membawa pasangan CSRF. Skema folder sudah masuk
(`00008`), peran akun sudah jadi satu-satunya sumber hak (`00009`, `00010`),
dan Langkah 17 selesai: `internal/authz` memutuskan dari peran akun ditambah
satu pemeriksaan keanggotaan, dengan pengecualian owner hidup di satu tempat.
Yang tersisa dimulai dari Langkah 18 (undangan, reset password, daftar sesi),
lalu seluruh frontend.

**Langkah 18 sudah melewati tujuh dari delapan potongannya.** Potongan a
menentukan alamat klien di belakang proxy; potongan b mengganti fixed window
dengan sliding window berlapis; potongan c memasang tiga ember kegagalan —
akun, alamat, jaringan — di depan `/auth/login`; potongan d memberi setiap akun
daftar sesinya sendiri dan dua cara mengakhirinya; potongan e melahirkan
`internal/mail`; potongan f menutup seluruh alur undangan, dari token sampai
akun yang jadi; potongan g menutup alur reset password, dari permintaan sampai
sandi baru dan seluruh sesi lama yang dicabut.

**`/auth/login` berbatas sejak PR #73**, dan itu menutup lubang tertua di
repo ini: `httpx.RateLimit` ada sejak Langkah 13 dan tidak pernah dipasang,
padahal ADR-0005 dan ADR-0010 mewajibkannya.

Yang tersisa di Langkah 18 tinggal potongan h — blocklist HIBP. Ia menyentuh
dua jalur yang sudah ada, dan keduanya kini sudah berdiri: `Invitations.Accept`
dan `PasswordResets.Confirm`, masing-masing memanggil `identitydom.HashPassword`
di satu baris.

**Aplikasi ini sekarang bisa memulihkan akun tanpa campur tangan siapa pun.**
Orang yang lupa sandinya memanggil `POST /auth/password/reset`, membuka
tautannya, memilih sandi baru lewat `POST /auth/password/reset/confirm`, dan
seluruh perangkat yang tadinya masuk ikut dikeluarkan. Sampai #96 satu-satunya
jalan keluar dari sandi yang terlupa adalah mengubah `password_hash` langsung
di database.

**Potongan f tuntas dalam enam PR:** token undangan (#82), empat query beserta
cap sekali pakainya (#83), `Invitations.Create` yang mengundang lalu mengirim
tautannya (#84), `Invitations.Accept` yang menukar tautan jadi akun dalam satu
transaksi (#86), lalu kedua endpoint beserta perakitannya (#87, #88).

**`internal/mail` sekarang tersambung ke proses yang berjalan.** Sejak #87
`SMTP_*` dibaca `internal/config`, `mail.NewSMTP` dirakit di `cmd/api`, dan
`identitysvc.InTx` punya implementasi yang membuka transaksi dari pool. Semuanya
mendarat bersama endpoint yang memakainya, bukan lebih dulu — itu yang
membedakannya dari kode mati dan dari variabel wajib yang tak dibaca siapa pun.

**Aplikasi ini sekarang bisa melahirkan akun.** Owner memanggil
`POST /invitations`, pegawainya mengikuti tautan ke `POST /invitations/accept`,
memilih sandinya sendiri, dan langsung masuk. Sampai #87 satu-satunya akun yang
bisa ada adalah yang ditanam manual ke database.

**Yang belum punya implementasi:** antarmuka `authz.Memberships` sengaja tanpa
implementasi sampai modul project lahir di Fase 1 — `project_members` dan
`folder_members` miliknya, dan `sqlc.yaml` memegang satu entri per modul. Pola
yang sama dengan `httpx.Limiter`, yang implementasi Redis-nya menyusul di
Langkah 18. Sampai itu ada, belum satu pun query otorisasi yang pernah
dijalankan terhadap database.

**Model otorisasi berubah dua kali dalam dua hari, dan yang kedua membatalkan
sebagian yang pertama.** Pada 2026-08-07 project dikelompokkan ke dalam folder
yang keanggotaannya diwariskan ([ADR-0011](adr/0011-folder-dan-pewarisan-keanggotaan.md)).
Pada 2026-08-08 hak dipindahkan dari keanggotaan ke akun
([ADR-0012](adr/0012-peran-akun-dan-akses-owner.md)): `users.role` berisi
`owner`, `maintainer`, `contributor`, atau `viewer`, dan keanggotaan tinggal
menentukan jangkauan. Aturan peran efektif ADR-0011 batal; pewarisan
keanggotaannya tetap.

`internal/authz` karena itu ditulis ulang **di hari yang sama ia selesai**.
Bukti Langkah 17 menyebut kedua rangkaian PR-nya dengan sengaja: menandainya
selesai lalu diam-diam menggantinya adalah cara catatan ini berbohong.

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
>
> Rekonsiliasi kedelapan (2026-08-07, `82b3e4d`): 34 baris, 24 `selesai`,
> 10 `belum`. Jadi 34 · 0 · 10.
>
> Rekonsiliasi kesembilan (2026-08-07, setelah ADR-0011): tabel Fase 0 bertambah
> satu baris — migration folder — jadi 35 baris, 24 `selesai`, 11 `belum`.
> Jadi 34 · 0 · 11. Angka `selesai` tidak berubah; yang bertambah pekerjaannya.
>
> Rekonsiliasi kesepuluh (2026-08-07, `17cd30c`): 35 baris, 25 `selesai`,
> 10 `belum`. Jadi 35 · 0 · 10.
>
> Rekonsiliasi kesebelas (2026-08-08, `323084a`): 35 baris, 26 `selesai`,
> 9 `belum`. Jadi 36 · 0 · 9.
>
> Rekonsiliasi kedua belas (2026-08-08, `98c5b4d`): dua baris bertambah untuk
> pekerjaan ADR-0012 — 37 baris, 28 `selesai`, 9 `belum`. Jadi 38 · 0 · 9.
>
> Rekonsiliasi ketiga belas (2026-08-08, `46c9c04`): 37 baris, 28 `selesai`,
> 1 `sedang`, 8 `belum`. Jadi 38 · 1 · 8. Langkah 18 dimulai dan **berhenti di
> tengah** — statusnya `sedang`, bukan `belum`, dan bagian yang sudah masuk
> disebutkan di kolom buktinya.
>
> Rekonsiliasi keempat belas (2026-08-08, `e5eae60`): 37 baris — 31 sub-langkah
> bernomor (22 `selesai`, 1 `sedang`, 8 `belum`) ditambah 6 baris perubahan
> cakupan yang semuanya `selesai` — ditambah 10 gerbang dokumen.
> Jadi 38 · 1 · 8 — **tidak berubah**, dan itu benar.
> Tiga PR ter-merge (#67, #68, #69) tapi semuanya di dalam Langkah 18, yang
> sudah `sedang` sebelum sesi ini dan masih `sedang` sesudahnya. Yang bergerak
> ada di tabel potongan, bukan di tabel Fase 0. Angka yang diam bukan tanda
> tidak ada pekerjaan; ia tanda pekerjaannya belum menyentuh batas langkah.
>
> Dan paragraf di atas tabel **basi lagi**, persis seperti yang diperingatkan
> dua rekonsiliasi lalu: ia masih menulis `22 selesai dan 9 belum` untuk 31
> sub-langkah, karena rekonsiliasi ketiga belas memperkenalkan status `sedang`
> di tabel tanpa ikut membetulkan kalimatnya. Ini kedua kalinya paragraf yang
> sama tertinggal. Skrip penghitungnya sekarang memisahkan sub-langkah bernomor
> dari baris perubahan cakupan, supaya kalimat itu bisa diperiksa dan bukan
> hanya totalnya.
>
> Rekonsiliasi kelima belas (2026-08-08, `2c7d057`): dihitung dengan skrip yang
> sama — 37 baris, 31 sub-langkah bernomor (22 `selesai`, 1 `sedang`, 8 `belum`)
> ditambah 6 baris perubahan cakupan yang semuanya `selesai`, ditambah 10
> gerbang dokumen. Jadi **38 · 1 · 8**, tidak berubah untuk kedua kalinya
> berturut-turut: tiga PR ter-merge (#71, #72, #73) dan ketiganya di dalam
> Langkah 18. Paragraf di atas tabel ikut diperiksa kali ini — dengan skrip yang
> sama, bukan dengan mata — dan ia masih benar.
>
> Rekonsiliasi keenam belas (2026-08-08, `64d3f4b`): angka yang sama lagi —
> **38 · 1 · 8**, tidak berubah untuk ketiga kalinya berturut-turut. Dua PR
> ter-merge (#75, #76) dan keduanya menutup potongan d di dalam Langkah 18.
>
> **Tiga rekonsiliasi berturut-turut tanpa satu angka pun bergerak bukan tanda
> berkas ini rusak; ia tanda satu baris tabel terlalu kasar untuk pekerjaan
> sebesar Langkah 18.** Delapan potongannya sudah punya tabel sendiri, dan di
> situlah gerakannya terbaca: empat dari delapan selesai. Yang perlu dijaga
> adalah kolom bukti di baris Langkah 18 tetap menyebut potongan mana yang
> sudah masuk — kalau ia hanya menulis `sedang`, tiga sesi kerja akan hilang
> dari catatan ini tanpa ada yang bisa menemukannya.
>
> Rekonsiliasi ketujuh belas (2026-08-08, `0500277`): dihitung dengan skrip yang
> sama — 37 baris, 31 sub-langkah bernomor (22 `selesai`, 1 `sedang`, 8 `belum`)
> ditambah 6 baris perubahan cakupan yang semuanya `selesai`, ditambah 10
> gerbang dokumen. Jadi **38 · 1 · 8**, tidak berubah untuk **keempat** kalinya
> berturut-turut. Tiga PR ter-merge (#78, #79, #80) dan ketiganya menutup
> potongan e di dalam Langkah 18.
>
> Rekonsiliasi kedelapan belas (2026-08-08, `0515217`): **38 · 1 · 8** untuk
> **kelima** kalinya berturut-turut. Tiga PR ter-merge (#82, #83, #84) dan
> ketiganya di dalam potongan f, yang belum tuntas — tabel potongan karena itu
> memakai status `sedang` untuk pertama kalinya, dengan PR mana yang sudah masuk
> dan apa yang tersisa ditulis di kolom yang sama.
>
> Lima rekonsiliasi tanpa satu angka bergerak sementara sepuluh PR ter-merge
> adalah bukti paling jelas sejauh ini bahwa **satu baris tabel Fase 0 bukan
> satuan yang bisa mengukur Langkah 18.** Tabel potongan yang menanggungnya, dan
> ia sendiri sekarang mulai terlalu kasar: potongan f butuh lima PR, dan itu
> dicatat di dalam selnya, bukan dengan memecah barisnya lagi.
>
> Paragraf di atas tabel diperbarui di rekonsiliasi ini — bukan angkanya, yang
> masih benar, melainkan kalimat `empat dari delapan potongan` yang menjadi
> salah begitu potongan e masuk. Ini kelas kesalahan yang sama dengan yang
> diperingatkan dua kali di atas, hanya pindah kalimat: **yang menua di berkas
> ini bukan angkanya, melainkan prosa di sekitarnya.** Skrip penghitung tidak
> bisa memeriksa prosa, jadi setiap rekonsiliasi harus membaca ulang paragraf
> ringkasan dan tabel potongan bersamaan.
>
> Rekonsiliasi kesembilan belas (2026-08-08, `96194d7`): dihitung dengan skrip
> yang sama — 37 baris, 31 sub-langkah bernomor (22 `selesai`, 1 `sedang`,
> 8 `belum`) ditambah 6 baris perubahan cakupan yang semuanya `selesai`,
> ditambah 10 gerbang dokumen. Jadi **38 · 1 · 8** untuk **keenam** kalinya
> berturut-turut. Tiga PR ter-merge (#86, #87, #88) dan ketiganya menutup
> potongan f.
>
> Rekonsiliasi kedua puluh (2026-08-08, `0159ab3` + #96): dihitung dengan skrip
> yang sama — 37 baris, 31 sub-langkah bernomor (22 `selesai`, 1 `sedang`,
> 8 `belum`) ditambah 6 baris perubahan cakupan yang semuanya `selesai`,
> ditambah 10 gerbang dokumen. Jadi **38 · 1 · 8** untuk **ketujuh** kalinya
> berturut-turut. Tujuh PR ter-merge (#90–#96) dan ketujuhnya menutup potongan g.
>
> Tujuh rekonsiliasi diam sementara dua puluh PR ter-merge. Baris Langkah 18
> tetap `sedang` dan itu benar: tujuh dari delapan potongan selesai, dan barisnya
> baru boleh berubah kalau kedelapan-delapannya masuk.
>
> **Prosa di sekitar tabel diperiksa lebih dulu kali ini, bukan terakhir**, dan
> tiga kalimat memang sudah salah: `enam dari delapan potongan`, janji bahwa
> potongan g `menyusul`, dan — yang paling mahal — catatan di bagian "yang perlu
> diingat" bahwa potongan g butuh `LoginGuard`. Yang ketiga bukan prosa basi
> melainkan **petunjuk yang salah**, dan ia dicoret di tempatnya dengan aturan
> yang sebenarnya berlaku ditulis di bawahnya, bukan dihapus. Catatan yang salah
> dan hilang tanpa jejak adalah cara berkas ini kehilangan kepercayaan.
>
> Enam rekonsiliasi diam sementara tiga belas PR ter-merge. Yang bergerak
> memang bukan angka Fase 0: satu baris tabel itu menampung delapan potongan
> dan enam sudah selesai, tapi barisnya baru boleh berubah kalau kedelapan-
> delapannya masuk. **Kali ini yang paling perlu diperiksa adalah prosanya, dan
> ia memang sudah basi lagi** — paragraf ringkasan masih menulis `lima dari
> delapan potongan` dan masih menjanjikan `SMTP_*` sebagai pekerjaan yang
> menyusul, padahal #87 sudah memasangnya. Ketiga kalinya prosa yang sama
> tertinggal di belakang tabelnya.

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
| 16 | Middleware CSRF double-submit | selesai | Rencana Fase 0 §16, ADR-0005 | PR #46, #47 (`82b3e4d`) |
| — | Migration `00008` `folders`, `folder_members`, `projects.folder_id` | selesai | Perubahan cakupan, lihat bawah; [ADR-0011](adr/0011-folder-dan-pewarisan-keanggotaan.md) | PR #51 (`17cd30c`) |
| — | Migration `00009` & `00010` — peran akun, dan pembuangan `is_owner` | selesai | Perubahan cakupan, lihat bawah; [ADR-0012](adr/0012-peran-akun-dan-akses-owner.md) | PR #59, #60, #63 (`98c5b4d`) |
| — | Modul identity membawa peran akun, bukan `is_owner` | selesai | ADR-0012 | PR #61 |
| 17 | `internal/authz` + table-driven test **tujuh** pola | selesai | Rencana Fase 0 §17, [authorization.md](authorization.md), ADR-0012 | PR #53, #54, #55, lalu **ditulis ulang** di #62 (`94d2303`) |
| 18 | Undangan, reset password, daftar & cabut sesi + OpenAPI | sedang | Rencana Fase 0 §18, ADR-0009, ADR-0010 | 7 dari 8 potongan selesai — a: #65 · b: #67, #68 · c: #69, #71, #72, #73 · d: #75, #76 · e: #78, #79, #80 · f: #82, #83, #84, #86, #87, #88 · g: #90, #91, #92, #93, #94, #95, #96. Sisa: h (HIBP) |
| 19 | **Gerbang manusia:** arah desain tertulis | belum | Rencana Fase 0 §19, `rules/15-ui-design.md` §2 | — |
| 20 | Scaffold Vite + shadcn + token | belum | Rencana Fase 0 §20 | — |
| 21 | Generate tipe dari OpenAPI + layar login empat state | belum | Rencana Fase 0 §21 | — |
| 22 | Embed SPA ke biner, satu origin | belum | Rencana Fase 0 §22, ADR-0001 | — |
| 23 | `cmd/seed --size=small\|full` | belum | Rencana Fase 0 §23 | — |
| 24 | `cmd/worker` minimal — pangkas sesi kedaluwarsa | belum | Rencana Fase 0 §24 | — |
| 25 | Dockerfile, `compose.prod.yml`, Caddy, workflow deploy | belum | Rencana Fase 0 §25 | — |
| 26 | Backup + uji restore sungguhan | belum | Rencana Fase 0 §26 | — |

## Yang perlu diketahui sebelum menulis baris pertama

Log sesi tidak lagi ikut repo (lihat perubahan cakupan di bawah), jadi berkas
ini satu-satunya yang membawa konteks antar-sesi. Isi bagian ini adalah hal
yang **mengubah cara kerja**, bukan sejarah.

- **Branch protection ditegakkan server sejak 2026-08-07.** Nol approval, dua
  check wajib, berlaku untuk admin. `gh pr merge --auto` sekarang aman.
- **Jangan menambah `oasdiff`, `sqlc`, atau `migrations` sebagai check wajib.**
  Ketiganya disaring per-path dan tidak melapor di PR yang tidak menyentuh
  path-nya — PR dokumen akan menggantung selamanya di *Expected*.
- **Test skema dan repository skip diam-diam tanpa `TEST_DATABASE_URL` dan
  `TEST_REDIS_URL`,** dan hasilnya tetap terlihat `ok`. Di mesin pengembangan
  utama Docker sering tidak berjalan, jadi **CI yang memverifikasinya**, dari
  database kosong. Saat Docker **berjalan**, jalankan sendiri: compose
  memetakan PostgreSQL ke port di `.env`, dan
  `TEST_DATABASE_URL=postgres://<user>:<sandi>@127.0.0.1:<port>/<db>?sslmode=disable`
  menyalakan seluruh test skema. Kredensialnya dari `.env`, jangan disalin ke
  berkas mana pun yang ikut repo.
- **Jalankan migration baru terhadap database yang benar-benar kosong, bukan
  database lokal yang sudah ter-migrate.** Buat database sekali pakai
  (`CREATE DATABASE ...`), arahkan `TEST_DATABASE_URL` ke sana, lalu
  `go test ./...`. Migration `00008` lolos di database lokal dan gagal di
  database kosong; bedanya bukan isi skemanya, melainkan paket test yang
  bermigrasi bersamaan.
- **`/auth/login` berbatas sejak PR #73** — lubang tertua di repo ini, tertutup.
  Yang perlu diingat tentang bentuknya: ia menolak **sebelum** password
  disentuh, ia menghitung **kegagalan** bukan percobaan, dan ia **gagal
  tertutup**. Endpoint berikutnya yang butuh batas harus memilih sadar antara
  dua bentuk: `httpx.RateLimit` (middleware, menghitung setiap permintaan, cocok
  untuk pencarian dan endpoint mahal) dan `identitysvc.LoginGuard`
  (menghitung kegagalan, cocok untuk apa pun yang memverifikasi rahasia —
  reset password dan OTP di potongan g).
- **Test yang memakai Redis harus memberi penanda per-jalan pada kuncinya.**
  Redis hidup lebih lama daripada proses test, jadi kunci yang hanya diturunkan
  dari `t.Name()` membuat `go test -count=2` — dan setiap rerun CI di dalam
  jendela terpanjang — mulai dengan penghitung yang sudah penuh. Gejalanya
  terbaca seperti pembatas yang rusak, bukan seperti test yang memakai ulang
  kunci. Cacat ini hidup sejak PR #24 dan baru ketahuan di PR #68.
- **Mutasi yang lolos berarti test-nya yang palsu, bukan mutasinya yang lemah.**
  Di PR #69 nomor urut anggota ZSET dibuang dan seluruh test tetap hijau —
  karena `Record` berurutan hampir tak pernah mendarat di milidetik yang sama,
  jadi tabrakan yang diklaim diuji tidak pernah terjadi. Test-nya ditulis ulang
  jadi 50 `Record` bersamaan. Untuk apa pun yang dijaga atomisitas, test-nya
  harus konkuren; yang berurutan hanya terlihat seperti menguji.
- **`oasdiff breaking` tidak menangkap penghapusan properti dari respons.**
  PR #61 menghapus `is_owner` dari `/me` dan `/auth/login` — perubahan yang
  merusak setiap klien yang membacanya — dan gerbangnya hijau. Ia menjaga sisi
  permintaan; sisi respons harus dijaga mata.
- **Jangan pakai `CREATE INDEX CONCURRENTLY` di migration repo ini.** Ia
  menunggu setiap transaksi yang bisa melihat tabelnya, kena `lock_timeout`,
  dan karena `-- +goose NO TRANSACTION` tidak bisa di-rollback, ia meninggalkan
  indeks **INVALID**. Setiap migration sesudahnya gagal dengan `already exists`
  — permanen, sampai ada yang menghapus indeksnya manual. Terjadi pada
  percobaan pertama `00008`. Kalau suatu saat memang perlu (indeks pada tabel
  besar yang sudah berisi), ia butuh rencana pemulihan lebih dulu, bukan hanya
  berkas migration.
- **`go test -race` tidak bisa dijalankan lokal** — detektornya menuntut cgo.
  Untuk apa pun yang menyentuh konkurensi, CI-lah verifikasinya.
- **Target 400 baris per PR itu nyata.** Pada 2026-08-07 pekerjaan dipecah lima
  kali *sesudah* ditulis, yang berarti menulis ulang test untuk potongan yang
  berbeda. Putuskan potongannya sebelum menulis, bukan sesudah.
- **Sejak Langkah 16, setiap `POST`, `PATCH`, `PUT`, dan `DELETE` wajib
  membawa pasangan CSRF** — termasuk `/auth/login`. Memanggilnya dengan `curl`
  sekarang menuntut dua langkah: satu permintaan aman untuk mengambil cookie
  `__Host-csrf`, lalu salin nilainya ke header `X-CSRF-Token`. Permintaan yang
  lupa itu dijawab `403`, bukan `401`, dan mudah tersalahartikan sebagai
  masalah sesi.
- **Setiap perintah test dijalankan di shell baru, jadi `TEST_DATABASE_URL`
  harus disetel di perintah yang sama.** Menyetelnya di satu perintah lalu
  menjalankan `go test` di perintah berikutnya menghasilkan seluruh paket
  repository **skip diam-diam** sambil melaporkan `ok`. Terjadi di sesi ini, dan
  hanya ketahuan karena hasilnya diperiksa ulang dengan `-v`. Untuk paket yang
  bisa skip, `-v` bukan kemewahan: ia satu-satunya beda antara `ok` yang berarti
  lulus dan `ok` yang berarti tidak dijalankan.
- **`gh pr merge --auto --delete-branch` tidak menghapus branch-nya.** Ketiga PR
  sesi ini di-arm dengan flag itu, ketiganya ter-merge, dan ketiga branch masih
  hidup di `origin` sesudahnya. Pada jalur auto-merge flag itu tidak berlaku;
  penghapusannya harus dilakukan sendiri (`git push origin --delete <branch>`).
  Gejalanya menumpuk diam-diam: squash merge membuat `git branch --merged main`
  tidak pernah menampilkannya.
- **Test yang butuh listener memakai `net.ListenConfig`, bukan `net.Listen`.**
  Linter `noctx` menolak yang kedua, dan itu berlaku di berkas test juga —
  pengecualian di `.golangci.yml` hanya untuk `gosec`.
- **Mutasi yang tidak kompilasi tidak menguji apa pun.** Melepas satu pemakaian
  sering membuat impor jadi tak terpakai; `go test` berhenti di build dan
  hasilnya terbaca seperti test yang lemah. Ini terjadi tiga kali dalam satu
  sesi sebelum polanya disadari, dan dua kali lagi sesudahnya.
- **Rate limit yang dikunci ke pemanggil harus dipasang di *dalam* penjaga
  sesi, bukan di luarnya.** Kuncinya diambil dari akun di context, dan akun itu
  baru ada setelah penjaga menyelesaikannya. Dipasang di luar, setiap kunci
  kosong, `httpx.RateLimit` melewatkan permintaan tanpa menghitung — dan
  pembatas yang tidak menghitung apa pun **terlihat persis seperti pembatas yang
  terpasang**. Ini kelas cacat yang sama dengan `httpx.RateLimit` yang tidak
  pernah dipasang sampai PR #73, hanya lebih sulit dilihat. Ada test untuk
  urutannya di `route_test.go`; komentar saja tidak cukup untuk klaim seperti
  ini.
- **Response yang statusnya sudah dikirim tidak bisa dibatalkan, dan itu
  menyembunyikan mutasi.** Membuang `return` sesudah menulis error tidak
  mengubah apa pun yang bisa dilihat sebuah test HTTP: header sudah terkirim,
  jadi cookie yang ditambahkan sesudahnya hilang dan status tidak berubah.
  Kodenya tetap salah — ia memanggil `Sessions.Issue` untuk akun yang tidak ada.
  Yang menangkapnya adalah **efek sampingnya**: "penukaran yang ditolak tidak
  membuat baris sesi". Untuk handler, memeriksa status dan body saja
  meninggalkan celah sebesar satu `return`.
- **Bentuk error `httpx` tidak bisa disusun ulang dari luar** — `errorBody` dan
  `errorEnvelope` tidak di-export, disengaja. Endpoint yang ingin body 502
  dengan muatan tambahan harus mengubah `httpx` lebih dulu. Di potongan f
  jawabannya adalah tidak: kegagalan kirim dijawab 500 dengan amplop yang sama,
  dan id undangannya masuk log. Satu bentuk response kedua untuk satu kasus
  tidak sepadan.
- **`emit_pointers_for_null_types` tidak berlaku untuk `timestamptz`.** Kolom
  nullable tetap datang sebagai `pgtype.Timestamptz` dengan `.Valid`, bukan
  `*time.Time`. Konversinya berhenti di lapisan service — domain tidak mengimpor
  apa pun dari repository (ADR-0008).
- **Ada properti yang tidak bisa dilihat lewat panggilan store mana pun, dan
  test-nya akan terlihat lengkap sampai ada yang memutasinya.** Di #94 memindah
  `HashPassword` ke **dalam** transaksi tidak mengubah satu pun assertion:
  panggilan mahal itu bukan panggilan store, jadi `store.calls` tetap identik.
  Yang hilang bukan test yang lemah melainkan kejadian yang tak terekam.
  Jawabannya membuat kejadiannya terekam — fake `inTx` sekarang mencatat
  `"begin"` — bukan menambah assertion pada yang sudah terlihat. Kalau sebuah
  klaim berbunyi "X tidak terjadi di dalam Y", maka **Y harus punya jejak**.
- **Satu batas transaksi per modul berarti setiap fake menanggung seluruh
  antarmuka.** `TxStore` (dulu `InvitationStore`, diganti nama di #90) tumbuh
  enam metode selama potongan g, dan tiga fake yang sudah ada masing-masing ikut
  bertambah — sekitar delapan puluh baris stub yang tidak diuji siapa pun.
  Ongkos ini disengaja dan alasannya ada di komentar `TxStore`, tapi ia tumbuh
  linear terhadap jumlah service. **Kalau modul berikutnya membuat fake-nya lebih
  panjang daripada test-nya, keputusan itu perlu ditinjau ulang** — bukan
  sekarang.
- **`Get-Content -Raw` lalu `Set-Content -Encoding utf8` merusak karakter
  non-ASCII.** PowerShell 5.1 membaca berkas UTF-8 sebagai ANSI, jadi setiap em
  dash kembali sebagai dua karakter dan ditulis ulang sebagai empat byte. Ini
  merusak `passwordreset.go` saat dipakai untuk menjalankan mutasi. **Untuk
  mengubah berkas, pakai alat edit; PowerShell untuk menjalankan perintah, bukan
  untuk menyunting.** Sekelas dengan heredoc bash yang menelan backslash regex,
  yang sudah tercatat di sesi undangan.
- **Pesan commit dan body PR lewat berkas, bukan lewat `-m`.** PS 5.1 memecah
  argumen yang memuat tanda kutip ganda saat meneruskannya ke `git`, dan
  gejalanya `pathspec '...' did not match any file(s)` — terbaca seperti masalah
  git, padahal masalah shell. `git commit -F <berkas>` dan
  `gh pr create --body-file <berkas>` tidak punya persoalan ini.

## Langkah 18, dipotong sebelum ditulis

Delapan potongan, diputuskan pada 2026-08-08 sebelum baris pertamanya. Urutannya
menutup lubang rate limit lebih dulu.

| # | Isi | Status |
|---|---|---|
| a | `httpx.ClientIP` — proxy tepercaya, agregasi IPv6 /64 | selesai, PR #65 |
| b | Sliding window berlapis di `internal/redis`, menggantikan `FixedWindow` yang tak terpakai | selesai, PR #67 (menggeser) & #68 (berlapis) |
| c | `/auth/login` berbatas: ember akun + IP + prefiks, gagal tertutup | selesai — #69 primitif, #71 kunci jaringan, #72 kebijakan, #73 pemasangan |
| d | Daftar & cabut sesi + kontrak | selesai — #75 query & kebijakan, #76 endpoint & kontrak |
| e | `internal/mail` — pengirim SMTP, dan penangkap untuk test | selesai — #78 render, #79 penangkap, #80 SMTP |
| f | Undangan: owner membuat akun pegawai | selesai — 6 PR: #82 token, #83 query, #84 pembuatan & pengiriman, #86 penukaran, #87 `POST /invitations` + `SMTP_*` + perakitan, #88 `POST /invitations/accept` |
| g | Reset password | selesai — 7 PR: #90 `TxStore`, #91 token & jendela, #92 query, #93 `Request`, #94 `Confirm`, #95 `POST /auth/password/reset`, #96 `POST /auth/password/reset/confirm` |
| h | Blocklist HIBP dengan k-anonymity, bertimeout, gagal terbuka | belum |

Potongan f akhirnya butuh **enam** PR, bukan lima seperti yang diputuskan
sebelum baris pertamanya. Yang meleset bukan jumlah pekerjaannya melainkan
ukurannya: PR terakhir yang direncanakan berisi endpoint, kontrak, `SMTP_*`, dan
perakitan sekaligus, dan itu jauh di atas 400 baris. Ia dipecah lagi
**sebelum ditulis** — #87 plumbing dan endpoint pengundangan, #88 endpoint
penukaran — jadi kali ini aturan "potong sebelum menulis" bertahan meski
angkanya tidak.

Yang perlu diingat saat melanjutkan:

- **Undangan mengirim surelnya *setelah* transaksi commit, bukan di dalamnya.**
  Menahan transaksi selama satu perjalanan SMTP adalah kunci pada tabel
  `invitations` yang digantungkan pada pihak ketiga sampai 20 detik. Ongkos yang
  diterima: baris yang tertulis tapi tak terkirim. Ia mati — tokennya tidak
  pernah disimpan — dan percobaan ulang menggantikannya. Endpoint berikutnya
  yang mengirim surel (reset password, potongan g) harus memilih urutan yang
  sama secara sadar.
- **`invitations.role` sudah memakai kosakata ADR-0012 sejak `00009`**
  (`maintainer`, `contributor`, `viewer` — `owner` sengaja absen). Tidak perlu
  migration untuk undangan; ini sempat terlihat perlu karena `00001` masih
  menuliskan `admin/member/viewer` dan yang membatalkannya ada di `00009`.
- **Menguji atomisitas menuntut koneksi yang berbeda, bukan hanya goroutine.**
  `TestConcurrentRedemptionsLetExactlyOneThrough` satu-satunya test di repo ini
  yang **commit** — dua transaksi tidak bisa balapan di dalam satu transaksi —
  dan ia membersihkan barisnya sendiri di `t.Cleanup`. Dijalankan berurutan,
  klaim sekali-pakai itu lolos walau penjaganya dibuang.
- **`identitysvc.InTx` adalah fungsi, bukan handle pgx.** Batas transaksi tetap
  milik service, tapi pgx tidak ikut masuk ke sana — itu yang membuat service-nya
  bisa diuji dengan fake biasa. Implementasinya (yang membuka pool) belum ada.
- **Undangan hanya untuk membuat akun baru.** Menambahkan orang yang sudah
  punya akun ke folder atau project berlaku **langsung tanpa persetujuan**
  (keputusan pemilik, 2026-08-08). `invitations.role` berisi peran **akun**.
- **Angka ember jaringan saya pilih sendiri, bukan dari ADR-0010** — 300 / 10
  menit dan 2000 / hari. Tabel ADR-0010 hanya punya baris per akun dan per IP.
  Ini yang pertama layak diperdebatkan kalau ada yang terkunci tanpa sebab; ia
  ada di `DefaultLoginLimits` dan ditandai di komentarnya.
- ~~**Potongan g (reset password) butuh `LoginGuard` lagi, bukan
  `httpx.RateLimit`**: ia memverifikasi rahasia, jadi yang dihitung kegagalan.~~
  **Salah, dan dikoreksi di #96.** Catatan ini ditulis sebelum
  `/invitations/accept` ada. Endpoint itu juga memverifikasi rahasia dan
  `cmd/api/main.go` memutuskan sebaliknya, dengan alasan yang ditulis di
  tempatnya: token acak 32 byte bukan sesuatu yang ditebak siapa pun, jadi yang
  layak dibatasi trafiknya. Token reset lahir dari `newOpaqueToken` yang sama.
  **Aturan yang sebenarnya berlaku:** `LoginGuard` untuk rahasia yang bisa
  ditebak manusia — sampai sekarang hanya password di `/auth/login`;
  `httpx.RateLimit` untuk segala sesuatu yang lain, termasuk endpoint yang
  menukar token opaque.
- **Potongan f yang merakit `internal/mail`, dan sudah dilakukan di #87.**
  `SMTP_*` dibaca `internal/config`, `mail.NewSMTP` dirakit di `cmd/api`, dan
  `identitysvc.InTx` membuka transaksi dari pool. Potongan g mewarisi ketiganya
  dan tidak perlu merakit apa pun lagi — yang ia butuhkan hanya memanggil
  pengirim yang sudah ada.
- **Reset password menuntut tiga hal dari potongan f, dan ketiganya sudah ada:**
  pengirim yang terpasang, `InTx` yang sungguhan, dan pola "kirim setelah commit"
  yang harus dipilih ulang secara sadar. Yang belum ada dan hanya milik potongan
  g: `password_resets` sudah punya tabelnya sejak `00001` tapi belum satu query
  pun, dan `LoginGuard` — bukan `httpx.RateLimit` — yang menjaganya, karena ia
  memverifikasi rahasia.
- **`mail.Render` menolak karakter kendali, tidak membersihkannya.** `net/mail`
  sebenarnya menetralkan alamat yang disisipi dengan sendirinya — ia mengutip
  seluruhnya ke dalam local part — tapi yang keluar adalah header sah yang
  menyebut alamat yang tidak dimaksudkan siapa pun. Ini ditemukan lewat test
  yang gagal, bukan lewat rancangan.
- **`mail.Capture` merender, bukan sekadar menumpuk.** Penangkap yang hanya
  menambah ke slice akan meloloskan test atas pesan yang ditolak pengirim
  sungguhan. Pengirim palsu tidak boleh lebih longgar daripada yang asli.
- **Amplop SMTP membawa alamat telanjang; header membawa nama tampilan.**
  `RCPT TO` berisi nama tampilan ditolak seluruh pengirimannya oleh server
  sungguhan, dan server palsu yang mengurai isi tanda kurung siku akan
  memaafkannya — jadi server palsu di `smtp_test.go` mencatat baris perintahnya
  apa adanya.
- **`AllowUnencrypted` hanya untuk Mailpit.** Tanpa enkripsi, pesan, penerima,
  dan tautan resetnya menyeberang jaringan terbaca. Kredensial bersama opsi itu
  ditolak di konstruktor.
- **Kepemilikan sesi ditegakkan di `WHERE`, bukan di Go.** `DeleteSessionForUser`
  memuat `AND user_id`, dan test-nya berjalan terhadap PostgreSQL sungguhan
  dengan dua akun. Endpoint berikutnya yang menyentuh data milik seseorang harus
  memakai pola yang sama — pemeriksaan kepemilikan di Go bisa hilang dalam satu
  refactor tanpa satu test pun berubah warna.
- **Fixture yang berbohong tentang tipe kolom baru ketahuan saat kolomnya
  dipakai.** Fixture route membagikan id sesi `"session-1"` sejak Langkah 15,
  padahal kolomnya `uuid`. Ia tidak pernah salah sampai ada kode yang memvalidasi
  bentuknya. Kalau sebuah stub memalsukan sebuah id, palsukan dengan bentuk yang
  benar.
- Potongan f sampai h butuh SMTP. Mailpit sudah ada di compose dan menutup
  seluruh pengembangan lokal; penyedia produksinya belum dipilih dan baru
  menggigit di Langkah 25.

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
| Penyedia SMTP untuk produksi. `SMTP_HOST` dan kawan-kawan sudah wajib di [environments.md](environments.md), tapi layanannya belum dipilih | 2026-08-08 | Langkah 25 (deploy), bukan Langkah 18 — Mailpit menutup seluruh pengembangan lokal |
| Angka ember jaringan: 300 / 10 menit dan 2000 / hari. [ADR-0010](adr/0010-ip-klien-dan-rate-limit.md) tidak punya barisnya, jadi keduanya dipilih sendiri saat menulis `DefaultLoginLimits` | 2026-08-08 | Tidak memblokir apa pun — ia sudah berjalan dengan angka itu. Yang perlu diketahui: angka ini yang pertama dicurigai kalau ada yang terkunci tanpa sebab, dan mengubahnya cuma menyunting satu konstanta |
| Jendela undangan tujuh hari. [ADR-0005](adr/0005-autentikasi-sesi-cookie.md) menetapkan jendela sesi dan diam soal undangan, jadi angkanya dipilih sendiri saat menulis `identitydom.InvitationWindow` | 2026-08-08 | Tidak memblokir apa pun — ia sudah berjalan dengan angka itu. Yang perlu diketahui: ia yang pertama dicurigai kalau ada pegawai yang harus minta undangan kedua, dan mengubahnya cuma menyunting satu konstanta |
| Angka batas kedua endpoint undangan: 20 per jam dan 100 per hari untuk mengundang, 10 per 10 menit dan 50 per jam untuk menukar. ADR-0005 §131 mewajibkan adanya batas dan diam soal angkanya | 2026-08-08 | Tidak memblokir apa pun — keduanya sudah berjalan. Yang perlu diketahui: batas pengundangan yang pertama dicurigai kalau owner tertahan saat onboarding satu tim sekaligus, dan keduanya ada di `cmd/api` sebagai dua konstanta |

Pertanyaan `SMTP_USERNAME` dan `SMTP_PASSWORD` **tertutup pada 2026-08-08**:
keduanya wajib saat `APP_ENV=production` dan opsional di `local`, sejalan dengan
cara `APP_BASE_URL` sudah diperketat di produksi. Terpasang di PR #87, dan
[environments.md](environments.md) ikut dibetulkan supaya dokumennya tidak
menyimpang dari kodenya.


Pertanyaan branch protection **tertutup pada 2026-08-07**. Repo dijadikan
publik — proteksi digerbang plan dan gratis hanya untuk repo publik — lalu
proteksinya dipasang:

| Setelan | Nilai |
|---|---|
| Approval wajib | 0 — proyek satu orang; GitHub tidak mengizinkan approve PR sendiri, dan mensyaratkan satu approval berarti mengunci diri sendiri |
| Check wajib | `Go — vet, lint, test` dan `Pemindai secret` |
| Branch harus mutakhir sebelum merge | ya |
| Berlaku untuk admin | ya |
| Force push dan hapus branch | ditolak |

**Hanya dua check yang wajib, dan itu disengaja.** `oasdiff`, `sqlc`, dan
`migrations` disaring per-path: ketiganya tidak melapor di PR yang tidak
menyentuh path-nya, jadi menjadikannya wajib akan menggantungkan setiap PR
dokumen selamanya di status *Expected*.

Riwayatnya, supaya tidak terulang: sebelum ini `main` tidak punya proteksi apa
pun. Itu ditemukan lewat akibatnya, bukan lewat pemeriksaan pengaturan —
`gh pr merge --auto` pada PR #13 langsung mengeksekusi merge, karena auto-merge
hanya menunggu *required check* dan tidak ada satu pun yang wajib. PR #13
ter-merge sebelum CI-nya selesai; CI-nya kemudian hijau, tapi itu keberuntungan.
Sejak PR #14 urutannya jadi `gh pr checks --watch` dulu, baru merge. Sekarang
urutan itu ditegakkan server, bukan ingatan.

Pertanyaan ukuran PR sudah terjawab pada 2026-08-07: Langkah 8–11 dipecah jadi
tujuh PR, masing-masing di bawah 250 baris yang ditulis tangan.

Empat pertanyaan lain terjawab pada 2026-08-07, dan yang pertama mengubah model
otorisasi:

| Pertanyaan | Jawaban | Tercatat di |
|---|---|---|
| Siapa yang boleh membuat project | Setiap akun aktif, bukan hanya `owner` instalasi. Pembuat otomatis `admin`, jejaknya di `projects.created_by` yang sudah ada sejak migration `00002` | [authorization.md](authorization.md), [openapi.yaml](api/openapi.yaml) |
| Apakah "folder" hal yang sama dengan project | Bukan. Folder adalah wadah berisi banyak project, satu tingkat, dan **keanggotaannya diwariskan** ke project di dalamnya. Peran efektif = yang tertinggi antara peran folder dan peran project | [ADR-0011](adr/0011-folder-dan-pewarisan-keanggotaan.md) |
| Rujukan desain untuk gerbang Langkah 19 | Jira — satu-satunya rujukan terkenal yang punya sprint, backlog, burndown, dan laporan waktu sekaligus. Kepadatan kontrolnya justru yang perlu ditahan saat diterjemahkan | Langkah 19, belum ditulis |
| Pin `gitleaks` | v8.30.1, dengan `GOTOOLCHAIN=auto` | `ci.yml`, PR #49 |

Tiga pertanyaan terjawab lebih awal pada 2026-08-07 dan menjadi dua ADR:

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
| 2026-08-07 | Branch protection dipasang di `main`: 0 approval, dua check wajib, berlaku untuk admin | Menutup pertanyaan yang terbuka sejak 2026-08-07. Approval 0 karena proyek satu orang; yang ditegakkan adalah "lewat PR dan lewat CI hijau", bukan adanya orang kedua | ya — keputusan pemilik |
| 2026-08-07 | Repo dijadikan publik | Branch protection, ruleset, *secret scanning*, dan *push protection* semuanya digerbang plan: gratis hanya untuk repo publik. Push protection adalah kontrol yang sebelumnya tidak ada — ia menolak secret sebelum commit-nya mendarat, sedangkan `gitleaks` di CI baru berteriak setelah ter-push | ya — keputusan pemilik pada 2026-08-07 |
| 2026-08-07 | Log sesi (`docs/sessions/`) dilepas dari repo dan masuk `.gitignore` | Permintaan pemilik, menyusul repo jadi publik. Isinya catatan proses, bukan dokumentasi proyek. Konsekuensinya: klon baru tidak membawa riwayat sesi, jadi `progress.md` menjadi satu-satunya pembawa konteks antar-sesi yang ikut repo | ya |
| 2026-08-07 | `oasdiff` dipin ke v1.28.0 dan dipasang dengan `GOTOOLCHAIN=auto` | Gerbang kontrak berhenti bisa memasang alatnya: `@latest` menuntut Go 1.26. Sekaligus menghapus `@latest` — gerbang yang alatnya tidak dipin bisa berubah perilakunya tanpa satu pun commit di repo ini | ya — ditanyakan dan disetujui pemilik pada 2026-08-07 |
| 2026-08-08 | **Hak dipindahkan dari keanggotaan ke akun.** `users.role` jadi satu-satunya sumber hak; kolom `role` di `project_members` dan `folder_members` dihapus | Permintaan pemilik. Penggunanya pegawai, dan jabatan pegawai tidak berubah karena ia pindah project. Rancangan sebelumnya menduplikasi jabatan itu ke setiap baris keanggotaan dan menyisakan pertanyaan tanpa jawaban benar: manajer yang diundang ke project lain, diundang sebagai apa. Lihat [ADR-0012](adr/0012-peran-akun-dan-akses-owner.md) | ya — keputusan pemilik |
| 2026-08-08 | **`owner` menjadi akses penuh ke semua folder dan project**, tanpa perlu keanggotaan, dengan setiap akses di luar keanggotaan tercatat di `activity_events` | Permintaan pemilik, membalik daftar tertutup yang sebelumnya menahan owner dari isi project. Konsekuensinya dinyatakan di ADR: satu akun bisa membaca seluruh instalasi, jadi 2FA untuk akun owner naik dari "kalau sempat" menjadi layak dijadwalkan | ya — keputusan pemilik |
| 2026-08-08 | Peran dinamai `owner` / `maintainer` / `contributor` / `viewer` | Kosakata GitHub dan GitLab, dan bentuknya kemampuan bukan jabatan — nama seperti `engineer` atau `tech_lead` akan salah untuk QA, desainer, dan analis sejak hari pertama | ya — dipilih pemilik dari empat usulan |
| 2026-08-08 | Migration `00009` disunting di tempat saat penggantian nama, bukan ditimpa migration rename | Diperiksa lebih dulu: satu-satunya database yang ada berada di versi 8, jadi belum ada instalasi yang menerapkannya. goose tidak menyimpan checksum, jadi kalau sudah ada yang menerapkan, jawabannya harus berbeda | ya |
| 2026-08-08 | Kolom `owner` di tiga baris pengelolaan anggota diubah dari `ya` menjadi `—` di [authorization.md](authorization.md) | Matriks dan daftar tertutup di dokumen yang sama saling bertentangan; daftar itu menyatakan dirinya tertutup dan tidak memuat ketiganya. Ditemukan saat menerjemahkan matriks jadi tabel `internal/authz`. Daftar tertutup yang dimenangkan karena lebih ketat dan tidak menghilangkan apa pun: owner menambahkan dirinya sebagai `admin` lebih dulu, langkah yang tercatat dan memberi tahu anggota | ya — lewat merge PR #55, yang deskripsinya menjelaskan pilihan ini |
| 2026-08-08 | `Project \| hapus permanen` ditegakkan sebagai **owner dan anggota**, bukan salah satunya | Ambiguitas yang sama: matriks menulis "Harus anggota", daftar tertutup menyebutnya hak di luar keanggotaan. Diambil yang lebih ketat | ya — lewat merge PR #55 |
| 2026-08-07 | Pembuatan project dibuka untuk **setiap akun aktif**, bukan hanya `owner` instalasi | Permintaan pemilik. Aman karena pendaftaran mandiri tetap non-goal: akun hanya lahir dari undangan `owner`, jadi himpunan pembuatnya tetap terkendali. Tidak butuh migration — `projects.created_by` sudah ada sejak `00002` | ya |
| 2026-08-07 | **Folder** ditambahkan sebagai wadah project, dengan keanggotaan yang diwariskan | Permintaan pemilik. Ini penambahan cakupan besar, dan konsekuensinya bukan satu tabel: setiap pemeriksaan izin jadi punya dua sumber kebenaran, dan memindahkan project antar-folder menjadi perubahan akses. Lihat [ADR-0011](adr/0011-folder-dan-pewarisan-keanggotaan.md) | ya — keputusan pemilik |
| 2026-08-07 | Skema folder dijadwalkan masuk di Fase 0, sebelum `internal/authz` ditulis | Alasan yang sama dengan [ADR-0007](adr/0007-postgresql-18.md): database masih kosong. Menulis `authz` tanpa folder lebih dulu berarti menulis ulang seluruh test-nya begitu folder datang | ya |
| 2026-08-07 | `gitleaks` dipin ke v8.30.1 dan dipasang dengan `GOTOOLCHAIN=auto`, menutup pertanyaan yang terbuka sejak 2026-08-07 | Alasan yang sama dengan `oasdiff`, dan taruhannya lebih besar: ia salah satu dari dua check **wajib**, jadi rilis yang menuntut Go lebih baru akan menghentikan seluruh merge tanpa satu pun commit di repo ini. v8.30.1 sendiri hanya menuntut Go 1.24.11; `GOTOOLCHAIN=auto` dipasang supaya kenaikan pin berikutnya tidak gagal karena alasan yang sudah dikenali | ya — ditanyakan dan disetujui pemilik pada 2026-08-07 |
| 2026-08-07 | *Allow auto-merge* dinyalakan di setelan repo | `gh pr merge --auto` sebelumnya ditolak — branch protection dan *Allow auto-merge* adalah dua setelan berbeda, dan yang kedua mati. Gerbangnya tidak berubah: branch protection tetap yang menentukan kapan sebuah PR boleh masuk | ya — keputusan pemilik pada 2026-08-07 |
| 2026-08-07 | `password.minLength` di kontrak turun dari 8 ke 1, dengan `maxLength` 1024 | Kebijakan panjang ditegakkan saat password **dibuat**, bukan saat dipakai. Endpoint login yang menolak password pendek baru saja memberi tahu pemanggilnya apa aturannya. `oasdiff` tidak menganggapnya merusak | ya — lewat merge PR #39 |
| 2026-08-07 | Normalisasi NFKC sebelum hashing dan sebelum verifikasi password | [ADR-0009](adr/0009-kebijakan-password.md). Tanpa itu password yang sama dari papan ketik berbeda gagal login. Konsekuensinya: normalisasi tidak bisa diubah lagi setelah ada hash tersimpan | ya |
| 2026-08-07 | Langkah 16 dipecah jadi dua PR (#46 pemeriksaan, #47 penerbitan cookie & pemasangan) | Ditulis sekaligus jadi 573 baris. Kali ini potongannya diputuskan sebelum PR pertama dibuat, bukan sesudah | ya |
| 2026-08-07 | Cookie CSRF bernama `__Host-csrf`, bukan `csrf` seperti tertulis di ADR-0005 dan kontrak. ADR-0005 disusulkan pada 2026-08-07 supaya dokumennya tidak menyimpang dari kodenya | Double-submit hanya rahasia selama tidak ada pihak lain yang bisa menulis cookie-nya. Subdomain saudara yang menulis `csrf` biasa memegang kedua belahnya sekaligus; cookie `__Host-` bersifat host-only menurut definisinya, jadi ia tidak bisa. Ini kelemahan bawaan double-submit, dan awalan itu yang menutupnya | ya — lewat merge PR #47, yang deskripsinya menjelaskan penyimpangan ini |
| 2026-08-07 | Cookie CSRF diterbitkan middleware pada permintaan aman mana pun, bukan oleh handler login seperti tersirat di kontrak | Kalau penerbitannya milik satu endpoint, endpoint yang mengubah data bergantung pada endpoint lain yang ingat menerbitkannya — lubang yang sama dengan "lupa memasang middleware", dari sisi sebaliknya. Akibat baiknya `POST /auth/login` ikut terlindungi dari login lintas-situs | ya — lewat merge PR #47 |
| 2026-08-07 | Rantai middleware diangkat dari `run()` menjadi `apiHandler()` di `cmd/api` | ADR-0005 menaruh pemeriksaan CSRF di router justru supaya tidak ada route yang bisa ditambahkan tanpanya. Rantai yang hanya dirakit di dalam `run()` tidak bisa ditanyai apakah itu masih benar; sekarang ada dua test yang menanyakannya | ya — lewat merge PR #47 |
| 2026-08-08 | **`DELETE /me/sessions` menyisakan sesi yang sedang dipakai** | Terbaca mengejutkan dari URL-nya, jadi alasannya ditulis di kontrak dan bukan hanya di kode: `POST /auth/logout` sudah mengakhiri sesi yang sedang dipakai, jadi pemanggil yang ingin keluar dari mana-mana termasuk dari sini memanggil keduanya. Satu endpoint yang mengeluarkan pemanggilnya sambil masih berutang jawaban adalah setengah yang lebih buruk dari pasangan itu | ya — lewat merge PR #76, yang deskripsinya menjelaskan pilihan ini |
| 2026-08-08 | **Pencetakan token diangkat jadi milik bersama sesi dan undangan** (`identity/domain/token.go`) | Keduanya nyaris menjadi sepuluh baris kode keamanan yang sama di dua berkas, dan lebar 32 byte yang mereka sepakati adalah yang diharapkan `CHECK octet_length` di setiap kolom `token_hash`. Penyimpangan kecil dari disiplin ruang lingkup — ia menyentuh `session.go` — dengan alasan duplikasi kode keamanan adalah bug yang belum meledak. Seluruh test sesi lulus tanpa disunting, dan dua mutasi pada berkas bersama itu membunuh test di kedua sisi | ya — lewat merge PR #82 |
| 2026-08-08 | **Jendela undangan tujuh hari dipilih di luar ADR** | [ADR-0005](adr/0005-autentikasi-sesi-cookie.md) menetapkan jendela sesi dan diam soal undangan. Cukup panjang untuk bertahan melewati undangan Jumat sore atau seminggu cuti, cukup pendek supaya tautan yang terlanjur diteruskan ke grup obrolan berhenti bekerja. **Ditandai di kodenya** sebagai angka yang belum disepakati | belum — dipilih sendiri, menunggu jawaban pemilik |
| 2026-08-08 | **Undang ulang menutup tautan yang sudah terkirim** (`ExpireOpenInvitationsForEmail`) | Alternatifnya dua tautan hidup untuk satu alamat, dan alamat yang tidak sengaja diundang dua kali menyisakan pembuat akun cadangan di surel lama. Tidak ada di rencana Fase 0 §18; muncul saat memutuskan apa arti mengundang orang yang sama dua kali | ya — lewat merge PR #83 |
| 2026-08-08 | Potongan f dipecah jadi **lima** PR (#82 token, #83 query, #84 pembuatan, lalu penukaran, lalu endpoint & perakitan) | Diputuskan sebelum baris pertamanya, kali ini benar-benar sebelum. Utuh, potongan ini jauh di atas 400 baris; masing-masing PR di atas berkisar 340–580 dengan mayoritas test | ya |
| 2026-08-08 | Potongan f jadi **enam** PR: yang terakhir dipecah lagi menjadi #87 (perakitan + `POST /invitations`) dan #88 (`POST /invitations/accept`) | Rencana lima PR menaruh endpoint, kontrak, `SMTP_*`, dan perakitan dalam satu PR, dan itu jauh di atas 400 baris. Dipecah **sebelum ditulis**, bukan sesudah — yang meleset ukurannya, bukan aturannya | ya |
| 2026-08-08 | **`SMTP_USERNAME` dan `SMTP_PASSWORD` wajib hanya saat `APP_ENV=production`**, bukan di semua environment seperti tertulis di [environments.md](environments.md) | Mailpit tidak menerima kredensial apa pun, jadi mewajibkannya di `local` hanya melahirkan isian asal-asalan — persis yang dilarang dokumen yang sama. Bentuknya sama dengan `APP_BASE_URL`: aturan yang hanya mengetat di tempat ia berarti. `environments.md` ikut dibetulkan | ya — keputusan pemilik, menutup pertanyaan yang terbuka sejak 2026-08-08 |
| 2026-08-08 | `AllowUnencrypted` pada pengirim SMTP diikat ke `APP_ENV`, bukan ke variabel sendiri | Alasan yang sama dengan atribut `Secure` pada cookie sesi: saklar terpisah adalah saklar yang bisa salah disetel di produksi, dan yang dipertaruhkan di sini adalah pesan, penerima, dan tautan resetnya menyeberang jaringan terbaca | ya — lewat merge PR #87 |
| 2026-08-08 | `CreateUser` mengembalikan `timezone` walau tidak menuliskannya | Ia satu-satunya kolom di query itu yang dipilih database, dan penukaran undangan menyerahkan akun barunya langsung ke pemanggil. Baris yang dibaca kembali dengan timezone kosong adalah query yang melaporkan nilai yang bukan yang tersimpan | ya — lewat merge PR #86 |
| 2026-08-08 | **Menukar undangan langsung memulai sesi**, bukan mengarahkan ke halaman login | Orang yang baru saja membuktikan ia memegang tautannya dan memilih sandinya sendiri sudah melakukan semua yang diminta halaman login. Menyuruhnya mengetik ulang sandi yang ia setel sepuluh detik lalu tidak melindungi siapa pun. Dinyatakan di kontrak, bukan hanya di kode | ya — lewat merge PR #88 |
| 2026-08-08 | Batas undangan (20/jam, 100/hari per pengundang) dan batas penukaran (10/10 menit, 50/jam per alamat) dipilih di luar ADR | [ADR-0005](adr/0005-autentikasi-sesi-cookie.md) §131 mewajibkan adanya batas dan diam soal angkanya, dan ADR-0010 hanya punya baris untuk login. Yang mengundang hanya owner, jadi 20/jam longgar untuk onboarding satu tim sekaligus ketat terhadap akun owner yang diambil alih. Penukaran **gagal tertutup**; pengundangan **gagal terbuka** — owner yang tidak bisa mengundang siapa pun karena Redis mati adalah pertukaran yang terbalik | belum — dipilih sendiri, menunggu jawaban pemilik |
| 2026-08-08 | Potongan e dipecah jadi tiga PR (#78 render, #79 penangkap, #80 SMTP) | Utuh, ia 1.427 baris. Pemecahannya diputuskan setelah render dan penangkap ditulis, bukan sebelumnya — pelanggaran aturan "potong sebelum menulis" yang tidak berbiaya kali ini hanya karena ketiga berkas test-nya sudah berdiri sendiri. #78 tetap 561 baris dan #80 tetap 626, keduanya di atas target 400; memecahnya lebih jauh menyisakan validator atau separuh percakapan SMTP yang tidak dipanggil siapa pun | ya |
| 2026-08-08 | **`internal/mail` mendarat tanpa satu pun pemanggil**, dan `SMTP_*` sengaja belum dibaca `internal/config` | Merakit pengirim di `cmd/api` sebelum ada yang mengirim adalah kode mati (`rules/00-core.md` §8), dan mewajibkan variabel environment yang tidak dibaca siapa pun adalah yang dilarang [environments.md](environments.md) dan komentar di kepala `internal/config`. Pola yang sama dengan `authz.Memberships` dan `httpx.Limiter`. Konsumen pertamanya potongan f | ya |
| 2026-08-08 | Baris `internal/mail/` ditambahkan ke pohon paket di [architecture.md](architecture.md) | Preseden `internal/redis` pada 2026-08-07: pohon itu peta yang dibaca orang sebelum memutuskan di mana kode baru ditaruh, dan paket yang hilang darinya mengundang paket kedua di sebelahnya | ya — lewat merge PR #80 |
| 2026-08-08 | Potongan d dipecah jadi dua PR (#75 query & kebijakan, #76 endpoint & kontrak) | Utuh, ia lebih dari 1.100 baris. #75 sendiri 676 baris — di atas target 400 — tapi 117 di antaranya keluaran `sqlc generate` dan ~380 test; memecah SQL dari service akan meninggalkan query yang tidak dipanggil siapa pun | ya |
| 2026-08-08 | **Angka ember jaringan (300 / 10 menit, 2000 / hari) dipilih di luar ADR-0010** | Tabel ADR-0010 hanya punya baris per akun dan per IP, sedangkan potongan c meminta tiga ember. Angkanya sepuluh kali jatah per-alamat pada jendela yang sama: cukup longgar sampai satu kantor atau kampus di belakang satu /24 tidak menyentuhnya, cukup ketat sampai sebaran yang menjaga tiap alamat di bawah 30 tetap tertangkap. **Ditandai di kodenya sebagai angka yang belum disepakati** | belum — dipilih sendiri, menunggu jawaban pemilik |
| 2026-08-08 | Potongan c dipecah jadi empat PR (#69 primitif, #71 kunci jaringan, #72 kebijakan, #73 pemasangan) | Alasan yang sama dengan Langkah 13–16. Utuh, ia akan jauh di atas 400 baris | ya |
| 2026-08-08 | `httpx.writeRateLimited` di-export jadi `WriteRateLimited` | Endpoint login menolak dari dalam handler-nya, bukan dari middleware, karena ADR-0010 menghitung percobaan yang **gagal** dan itu baru diketahui setelah password diperiksa. Ia tetap harus menghasilkan status, body, dan pembulatan `Retry-After` yang sama dengan yang ditolak middleware; satu fungsi bersama yang menjaminnya | ya — lewat merge PR #73 |
| 2026-08-08 | Kunci ember akun di-hash sebelum masuk Redis | Bukan kerahasiaan — alamat surel bisa ditebak. Ia menjaga daftar siapa saja yang mencoba masuk tidak tersimpan di datastore yang tidak berkepentingan memegangnya (`rules/45-privacy`), dan tidak ada apa pun yang perlu membacanya kembali. Sekaligus membatasi panjang kunci | ya — lewat merge PR #72 |
| 2026-08-08 | Potongan b dipecah jadi dua PR (#67 menggeser, #68 berlapis) | Alasan yang sama dengan Langkah 13–16. Potongannya diputuskan sebelum PR pertama dibuat | ya |
| 2026-08-08 | **Penghitung kegagalan jadi tipe tersendiri (`redis.FailureCounter`), bukan metode tambahan pada `SlidingWindow`** | ADR-0010 menghitung *kegagalan*, dan jawaban "apakah percobaan ini gagal" baru ada setelah password diverifikasi — jauh setelah titik di mana pemanggil harus ditolak. `Allow` yang memeriksa dan mencatat sekaligus tidak bisa menyatakan itu. Menyatukan keduanya berarti menghitung login yang berhasil, dan di satu kantor di belakang satu NAT itu mengunci seluruh kantor di jam tersibuk — persis yang dilarang ADR-0010 | ya — lewat merge PR #69, yang deskripsinya menjelaskan pilihan ini |
| 2026-08-08 | Konsekuensi pilihan di atas diterima secara sadar: antara `Check` dan `Record` ada jendela tempat ledakan percobaan bersamaan bisa lolos | Jendelanya selebar jumlah percobaan yang muat dalam satu verifikasi Argon2id (ADR-0005) — ongkos yang memang ada untuk membuat penebakan mahal. Menutupnya lebih rapat menuntut penghitungan keberhasilan atau mekanisme pengembalian jatah, dan yang pertama adalah penguncian kantor di atas | ya — lewat merge PR #69 |
| 2026-08-07 | `golang.org/x/crypto` jadi dependency runtime langsung | Argon2id diwajibkan ADR-0005 dan `rules/40-security.md`, dan pustaka standar Go tidak punya implementasinya. Turunannya `golang.org/x/sys` sudah ada di `go.sum`. `govulncheck` bersih | ya — ditanyakan dan disetujui pemilik pada 2026-08-07 |

## Cara memperbarui berkas ini

Rekonsiliasi dilakukan di akhir setiap fase, atau minimal mingguan:

1. Daftar PR yang ter-merge sejak `Terakhir direkonsiliasi`
2. Untuk setiap `selesai`: benarkah kodenya ada dan gerbangnya hijau?
3. Untuk setiap `sedang` yang tidak bergerak: tulis penghalangnya
4. Cari pekerjaan di repo yang tidak ada di daftar — itu pelebaran cakupan
5. Perbarui tanggal dan commit rekonsiliasi

Item yang ternyata belum benar-benar selesai **dikembalikan statusnya**.
