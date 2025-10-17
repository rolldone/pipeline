Write File step - Design (brief)

Goal
- Add a `write_file` step that renders local files and uploads/writes them to targets (single or multiple files).
- Support templating, tolerant rendering (warnings + fallback), overwrite policies, and a simple `save_output` summary.

Contract (inputs / outputs)
- Inputs (Step fields):
  - `files`: array of FileEntry { source, destination, template?, perm?, owner?, group? }
  - `template`: optional step-level default ("enabled"|"disabled")
  - `overwrite`: "always" | "never" | "if-different" (default: if-different)
  - `skip_if_exists`: boolean (when overwrite==never, optionally skip instead of error)
  - `mode`: "local" | "remote" (remote uses SSH upload)
  - `save_output`: optional context variable name
- Output (saved JSON at `save_output`): simple structure:
  {
    "exit_code": 0,
    "reason": "success",
    "duration_seconds": 1,
    "files_written": ["/path/to/dst"],
    "render_warnings": [{"file":"templates/x.tmpl","error":"..."}]
  }

Behavior
- Expand files via existing `buildFileTransferEntries` (supports globs).
- For each entry:
  - Read source bytes.
  - If templating enabled and file is text (isTextBytes): render via `interpolateString`.
  - Attempt to write rendered bytes to a temp file in `e.tempDir` using `WriteFileFunc`.
  - On render/write error: fallback to original bytes and record a render warning.
  - Determine whether to write to destination depending on `overwrite` policy. If `if-different`, compute checksum of target (remote via SSH or local read) and compare.
  - Use atomic write: write temp file on target (or upload temp then rename) then `mv` to destination.
  - If perms/owner/group requested, attempt to apply (note: remote chown may require elevated privileges).
- Accumulate files_written and render_warnings; populate `save_output` JSON.

Edge cases
- Binary files are not templated; copied as-is.
- If target is Windows: use PowerShell commands or SMB (strategy chosen via OS detection).
- If OS detection fails: error out with clear message.

Testing plan
- Unit tests for rendering, fallback, overwrite policies, and checksum comparison.
- End-to-end test using injected `WriteFileFunc` and `ExecCommand` to simulate remote behavior.

Notes
- Keep `save_output` simple (only essential fields).
- Default `overwrite=if-different` recommended.


