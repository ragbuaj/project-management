---
type: session
project: project-management
phase: build
date: 2026-08-07
---

# Sesi: fracdex dan hashing password

Lanjutan dari [sesi sqlc dan httpx](2026-08-07-fase-0-sqlc-dan-httpx.md), yang
berhenti setelah Langkah 13. Sesi ini menyelesaikan Langkah 14 dan irisan
pertama Langkah 15.

## Dikerjakan

Empat PR ter-merge, semuanya menunggu CI hijau lebih dulu:

| PR | Isi | Commit |
|---|---|---|
| #26 | Format kunci fracdex + `Validate` | `89d757c` |
| #27 | `Between` + aritmetika integer + midpoint | `aff9c4e` |
| #28 | Property test, fuzz target, dua klaim panjang kunci | `90207d6` |
| #29 | Password Argon2id di `identity/domain` (Langkah 15a) | `81bd24c` |

Langkah 14 semula ditulis sebagai satu PR 637 baris. Dipecah tiga sebelum
dikirim, karena target `rules/50-git-workflow.md` adalah 400 baris dan pemilik
sudah dua kali menolak PR sebesar itu.

## Diverifikasi

Pada `81bd24c`:

```
$ go test ./... -count=1
ok  	internal/config                        1.037s
ok  	internal/fracdex                       1.250s
ok  	internal/httpx                         2.650s
ok  	internal/modules/identity/domain       1.081s
ok  	internal/modules/identity/repository    0.265s
ok  	internal/postgres                      0.274s
ok  	internal/redis                         2.331s

$ golangci-lint run ./...
0 issues.

$ go run golang.org/x/vuln/cmd/govulncheck@latest ./internal/modules/identity/...
No vulnerabilities found.
```

**Dua baris di atas menyesatkan kalau dibaca apa adanya.** `TEST_DATABASE_URL`
dan `TEST_REDIS_URL` tidak disetel di shell ini, jadi 53 test skema **skip**
dan tetap tampil `ok`. Yang benar-benar menjalankannya adalah CI, yang mulai
dari database kosong. Gerbang untuk PR sesi ini semuanya hijau di sana.

Fuzzer dijalankan di luar CI: **3,38 juta eksekusi dalam 30 detik** pada pohon
Langkah 14 yang final, tanpa sanggahan. Sebelum pemecahan PR, 1,27 juta
eksekusi lagi juga bersih. Corpus hasilnya ada di cache Go, tidak di repo.

Delapan mutasi sengaja dijalankan untuk membuktikan test-nya menggigit, bukan
sekadar hijau:

```
# pembulatan midpoint jadi ke bawah
Between("a0", "a0V") = "a0F", want "a0G"

# pintasan `stepped < next` dimatikan
Between("a0", "a2") = "a0V", want "a1"

# drop(a, shared) dikembalikan jadi a[shared:]
--- FAIL: .../a_fraction_that_runs_out_mid-comparison

# `after` tidak pernah menaikkan integer
longest key after 1000 appends is 202 bytes, want at most 3

# batas atas di Between dilepas
insertion 5 produced "a1", which does not sort below "a1"

# pemeriksaan panjang integer di split dilepas
panic: slice bounds out of range [:27] with length 25

# pemeriksaan versi argon2 dilepas
hash v=13 dilaporkan "password does not match", want ErrHashUnreadable

# batas parameter argon2 dilepas
baris p=255 dipatuhi, bukan ditolak
```

## Belum selesai

Langkah 15b (sesi), 15c (`/login`, `/logout`, `/me`), dan 16–26.

## Penghalang

Tidak ada.

## Berikutnya

Langkah 15b: menerbitkan, membaca, dan mencabut sesi. Tabel `sessions` sudah
ada sejak migration `00001`; yang belum ada adalah query-nya, penyimpanan
hash SHA-256 token, dan pembaruan geser masa idle.

## Keputusan yang diambil

- **`fracdex` memakai bagian integer berheader lebar, bukan hanya pecahan.**
  Tanpa itu setiap penambahan di ujung memanjangkan kunci satu digit. Dengan
  itu, 1000 penambahan tetap muat 3 byte — dan itu yang dikunci test.
- **Aritmetikanya dibuat identik dengan paket `fractional-indexing` klien web**,
  supaya kunci optimistis di browser sama persis dengan yang dihitung server.
  Tabel 18 vektor yang menguncinya.
