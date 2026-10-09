# VPS capacity and recovery operation — 8 October 2026

This directory records the reviewed operation against the existing Baarcha VPS.
The scripts pin incident-specific paths, image hashes, runtime identities and
baseline configurations. They are not a general installer. Exclusive journals
prevent blindly repeating disk changes, allocation or restore operations.
Private archives, keys, database copies and authentication receipts stay on the
VPS; they are excluded from this repository.

## Verified results

- Worker: 48 GiB RAM, 12 virtual CPUs; XFS data disk grown to 672 GiB with discard.
- Admission: 45 GiB memory budget (including 128 MiB VM overhead per runtime), 50 runtime slots and two dedicated builder-VM
  slots. Builder-VM slots are independent of coding-task concurrency.
- Density: 50 temporary starter sandboxes plus one customer sandbox served
  1,920 checks without failure; page p95 was 1.265 seconds. Combined guest PSS
  was 6.424 GiB. One and two concurrent Vite builds also passed.
- The later 50-build burst stopped on the host memory-pressure guard. It did
  not establish maximum build or coding-agent concurrency. All fixtures were
  removed and quota restored. Model/agent load tests are explicitly deferred.
- Discard reclaimed 103.34 GiB of physical storage. Verified MySQL backup and
  binlog archival reclaimed 54.70 GiB inside the worker. Daily MySQL backups
  have a 14-day retention policy with at least two complete backups retained.
- Controller `7745f34` is deployed. Its durable coding queue remains disabled
  (`SANDBOXD_CUBE_TASK_CONCURRENCY=0`). Tests use local mocks; no live agent load
  test is implied by queue acceptance.
- Derja is restored on the VPS and passed browser away/back navigation.
- React restore canary `01M16MV7KZSF3YNAJ1VKWKYED5` passed original workspace,
  home, history and configuration checks, reinstalled 202 dependencies, and
  passed stop/wake in 1.063 seconds. Its original runtime remains retained.

## Source-only recovery

`prepare-recovery*.py` converts the existing verified backups into scoped
workspace/home/history archives. `validate-recovery-artifacts.go` checks the
actual guest import contract before any runtime allocation.

`canonicalize-recovery-zip.py` preserves original archives and creates separate
ZIPs with Go-compatible DOS read-only flags. It checks every filename, Unix
mode and byte sequence. The flags do not change the restored Unix permissions.

`restore-from-backup.py` fences one stopped source, allocates a VPS target,
imports and verifies content, applies configuration, checks HTTP readiness,
commits the binding with compare-and-swap, and checks stop/wake. The recovery
channel permits only `registry.npmjs.org:443`; private addresses, protected
management services, other domains and model providers remain excluded.
It never contacts or deletes the original B200 runtime.

`restore-vite-batch.py` selects only the reviewed Vite startup command, restores
sequentially, and stops on the first failure. Failed journals require explicit
reconciliation. `resume-react-restore.py` and `complete-react-restore.py` record
the one-time canary reconciliation, including verification of the partial PNPM
directories created during its first blocked install.

`prepare-nos-recovery.py` follows NOS's prior completed recovery journal to the
exact verified artifacts, checking their hashes and unchanged task history.
This avoids adopting the older runtime identity from the initial backup.

## Outstanding acceptance

The first 133-project archive set is stored on the VPS. Source backups and
offline archive validation do not mean all projects have been started there.
Remaining B200 bindings require individual verified restoration. Some archives
need special handling for app authentication state, external cache links or
missing historical events; preserve the originals and do not fabricate data.

Recurring VPS source backups are installed and a first generation was verified.
The union of that generation and the emergency archive covers all 134 current
projects. Maximum build/agent concurrency and all-project production serving
still require further acceptance. Read the
latest append-only progress entries in `WORK.txt` and the private VPS journals
before continuing this operation.

## Real application memory and concurrent starts

The first real-application density run exposed an esbuild OOM at 512 MiB.
The reviewed Vite default is now 768 MiB. An app that exhausted that profile
passed full module serving at 1 GiB. Migration runners can promote a target to
1 GiB, then 2 GiB, only after proving guest OOM before routing changes; the
unused target is discarded through the guarded CLI and source data is kept.
These profiles are limits, not measured resident usage.

`parallel-migrations.py` runs two independent archive transfers with the parent
operator locks continuously inherited by both children, including profile
promotion via exec. `migration_lifecycle.py` serializes their short provider
mutations to respect native creation concurrency and the pending-create ledger.

Native Cubelet concurrency rejection `130513` with the exact create-flow busy
message occurs before workflow steps run. Controller `7745f34` retries only
that explicit rejection with bounded jittered backoff and a single admission
lease. Transport errors, ambiguous responses and other errors are not replayed.
Exhausted safe rejections release the lease only after a fresh verified paused
observation. The deployment reconciled the one already-stranded lease with the
controller drained and stopped, using the offline recovery CLI.

Race tests passed for cube, store and API packages. Production verification ran
two simultaneous real starts in three rounds: six successful starts in
1.3–2.9 seconds, no pending admissions, original stopped states restored.
See `concurrent-resume-7745f34.json` and `resume-retry-deployed.json`.

## Maintenance and account allowances

Some non-admin Free accounts exhausted their allowance during operator checks.
The independent account worker then correctly stopped their canonical sources,
interrupting exports. `maintenance-account.mjs` now takes the platform's exact
Postgres account locks for the explicitly reviewed maintenance sandboxes. It
checkpoints prior usage normally, then advances only those sandboxes' counters
without charging operator time. It never changes plans or deletes prior usage.
An EOF releases the guard; other accounts continue normal enforcement.
`maintenance-account-verified.json` records real lock contention, release, and
unchanged daily usage for three stopped test accounts.

A verified, uncommitted target can reach its native sleep timeout while awaiting
operator review. `cube-relocate connect-target` verifies the retained source
binding, target metadata and fenced journal before admitted resume. Commit
checks the completed connect receipt and CASes the new active target admission
token. Unknown provider outcomes still require explicit reconciliation.
