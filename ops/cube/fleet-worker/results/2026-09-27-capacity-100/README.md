# 100-copy attempt — failed acceptance, cleaned up

The requested 100 simultaneous sandboxes were **not proven**. The fleet remains
configured for 96 B200 + 4 VPS, but configuration is not a capacity result.
The earlier 50-sandbox acceptance remains the largest completed acceptance test.

The input was 74 customer snapshots plus 26 fillers, all in separate operator
apps. The original direct worker-to-worker transfer was stopped. All 74 snapshots
were uploaded as encrypted temporary S3 objects. B200 fetched its inputs directly
from S3 through its own host network; imports and verification ran through the
worker's local proxy. The operator broker carried bounded metadata only.
Successful imports compared file paths, modes and content hashes. A cached
94,344,371-byte snapshot took 24.65 seconds to import and verify, with successful
private preview/assets and rejected unsigned access. This is a test restore
measurement, not a production failover latency guarantee.

The attempt encountered a Cubelet initialization deadline, a pending charged
creation reservation, and subsequent recovery/lookup errors. It did not show
physical CPU, RAM or disk exhaustion. A separate harness error resumed a plan
after cleanup had already removed some fixtures; those later 404s must not be
treated as independent capacity evidence. No 100-running peak was reached and
the planned customer traffic fence was never entered.

Across 379 host samples during this incomplete attempt, the B200 worker peaked
at 14.322 CPU cores and 97.16 GiB RAM. The inference container peaked at 3.845
CPU cores and 79.07 GiB RAM. These peaks are not measurements at 100 simultaneous
healthy sandboxes. The worker has no GPU access.

Final verification on 2026-09-27 at approximately 20:33 UTC:

- All 100 operator app records removed, test native runtimes absent, and no
  charged test admission reservations remain.
- All 74 customer runtime bindings unchanged; production readiness passes.
- B200 native inventory and Cubelet containerd task list empty after cleanup.
- Temporary worker test cache and all 74 VPS source snapshot archives removed.
- Temporary WireGuard route/congestion/qdisc tuning reverted.
- **S3 cleanup incomplete:** all 74 version-specific deletes returned HTTP 403
  `AccessDenied` with the existing production credentials. The encrypted object
  versions and private cleanup receipts remain; deleting a marker would not
  prove deletion of the stored versions. An identity permitted to delete these
  exact versions is needed to finish object cleanup.

Private evidence on VPS:
`/opt/baarcha-bench/cube-fleet-20260927/capacity-100/`, particularly
`direct-copy-stopped.json`, `s3-local-canary.json`, `native-cleanup-results.json`,
`cleanup.json`, `final-cleanup/complete.json`, `post-cleanup.json`, and
`s3-cleanup.json`. Do not publish receipts, presigned URLs, encryption keys,
customer snapshots, or the SQLite backups.

Before another attempt: fix/reconcile failed creation without stranding admission,
verify recovery under concurrent creation, and reject resuming a fixture plan
after cleanup starts. Use fresh isolated fixture IDs. Sticky production placement
already exists; automatic S3 checkpoints, local dependency preparation, exclusive
writer ownership and routing takeover are not yet fully integrated or accepted.
