# Customer placement on B200

`cube-relocate` is a root-only, journaled operator primitive for the deployed
Cube controller. It does not expose a tenant migration endpoint. Run the
coordinator under the existing four deployment/operator locks.

The source keeps its app ID, sandbox ID, ownership, configuration and task
history. The coordinator starts an idle source, quiesces all owner writers,
exports workspace/home/history locally, pauses it, then inserts an admission
quarantine. The destination obtains encrypted snapshots directly from S3.
Only bounded jobs and verification receipts cross the management link.

The worker compares paths, modes and file hashes before starting the restored
application. Fresh templates may contain the reviewed Debian `.bash_logout`
file or an empty `.cache/` absent from older migrated homes: their exact hashes,
sizes and modes are checked separately. Cache children are never excluded.
Every source entry must still match, and unexpected paths fail the
comparison. Reviewed home manifests reject unknown files; fresh Cube homes use
the strict template manifest. Source exports are never silently reused.

A temporary deny-all egress channel allows the restored web process to start
before it has the canonical binding. That channel closes before commit. Commit
atomically switches the binding and admission ownership; the normal controller
then attaches the application's regular egress policy. Configuration, credentials,
ownership and task fingerprints must still match the fenced source. The old VM
remains paused and quarantined for recovery review. No source deletion occurs.

`relocate-idle-projects.py` checks canonical preview and pause/wake, then returns
an originally idle project to idle on B200. Four moves can run concurrently, with
two source exports and one native creation at a time. An export failure resumes
the unchanged source. A failure after fencing stops the cohort for reconciliation;
it does not reopen an uncertain source or release an uncertain reservation.
`complete-relocation.py` can repeat verification of an already restored target;
it never recreates or reimports it. A durable create/import intent prevents blind
retries. Receipts, URLs, keys, application environment and archives remain private.
An explicit `--keep-vps=<sandbox-id>` leaves a project assigned to VPS while
`--remaining` selects the idle customer cohort under the shared locks. The live
capacity acceptance includes customer web projects on both workers. Minecraft
tunnels are excluded from HTTP acceptance per the user's prioritization.
The acceptance temporarily pauses an already running tunnel only if it has no
active task, journals it, and restores its original running state after test
cleanup. Every slot at the measured peak must serve a verified HTTP page.

`proxy-compression.py` stages and syntax-checks the modern JavaScript gzip MIME
types on the pinned B200 proxy. Applying it requires the same operator locks;
it preserves the mounted configuration inode and gracefully reloads Nginx.
The local measurement compares decoded hashes and reports wire sizes without
carrying asset contents over the management link. Configuration and credential
inputs remain private. The B200 change is now live: decoded hashes match and
one measured module shrank from 1,364,318 to 213,380 bytes on the wire.

Validation includes transaction/race tests, a disposable same-ID live move, and
per-customer file/preview/wake evidence. `accept-live-capacity.py` separately
measures simultaneously serving migrated projects plus private filler pages.
It does not claim capacity for 100 saturated builds or replace the earlier
failed 100-copy report. Its third live serving run passed at 96 B200 + 4 VPS;
see `results/2026-09-28-capacity-ready/README.md` for the evidence and separately
verified cleanup.

This is controlled relocation and sticky local execution. Automatic continuous
S3 checkpoints, dependency reconstruction, worker-failure takeover and reboot
recovery remain separate work. Migration snapshots temporarily include installed
files for exact restoration; they are not the permanent source-only store.
