Agent ignore precedence
=======================

This agent uses a deterministic ignore strategy when performing indexing.

Key points
- The agent reads ignore patterns from the uploaded config under either
  `pipeline.ignores` or `devsync.ignores` in `.sync_temp/config.json`.
- Patterns follow gitignore-style semantics (the controller uses `go-gitignore`),
  so negation (`!pattern`) is supported and later patterns override earlier ones
  (last-match-wins).
- If the uploaded config contains an `ignores` list, the agent treats that list
  as authoritative and will NOT cascade per-directory `.sync_ignore` files
  unless the uploaded config omits `ignores` and local `.sync_ignore` files are present.

This design ensures the controller can centrally control which files the agent
indexes (useful to avoid pulling agent artifacts, build directories, or other
generated files).

---

Config format (example)
-----------------------

The executor uploads a JSON config to `.sync_temp/config.json`. The agent
supports both an older `devsync` section and a newer `pipeline` section; the
agent prefers `pipeline` values but falls back to `devsync` for compatibility.

Minimal example produced by the executor:

```json
{
  "pipeline": {
    "working_dir": "./",
    "ignores": ["*.tmp", "!important.log"]
  }
}
```

Notes:
- `pipeline.working_dir` (or `devsync.working_dir`) is required for indexing: the
  agent will `chdir` to this path before building the index.
- `pipeline.ignores` is a list of gitignore-style patterns; negation (`!`) and
  directory globs are supported.

Controller integration
----------------------

- The controller/executor writes configuration to `.sync_temp/config.json` on the
  remote and then executes the agent (or asks a running agent to index). The
  executor normalizes `working_dir` in the config to avoid double-parenting when
  `.sync_temp` is nested (for example when the executor `cd`s into the parent
  directory before invoking `./.sync_temp/pipeline-agent`).
- When running `file_transfer`, the executor may build and deploy `pipeline-agent`
  programmatically if the binary is missing on the remote. If you run the agent
  manually, ensure the agent executable can read the `.sync_temp/config.json` file
  (see the location rules below).


# Dokumentasi Agent (ringkasan Bahasa Indonesia)

Dokumentasi singkat untuk binary agent yang dijalankan di remote (.sync_temp).

Tujuan
- Agent adalah binary ringan yang diupload oleh controller lokal dan dijalankan di remote untuk melakukan tugas seperti:
  - Membuat index file remote (sqlite DB) untuk operasi safe pull/push
  - Menjalankan file-watcher (mode `watch`)
  - Menampilkan konfigurasi lokal (mode `config`)

Perintah dan flag penting
- indexing
  - Perintah satu kali: `pipeline-agent indexing`
  - Agent akan membaca `.sync_temp/config.json` di working dir (atau di direktori executable jika berada di `.sync_temp`) dan `chdir` ke `devsync.working_dir` sebelum indexing.
  - Output: `.sync_temp/indexing_files.db` (SQLite) yang kemudian diunduh controller.

- --manual-transfer
  - Bentuk 1: `pipeline-agent indexing --manual-transfer` (tanpa nilai)
    - Agent akan membaca `devsync.manual_transfer` dari `.sync_temp/config.json` dan hanya mengindeks prefix yang tercantum di sana.
  - Bentuk 2: `pipeline-agent indexing --manual-transfer prefix1,prefix2`
    - Controller bisa mengirim daftar prefix (dipisah koma) agar agent mengindeks hanya prefix tersebut.
  - Jika flag diberikan tetapi tidak ada prefix yang ditemukan (baik flag kosong dan config kosong), agent akan memperingatkan dan fallback melakukan full indexing.

- --bypass-ignore
  - Jika dipasang, agent akan mengabaikan aturan `.sync_ignore` saat indexing.

Lokasi config & working dir
- Agent mencari `.sync_temp/config.json` di:
  1. Direktori tempat executable berada (jika executable berada di `.sync_temp`, ia akan baca di situ)
  2. `./.sync_temp/config.json` di working directory
- Field penting di config: `devsync.working_dir` (wajib untuk indexing), `devsync.manual_transfer` (opsional)

Behaviour terkait ignore dan manual_transfer
- Agent mempunyai mekanisme `SimpleIgnoreCache` yang membaca `.sync_temp/config.json` dan `.sync_ignore` di tree.
- Jika path termasuk `manual_transfer`, path tersebut diperlakukan sebagai "explicit endpoint" dan tidak diblokir oleh pola ignore (kecuali `--bypass-ignore` dipakai).

Dukungan OS
- Agent di-build per-target OS (binary `.exe` untuk Windows, tanpa ekstensi untuk Unix).
- Controller/deploy logic menyesuaikan path separator, quoting, chmod, dan cara menjalankan perintah (cmd.exe vs shell) saat mengeksekusi agent di remote.

Verifikasi cepat (non-kode)
1. Pastikan `.sync_temp/config.json` yang diupload controller memiliki `devsync.working_dir` benar.
2. Untuk percobaan: jalankan `pipeline-agent indexing --manual-transfer prefix1` pada remote (atau controller menjalankan perintah ini) dan lihat output yang dicetak agent.
3. Periksa file `.sync_temp/indexing_files.db` di remote (atau unduh) dan buka tabel `files` untuk memastikan entri yang diindeks sesuai prefix.
4. Jika ingin menonaktifkan ignore lokal saat testing, gunakan `--bypass-ignore`.

Catatan
- Agent tidak melakukan transfer file langsung untuk operasi sync — ia hanya membuat index (DB) dan menyediakan hashing untuk operasi download/upload yang dikontrol oleh controller.
- Pastikan versi agent dan schema DB saling kompatibel antara controller dan agent untuk menghindari masalah parsing DB.

---
