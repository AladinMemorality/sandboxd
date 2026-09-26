# Read-only preflight HTTP helper correction

Actual installed preflight found that the `http()` function shadowed the imported
`http` package. The helper failed before routing, service, or lifecycle effects.
The import now uses `http_client`; a disposable loopback HTTP server test exercises
the real helper's successful request and its HTTP503 refusal.

All 92 lifecycle tests passed on Linux as root (no skips). This changes only the
boot helper among installed artifacts:
`1d0cb0f5750b295b30c7e820987cee8ec166d23c215d57ab45bc3d2a4baf0cca`.
The native start binary and other Python helper hashes remain unchanged. The
previous candidate and its test receipts are retained in the parent directory.
No live worker operations were performed by these tests.