- **Satu penyimpangan yang disengaja dari paket itu**: di sudut tempat
  penyisipan di depan menyentuh dasar ruang integer, paket JS mengembalikan
  string yang ditolak validatornya sendiri. `Between` membagi pecahan, supaya
  postcondition "keluaran selalu lolos `Validate`" bisa dinyatakan tanpa
  pengecualian. Sudut itu butuh 62^26 penyisipan di depan.
- **Pecahan tidak boleh berakhir digit nol, dan dasar ruang integer bukan
  kunci.** Keduanya ditegakkan `Validate`, bukan sekadar dijanjikan komentar.
- **Argon2id dengan 19 MiB, 2 iterasi, paralelisme 1** — konfigurasi kedua yang
  diterima OWASP.
- **Hash disimpan dalam format PHC**, jadi setiap baris membawa parameternya
  sendiri. Menaikkan biaya nanti tidak akan mengunci akun lama.
- **Hash yang tidak terbaca bukan password salah.** Dua kegagalan berbeda, dua
  sentinel berbeda, dan test yang memastikan keduanya tidak tertukar.
- **Parameter di luar batas ditolak, bukan dipatuhi.** `argon2.IDKey`
  mengalokasikan apa pun yang diminta baris database.
- **`golang.org/x/crypto` ditambahkan setelah ditanyakan ke pemilik**, sesuai
  `rules/00-core.md` §6.

## Yang gerbangnya tangkap di sesi ini

| Gerbang | Temuan |
|---|---|
| Menulis test | **Bug irisan di luar batas.** `midpoint` mengiris `a[shared:]` padahal `a` bisa lebih pendek dari prefix yang dibagi bersama — `panic`, bukan kunci yang salah. Ditemukan test penyisipan berulang, sebelum sempat di-commit |
| Membaca ulang tabel vektor | Setelah PR dipecah, tidak ada satu pun vektor yang menyentuh jalur `drop` itu. Dua vektor ditambahkan khusus untuknya |
| `misspell` | Ejaan Britania lagi: `neighbours`, `neighbouring`. Sama seperti sesi lalu |
| `gosec` G115 | Konversi `int → uint32` pada panjang digest. Batasnya sudah ada di `decode`, tapi gosec hanya melihat satu fungsi; dinyatakan ulang pada variabel yang benar-benar dikonversi, bukan didiamkan dengan `nolint` |
| `go mod tidy` | `go get` menaruh `x/crypto` sebagai `// indirect` sampai ada yang mengimpornya |
| Menghitung ulang `progress.md` | Ringkasan lama masih menulis 29 sub-langkah padahal pemecahan Langkah 15 menambah dua baris |

## Yang perlu diingat sesi berikutnya

- **`main` masih tanpa branch protection.** Urutan yang dipakai sesi ini:
  `gh pr checks <n> --watch`, baru `gh pr merge --squash`. Jangan `--auto`.
- **Ambang PR 400 baris itu nyata.** Tiga kali sesi ini pekerjaan dipecah
  sesudah ditulis, yang berarti menulis test dua kali untuk potongan berbeda.
  Lebih murah memutuskan potongannya sebelum menulis.
- `go test -race` tetap tidak bisa jalan lokal — detektornya menuntut cgo.
- Test skema butuh `TEST_DATABASE_URL`; test Redis butuh `TEST_REDIS_URL`.
  Tanpa keduanya test-nya **skip** dan hasilnya tetap terlihat hijau.
- Fuzz target hanya menjalankan seed corpus di CI. Menjalankannya sungguhan
  adalah pekerjaan manual: `-fuzz <nama> -fuzztime 30s`.
- Komentar kode Go **bahasa Inggris**, dan `misspell` menolak ejaan Britania.
- Pesan commit multi-baris di PowerShell 5.1: `git commit -F berkas`.

## Yang menunggu jawaban pemilik

Empat, semuanya ada di [progress.md](../progress.md). Yang baru: **panjang
password minimum**. Hashing membatasi maksimum 1 KiB karena itu soal keamanan;
minimum adalah keputusan produk dan belum tertulis di mana pun. Memblokir
Langkah 15c.

Pertanyaan Argon2id sudah terjawab di sesi ini: `golang.org/x/crypto`,
disetujui.
