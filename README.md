# Project Management Tool

Alat manajemen proyek untuk dipakai sendiri dan bersama beberapa rekan. Kanban,
sprint, otomatisasi, pencatatan waktu, dan integrasi GitHub/GitLab — di server
sendiri, dengan data yang bisa diekspor penuh kapan saja.

**Status:** Fase 0 (fondasi). Belum ada fitur produk.

## Dokumentasi

Seluruh keputusan ada di [`docs/`](docs/README.md) dan di-review lewat PR
seperti kode. Mulai dari sana, bukan dari kode.

| Kalau Anda mau tahu | Baca |
|---|---|
| Apa yang dibangun, dan apa yang **sengaja tidak** | [product-brief.md](docs/product-brief.md) |
| Urutan pengerjaan dan alasannya | [roadmap.md](docs/roadmap.md) |
| Bentuk sistem | [architecture.md](docs/architecture.md) |
| Kenapa stack-nya begini | [adr/](docs/adr/) |
| Skema database | [data-model.md](docs/data-model.md) |
| Siapa boleh melakukan apa | [authorization.md](docs/authorization.md) |
| Nama yang dipakai — dan yang dilarang | [glossary.md](docs/glossary.md) |
| Cara menjalankan lokal | [environments.md](docs/environments.md) |

## Struktur

```
backend/     Go — API, worker, migration, seed
frontend/    React + Vite (mulai Fase 0 PR 8)
deploy/      Docker Compose, Caddy (mulai Fase 0 PR 3)
docs/        Seluruh dokumen keputusan
```

## Gerbang otomatis

Aturan yang hanya tertulis bergantung pada ingatan. Yang dipasang sebagai hook
atau job CI tidak bisa dilupakan.

```bash
lefthook install
```

| Kapan | Yang diperiksa |
|---|---|
| `pre-commit` | gitleaks, format Go, golangci-lint, squawk pada migration |
| `pre-push` | `go test -race`, govulncheck |
| CI | Semua di atas, plus oasdiff saat kontrak API tersentuh |

`--no-verify` ada untuk keadaan darurat. Setiap pemakaiannya dinyatakan di PR,
bukan jadi kebiasaan — yang di lokal bisa dilewati, yang di CI tidak.

Gerbang di sisi GitHub sendiri — secret scanning, push protection, code
scanning — **tidak tersedia** untuk repositori pribadi yang privat. Apa yang
kita pakai sebagai gantinya, dan apa risiko yang tersisa, ada di
[environments.md](docs/environments.md#batas-platform-github).

## Lisensi

Belum ditentukan. Repositori privat.
