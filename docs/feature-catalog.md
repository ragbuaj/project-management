# Katalog Fitur

ID di dokumen ini dirujuk oleh [roadmap.md](roadmap.md). Kolom **Fase**
menunjukkan kapan fitur dikerjakan.

Effort untuk satu orang: **S** = di bawah sehari · **M** = 1–3 hari ·
**L** = seminggu atau lebih.

## A. Fondasi

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| A1 | Board / Project — wadah teratas, bisa banyak | S | 1 |
| A2 | Column — kolom dalam board, bisa diurutkan | S | 1 |
| A3 | Card — judul, deskripsi, posisi dalam kolom | S | 1 |
| A4 | Drag & drop antar kolom dan urutkan | M | 1 |
| A5 | Login | S | 1 |

## B. Detail kartu

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| B1 | Deskripsi Markdown dengan preview | S | 1 |
| B2 | Label berwarna | S | 1 |
| B3 | Due date + penanda terlambat | S | 1 |
| B4 | Checklist / subtask dengan progress bar | M | 3 |
| B5 | Prioritas (low / medium / high / urgent) | S | 1 |
| B6 | ~~Attachment / unggah berkas~~ | — | **non-goal** |
| B7 | ~~Cover image kartu~~ | — | **non-goal** |
| B8 | Komentar | S | 2 |
| B9 | Activity log per kartu | M | 2 |
| B10 | Relasi antar kartu (blocks / relates / duplicates) | M | 3 |
| B11 | ~~Custom field per board~~ | — | **non-goal** |

## C. Cara melihat data

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| C1 | Kanban view | — | 1 |
| C2 | Tampilan tabel dengan sort & filter kolom | M | 3 |
| C3 | Calendar view berdasarkan due date | M | 5 |
| C4 | Timeline / Gantt — butuh start date + relasi (B10) | L | 5 |
| C5 | Filter & pencarian | M | 3 |
| C6 | Saved filter | M | 4 |
| C7 | My Tasks — lintas project | S | 3 |

## D. Perencanaan agile

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| D1 | Backlog terpisah dari board aktif | M | 4 |
| D2 | Sprint — mulai, tutup, pindahkan sisa | M | 4 |
| D3 | Story point / estimasi | S | 4 |
| D4 | Epic | M | 4 |
| D5 | Burndown chart — butuh D2 + D3 | M | 5 |
| D6 | Velocity chart — butuh D2 + D3 | S | 5 |
| D7 | Cumulative flow diagram | M | 5 |

## E. Otomatisasi & alur kerja

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| E1 | WIP limit per kolom | S | 7 |
| E2 | Aturan otomatis (pemicu → syarat → aksi) | L | 7 |
| E3 | Kartu berulang (recurring) | M | 7 |
| E4 | Template kartu dan board | M | 7 |
| E5 | Workflow dengan transisi yang dibatasi | L | 7 |

## F. Waktu & pelaporan

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| F1 | Timer mulai/stop per kartu | M | 6 |
| F2 | Log waktu manual | S | 6 |
| F3 | Laporan waktu per project dan label | M | 6 |
| F4 | Dashboard ringkasan | M | 5 |

## G. Multi-user

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| G1 | Beberapa pengguna + assign kartu | M | 2 |
| G2 | Peran per project (admin / member / viewer) | M | 2 |
| G3 | Mention `@user` di komentar | S | 2 |
| G4 | Notifikasi in-app | M | 2 |
| G5 | Sinkronisasi realtime lewat WebSocket | L | 8 |

## H. Integrasi & luar aplikasi

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| H1 | Keyboard shortcut | M | 3 |
| H2 | Quick capture — tambah kartu dari satu kotak teks | S | 1 |
| H3 | Notifikasi Telegram (bot) untuk due date & mention | M | 8 |
| H4 | Integrasi GitHub — kartu terhubung ke issue/PR | L | 9 |
| H4b | Integrasi GitLab — issue/MR, lewat abstraksi yang sama | M | 9 |
| H5 | Impor dari Trello / CSV | M | 9 |
| H6 | Ekspor / backup JSON penuh | S | 1 |
| H7 | Share link read-only | M | 9 |
| H8 | API publik + token | M | 9 |
| H9 | PWA / mode offline | L | 10 |

## I. Kenyamanan

| ID | Fitur | Effort | Fase |
|---|---|---|---|
| I1 | Dark mode | S | 1 |
| I2 | Archive & restore | S | 1 |
| I3 | Trash dengan undo | M | 2 |
| I4 | Command palette (Ctrl/Cmd+K) | M | 3 |
| I5 | Riwayat global / audit trail | M | 2 |
| I6 | Bulk action | M | 3 |

## Non-goals

Alasan lengkap ada di [product-brief.md](product-brief.md#non-goals).

B6 attachment · B7 cover image · B11 custom field · multi-tenancy komersial ·
billing · pendaftaran mandiri · editing kolaboratif CRDT · aplikasi mobile
native · pembuat laporan generik · SSO/SAML/LDAP · i18n
