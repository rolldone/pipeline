Real test pipeline

How to run

1. Ensure you are in the repo root.
2. Build the binary (if not already):

   go build -o pipeline ./

3. Run locally:

   ./pipeline run real-test-pipeline

Notes
- This pipeline uses `file_transfer` in local mode and copies files from `real_test/src` to `/home/donny/real_test_target`.
- `delete_policy: soft` is currently a no-op in the codebase (soft delete not implemented). Running this pipeline will not remove extra files in the target.
