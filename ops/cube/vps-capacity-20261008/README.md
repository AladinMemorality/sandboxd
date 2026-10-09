# VPS capacity and recovery operation — 8 October 2026

This directory records the reviewed operation against the existing Baarcha VPS.
The scripts pin incident-specific paths, image hashes, runtime identities and
baseline configurations. They are not a general installer. Exclusive journals
prevent blindly repeating disk changes, allocation or restore operations.
Private archives, keys, database copies and authentication receipts stay on the
VPS; they are excluded from this repository.

## Verified results

- Worker: 48 GiB RAM, 12 virtual CPUs; XFS data disk grown to 784 GiB with discard.
- Admission: 45 GiB memory budget (including 128 MiB VM overhead per runtime), 50 runtime slots and two dedicated builder-VM
  slots. Builder-VM slots are independent of coding-task concurrency.
- Initial density: 50 temporary starter sandboxes plus one customer sandbox served
  1,920 checks without failure; page p95 was 1.265 seconds. Combined guest PSS
  was 6.424 GiB. One and two concurrent Vite builds also passed.
- The later 50-build burst stopped on the host memory-pressure guard. It did
  not establish maximum build or coding-agent concurrency. All fixtures were
  removed and quota restored. Model/agent load tests are explicitly deferred.
- Discard reclaimed 103.34 GiB of physical storage. Verified MySQL backup and
  binlog archival reclaimed 54.70 GiB inside the worker. Daily MySQL backups
  have a 14-day retention policy with at least two complete backups retained.
- Real application density subsequently passed with 49 reviewed 768 MiB apps
  and the preserved 2 GiB customer app: 600 homepage checks and 1,702 module
  checks, no new host or guest OOM, and original running states restored.
  Combined guest process PSS was 11.11 GiB (228 MiB mean). Internal HTTP p95
  was 15 ms; this measures preview serving, not browser rendering or agent work.
- Controller `eda67d2` is deployed. Its durable coding queue remains disabled
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

All 134 recovered sandboxes are placed on the VPS. Two additional user-created
VPS apps bring the reviewed inventory to 136, with no pending
admissions or fenced relocations. Each restore/profile change passed its
content and preview checks and a stop/wake cycle. Two unfinished applications
required separately recorded source repairs; original archives remain intact.
Current guest limits: 110 at 768 MiB, 11 at 1 GiB, and 15 at 2 GiB.

The previous emergency and recurring source backup generations cover all 134
projects in aggregate. A fresh 136-app all-VPS generation, the remaining
supervisor rollout, and validation of the remaining old checkpoints are pending.
The 50 real-preview density test has passed; see `results/real-preview-density-50-balanced-06`.
Maximum build/agent concurrency remains explicitly deferred. Read the latest
append-only entries in `WORK.txt` and the private VPS journals before continuing.

## Real application memory and concurrent starts

The first real-application density run exposed an esbuild OOM at 512 MiB.
The reviewed Vite default is now 768 MiB. An app that exhausted that profile
passed full module serving at 1 GiB. Migration runners can promote a target to
1 GiB, then 2 GiB, only after proving guest OOM before routing changes; the
unused target is discarded through the guarded CLI and source data is kept.
These profiles are limits, not measured resident usage.

`parallel-migrations.py` runs up to four independent archive transfers with the parent
operator locks continuously inherited by all children, including profile
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

## October 9 runtime recovery

All 49 original 512 MiB VPS profiles were replaced with verified fresh-state
768 MiB or 1 GiB runtimes. Production-bundle assets and safe local HTML redirects
are supported by the preview checks. Source runtimes and archives remain retained.

The owner-scoped Cube keepalive route is deployed. Migration maintenance renews
only its selected sandboxes, avoiding idle-reaper interruptions while retaining
normal account enforcement elsewhere. Two orphaned export sockets were repaired
with exact descriptor shutdown; no source process or data was replaced.

Supervisor `2c7e700` releases workspace locks before transmitting completed
workspace/home archives and bounds network reads/writes. Its isolated guest
regression tests and VPS stop/wake canary passed. New restores and the density
test install this static build. The updater now waits for its lock with a bound
and reports contention explicitly; parallel migrations no longer receive a
silent empty result.

The stored VM ceiling is 512 to retain recovery copies. Running slots remain 50,
RAM reservations 45 GiB including VM overhead, CPU reservations 9000m. This stored
limit is not a claim about 512 active VMs. See sanitized rollout receipts here.

The native 65% disk-use guard rejected a create before allocation after 112 apps
were restored. Verified request/operation evidence permits one bounded retry.
The VPS XFS data disk grew online from 672 to 728 GiB, preserving boot identity
and leaving over 128 GiB physical NVMe headroom even at full allocation.
The disk-use threshold and memory/CPU admission limits remain unchanged.

## Real application density: storage admission

The first 768 MiB application run warmed 41 stopped apps alongside one existing
2 GiB app (42 live). The next start received HTTP 503 before provider work.
Host/worker memory guards did not trip. Cleanup completed with canonical bindings
and the existing active runtime preserved. This run did not establish 50-app
capacity. Its immutable journal is `real-preview-density-50-balanced` on the VPS.

The storage ledger still held a 12 GiB grant for an acknowledged deleted, unbound
runtime whose native lookup returns 404. The tested controller fix reconciles
that grant only after matching identity/token/state and a fresh native 404.
The new density run uses a separate journal, checks observed free space against
all retained and proposed grants plus reserve/startup margin, and retains bounded
HTTP error responses privately. Storage thresholds are unchanged.

Cold September transfer archives passed metadata and checksum verification
on the VPS HDD. Original paths remain available through symlinks, and 64 GiB
was reclaimed on NVMe. The data disk grew online from 728 to 784 GiB with
its original inode, filesystem UUID and worker boot preserved, leaving over
128 GiB physical headroom even at full allocation. Controller `d5b07bb`
reconciled the stale grant through the offline CLI. See the deployment, cold
archive and disk-growth receipts. The sixth density run passed after recovering
four old checkpoints from verified source copies. The fresh backup and remaining
supervisor/wake checks are still pending; the runner stops on failure.

## Full memory snapshots and retained recovery sources

The VPS Cubelet now saves full guest memory on pause. The candidate was built
from the exact installed production source, preserving its embedded BPF objects,
and passed focused race tests. Deployment preserved all guest processes and
bindings. A canary passed four full pauses and three restores. This policy
applies to new pauses; it does not repair an older checkpoint before its first
successful wake. Source recovery is still an explicit, journaled operator action.

The controller now verifies native pause and released admission before reporting
stop success. Offline recovery validates the actual admitted CPU/memory profile,
including 768 MiB and 1 GiB, before accepting an observed create result.

The fleet validation reuses matching post-deployment density and recovery
evidence. Other stopped apps receive a wake, preview/module check, full pause,
second wake, and final stop. Existing running apps are checked without pausing
them. Failed journals are retained and require reviewed recovery before resume.
