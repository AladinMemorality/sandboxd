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

Validation includes transaction/race tests, a disposable same-ID live move, and
per-customer file/preview/wake evidence. `accept-live-capacity.py` separately
measures simultaneously serving migrated projects plus private filler pages.
It does not claim capacity for 100 saturated builds or replace the earlier
failed 100-copy report unless its own live acceptance succeeds.

This is controlled relocation and sticky local execution. Automatic continuous
S3 checkpoints, dependency reconstruction, worker-failure takeover and reboot
recovery remain separate work. Migration snapshots temporarily include installed
files for exact restoration; they are not the permanent source-only store.
