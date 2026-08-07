# Dokumentasi

Peta seluruh dokumen. Semua dokumen tinggal di dalam repo dan di-review lewat
PR seperti kode. Dokumen yang berpindah keluar repo akan menjadi usang tanpa
ada yang menyadarinya.

## Gerbang sebelum baris kode pertama

Dokumen 1–7 harus selesai dan disetujui sebelum implementasi dimulai.

**Gerbang ini lewat pada 2026-08-06.** Implementasi Fase 0 boleh dimulai.

| # | Dokumen | Isi | Status |
|---|---|---|---|
| 1 | [product-brief.md](product-brief.md) | Masalah, pengguna, ruang lingkup, **non-goals** | ✅ disetujui |
| 2 | [glossary.md](glossary.md) | Istilah domain yang disepakati | ✅ disetujui |
| 3 | [architecture.md](architecture.md) | C4 level 1–2, struktur paket, alasan pemilihan | ✅ disetujui |
| 4 | [adr/](adr/) | Keputusan yang sulit dibalik | ✅ ADR-0001…0010 Accepted, kecuali 0007 Proposed |
| 5 | [data-model.md](data-model.md) | ERD, DDL, indeks, retensi, penandaan data pribadi | ✅ disetujui |
| 6 | [api/openapi.yaml](api/openapi.yaml) | Kontrak API — sumber kebenaran | ✅ disetujui |
| 7 | [authorization.md](authorization.md) | Peran × sumber daya × aksi | ✅ disetujui |

## Boleh menyusul selama Fase 1

| # | Dokumen | Isi | Status |
|---|---|---|---|
| 8 | [nfr.md](nfr.md) | Target performa dengan angka dan persentil | ✅ draf awal |
| 9 | [threat-model.md](threat-model.md) | STRIDE ringkas | ✅ draf awal |
| 10 | [environments.md](environments.md) | Variabel environment, secret, layanan eksternal | ✅ draf awal |
| 11 | observability.md | Pertanyaan saat insiden → log & metrik yang menjawabnya | belum ditulis |
| 12 | release.md | Urutan deploy, feature flag, jalur rollback | belum ditulis |
| 13 | runbook.md | Jalankan lokal, deploy, lihat log, kegagalan yang bisa diperkirakan | belum ditulis |

## Perencanaan

| Dokumen | Isi |
|---|---|
| [roadmap.md](roadmap.md) | Peta 11 fase, isi tiap fase, dan alasan urutannya |
| [feature-catalog.md](feature-catalog.md) | Daftar fitur beserta ID yang dirujuk roadmap |
| [progress.md](progress.md) | Progres nyata yang terekonsiliasi dengan repo — **mulai dari sini di sesi baru** |
| [sessions/](sessions/) | Log per sesi kerja, dengan bukti perintah |

## Aturan pemeliharaan

- Perubahan kontrak API dan perubahan implementasinya berada di **PR yang sama**.
- ADR yang sudah `Accepted` **tidak pernah diedit**. Kalau keputusannya berubah,
  buat ADR baru dan ubah status yang lama menjadi `Superseded by ADR-00XX`.
- Diagram ditulis sebagai teks (Mermaid) supaya perubahannya terbaca di diff.
- Setiap baris di [authorization.md](authorization.md) harus punya test yang
  membuktikannya.
