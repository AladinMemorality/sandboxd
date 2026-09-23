# Existing-project migration and recovery

The branch includes an **offline operator CLI and tested migration state
machine**; production rollout remains gated. The current production
inventory requires per-project reviewed presets and owner-home manifests before
cutover. The CLI now transports explicitly selected owner-home data and streams
large app archives; these capabilities do not establish fleet readiness without
fresh offline preflight, disk-capacity checks and application validation. No
production container was stopped or copied in this work.

## What is preserved

`cube-migrate` retains the same sandbox ID, app/project association, preview port,
visibility, external identity, app configuration rows, and canonical task results.
The owner-private archive includes `.git`, `.env`, application databases, and
dependencies. It is never a publication/remix artifact. A fresh Cube supervisor
token is generated and sealed under the existing control-plane encryption key.

The Docker source remains stopped and retained. Private source and rollback ZIPs
are committed with fsync in a mode0700 directory with mode0600 files. Canonical
SHA256 manifests compare paths, modes, link targets and contents, independently
of ZIP timestamps/order/compression. Target configuration is acknowledged by the
authenticated supervisor before application readiness and provider cutover.

Task event history from before migration is replayed through the same API URL
from the retained Docker `.runtimed/tasks` directory. Each request rechecks owner,
sandbox, and the migration's durable task-ID inventory. Descriptor-relative
opens reject symlinks, hardlinks and special files; replay supports the existing
SSE cursor. Reading retained history does not wake a guest. A separate private
task archive copies only canonical, store-selected `events.jsonl` and terminal
`result.json` files. Checkpoint references already travel inside the app `.git`.
Imports verify the exact requested task-ID set and reject conflicting existing
history. Source and rollback history have separate durable checksums. Verified
history enables old checkpoint reversion in Cube; rollback reverse-copies new
Cube task history before changing provider. Earlier/incomplete history states
report `can_revert:false` with a reason and direct revert requests return409.
Raw agent logs, supervisor sockets and authentication files are not imported.

## Enforced maintenance scope

The lock-aware daemon holds a shared flock for its complete lifetime. The CLI
requires the same canonical database's exclusive flock and a protocol marker
written by a lock-aware daemon. A daemon refuses to start while an incomplete
migration journal exists. `inventory`, `fleet-preflight`, `status`, and `rollback-check` remain read-only and available
for diagnosis; they use SQLite `mode=ro` and do not apply schema migrations.

**First upgrade and run the lock-aware daemon, then stop it and disable automatic
restart before migration.** A legacy daemon does not implement the lock. The CLI
must run as native host root, outside Docker's PID namespace, and scans `/proc`
for other open database/WAL/SHM descriptors before opening SQLite and at each
state-machine checkpoint. Permission failures are fatal. This detects an already
running legacy process; it is not an atomic fence against a legacy binary being
started afterward. Do not downgrade or restart a legacy binary during maintenance.

Docker stop terminates all source container processes after its grace period,
including writers reached directly through Traefik. No coding task may be active
when migration begins. Cube's private guest quiescence suspends supervised app processes and stops
remaining UID1000 descendants, including detached sessions, using pidfds and
repeated process scans. Failure to fence writers refuses the operation; the
quiescence marker survives supervisor reexec. Every exported rollback tree is
verified twice. Tests exercise detached writers. The supervisor still shares
a UID with tenant code, so this is not a tamper-proof control boundary or a
worker-level disk snapshot against an actively compromised supervisor.

## State machine and crash recovery

| Journal phase | Durable state and next operation |
| --- | --- |
| `planned` | Original Docker metadata/config fingerprint retained; stop source |
| `quiesced` | Source stopped; write private app, reviewed home and selected-task archives |
| `archived` | Source checksum saved; persist fresh token and create Cube |
| `staging` | Creation may have happened; **never create another VM automatically** |
| `staged` | Remote identity and sealed credentials saved; import owner archive |
| `imported` | Reexec observed; verify manifest and apply configuration |
| `verified` | Content/config validated; resume app, verify readiness, pause VM |
| `complete` | Single SQLite transaction installed Cube binding/provider |
| `rollback_started` | Current Cube app data and selected task history quiesced/exported |
| `rollback_archived` | Current app/home/history archives/checksums durable; restore source |
| `rollback_restored` | Source copy verified; retain/rename original if config changed, pause target and switch provider |
| `rolled_back` | Same identity restored to stopped Docker; changed config requires normal wake recreation |
| `aborted` | Pre-cutover target removed and original Docker remains authoritative |

Side effects are replayable if a process dies before the journal acknowledges
them. Creation is the exception: the supervisor token is stored before the RPC,
and an uncertain response requires adoption of the existing tagged VM. Adoption
checks template/app/sandbox metadata and authenticates with the original token.
An unknown remote creation cannot be bypassed with `abort`.

Rollback always copies **current Cube data** back. It never merely changes the
provider pointer to stale Docker state. Source backup and target archive remain
available after rollback; the remote VM is paused, not deleted. Changed runtime
configuration selects guarded Docker recreation. Before target quiescence the
CLI validates current config, source ownership/mount and both Docker names. The
journal freezes the current config fingerprint; changes during recovery fail
closed. After reverse-copy verification the original stopped container is
renamed to `sandboxd-migration-source-SANDBOX_ID`, then the provider switch
clears its container ID. Normal wake reconstructs the canonical `s-SANDBOX_ID`
container through the existing `sandboxspec` and current `appenv` settings.

A successful CLI rollback with `pending_recreation:true` is **not application
acceptance**. Restart the control plane, perform normal wake, and verify actual
application readiness/config/data before accepting rollback. Under a later
maintenance window, `retire-source` removes only the original immutable container
ID after verifying a different running canonical container, current runtime
config and supervisor readiness. It preserves workspace and recovery archives;
`status` exposes the retained name and retirement acknowledgment. Do this before
eventual project purge so the retained original does not become an orphan.

## Commands

Build the Linux CLI from `control-plane`: `go build ./cmd/cube-migrate`. It uses
the same Cube API/proxy/domain/template and encryption-key environment settings
as sandboxd. The encryption key must already exist; it is never replaced.
Flags precede the action. Examples deliberately use placeholder paths and IDs:

```sh
# Read-only; works against the current pre-Cube database schema.
cube-migrate --database /DATA/state/sandboxd.db --workspaces /DATA/workspaces inventory
cube-migrate --database /DATA/state/sandboxd.db status
cube-migrate --database /DATA/state/sandboxd.db --workspaces /DATA/workspaces \
  --library /DATA/library --fleet-presets /PRIVATE/presets.json \
  --home-manifests /PRIVATE/home-manifests.json fleet-preflight

# Only during the enforced maintenance window, after reviewing eligibility.
cube-migrate --database /DATA/state/sandboxd.db --workspaces /DATA/workspaces \
  --archives /DATA/migration-archives --keyfile /DATA/secrets.key \
  --migrations /REVIEWED/migrations --sandbox SANDBOX_ID --preset react-vite \
  --home-manifests /PRIVATE/home-manifests.json --expected-fleet REVIEWED_SHA256 migrate

# Same flags/environment; recovery never guesses a replacement identity.
cube-migrate --database /DATA/state/sandboxd.db --sandbox SANDBOX_ID resume
cube-migrate --database /DATA/state/sandboxd.db --sandbox SANDBOX_ID \
  --adopt-runtime EXISTING_VM --traffic-token-file /PRIVATE/token adopt
cube-migrate --database /DATA/state/sandboxd.db --sandbox SANDBOX_ID rollback-check
cube-migrate --database /DATA/state/sandboxd.db --sandbox SANDBOX_ID rollback
cube-migrate --database /DATA/state/sandboxd.db --sandbox SANDBOX_ID abort
# After successful normal Docker wake/readiness, during a later maintenance window:
cube-migrate --database /DATA/state/sandboxd.db --sandbox SANDBOX_ID retire-source
```

Provide the same nondefault paths on every invocation. `rollback-check` checks
config/task eligibility without stopping the daemon; the engine rechecks those
predicates under maintenance before any target quiescence. It does not assert
that a quiescent target export will succeed. Archives and original workspaces require
backups and a separately reviewed retention policy. The CLI never purges them.
One journal is retained per sandbox; it deliberately refuses to overwrite an
aborted/rolled-back attempt or its recovery archives.

## Production inventory, 2026-09-23

The committed [metadata-only inventory](cube-pilot-results/production-inventory-2026-09-23.json)
was collected using [the read-only script](../scripts/inventory-cube-migration.py).
It reads SQLite metadata and filesystem stat information, not file contents,
project names, provider tokens, or decrypted configuration.

- 55 directory-backed sandboxes, all using port3000; no active coding tasks.
- Presets: react-vite, react-pro, nextjs, node-express, and legacy empty preset.
- All136 canonical tasks have both event and result artifact files. This is a
  metadata existence check; offline export validates their content and IDs.
- Approximately11.77GB of app regular files across the fleet; 32 sandboxes have
  hardlinked package files. Four app trees exceed256MiB before compression and
  the largest is approximately1.42GiB. Two contain individual files above128MiB.
- Home data includes agent history, shell/git configuration, package stores and
  custom tools/databases. Every project has home material requiring an explicit
  compatibility decision; some also have workspace siblings outside `app`.

The original v1 byte transport bounds were256MiB compressed,4GiB expanded,128MiB per file and200,000
entries. Inventory uses the shared runtime constants. Compressed size is only
known after export; inventory is preliminary eligibility, not proof of transfer.
Owner-aware export may flatten hardlinks only when descriptor-scoped inventory
proves every link belongs to the same owner home and the file belongs to UID1000.
Most absolute/external symlinks require an adapter. The private transport has
a narrow validated Python venv interpreter exception; inventory conservatively
flags absolute links for compatibility review. Unhandled home files block migration; caches/auth state are not
silently copied or discarded. The new streaming and reviewed-home channels below
address transport mechanics; each actual owner manifest still requires review.

## Historical owner-home compatibility inventory

The [home compatibility breakdown](cube-pilot-results/production-home-compatibility-2026-09-23.json)
is derived from the same read-only inventory. These groups overlap; they are
compatibility work categories, not permission to discard files.

| Home material outside the app | Sandboxes | Required treatment |
| --- | ---: | --- |
| Provider state (`.claude` or `.claude.json`) | 44 | Start a fresh provider session; retain the source privately. Review any portable settings separately from auth/session data. |
| Nonempty `.local` | 39 | Identify exact tool/data paths and version requirements; never assume all are caches. |
| Nonempty `.cache` | 38 | Prove a category can be regenerated, or include it in a reviewed private manifest. |
| Nonempty `.npm` | 21 | Separate package cache from configuration/auth and local tools. |
| Custom home categories | 11 | Transfer explicitly selected same-owner data with a path manifest and checksums. |
| Nonempty workspace siblings outside `app` | 2 | Preserve through a separate private archive; the app-only archive does not include them. |
| Individual app files above 128 MiB | 2 | Require bounded streaming or a reviewed app-specific preparation step. |

All 55 have shell/Git configuration. Its observed file sizes match the source
image skeleton, but size equality does not establish identical contents. Only
`01M28XBR5WVEB9B3XWX1CN4NEB` has no additional nonempty/home category beyond the
apparent skeleton and retained runtime logs. It is a candidate for a narrow
compatibility adapter, **requires its explicit reviewed manifest**. The other apparent skeleton
candidate, `01M2JXMTMTBA5XXYDDJY2T51C9`, has about 27.7 MB of workspace sibling
data and must not lose it.

The smallest next implementation is a fixed-path, authenticated private transfer
for verified stock shell/Git files and empty skeleton placeholders. It must
compare source content to a pinned trusted skeleton, safely stage the same
contents in the destination home, and verify hashes before cutover. Modified
files, additional owner data, provider auth/session directories, unexpected links,
and unreviewed path categories must remain blocking. Fresh Cube templates do
not establish that their home skeleton equals the historical Docker image.
A follow-up manifest can expand to specific nonsecret tool/data paths once their
runtime dependencies and rollback rules are explicit. Blanket home copying or
silently regenerating every cache would erase user changes or copy credentials;
the present migration gate deliberately remains closed for those cases.

## Fleet plan and reviewed owner-home transport

Use the newest production inventory, not a remembered count: the read-only
2026-09-23 inventory contains **56 sandboxes**, nine apps with no recorded preset,
and 30 ready raw snapshots (one source app needs a preset assignment). The
preflight enumerates every app, including projects without a current sandbox,
and separately reports historical/unassigned sandboxes. No preset is inferred
from filenames. `--fleet-presets` is an explicit JSON map of **app ID → preset**.
Raw snapshots require canonical IDs, matching source ownership, the exact
library path and real `workspace/app` ancestry. Existing Cube source archives
use their own immutable `cube-preset:` image metadata.

Before executing a fleet plan, freeze new-project admission and task submission,
drain all tasks, stop the control plane and regenerate preflight offline. Supply
its `identity_sha256` through `--expected-fleet` on every migration/recovery
invocation. It is checked before every phase and detects new, missing or
reassigned app/sandbox/snapshot IDs. Provider changes do not invalidate a
partially completed plan. This is an identity fence, not a claim that a running
inventory captured consistent file contents; stopped-source exports perform the
definitive checksum and scope checks. The CLI still migrates one reviewed
sandbox at a time; it does not silently skip blocked projects.

`--home-manifests` maps **sandbox ID → versioned HomeManifest**. See
[the private home contract](cube-migration/private-home.md). Each filesystem
leaf and empty directory must be classified. Preserve only explicitly reviewed
owner files/tools/subtrees; `.runtimed` and `workspace/app` use separate existing
channels. Provider/auth state stays on the original Docker source with a recorded
retention reason and must be regenerated in Cube by the normal credential flow.
Unclassified content, unsafe links or unsupported mounts block export. Prefer
`preserve` for owner-editable shell/Git defaults; `stock` proves exact trusted
bytes and consequently blocks export after a user changes those bytes.

The canonical manifest is frozen in the journal before stopping Docker. Source,
verified-target and reverse-copy home archives have independent durable hashes.
Home import replaces disjoint selected roots while quiesced; it is atomic per
root, not across all roots. An interruption must replay the journaled archive
before any resume/provider switch. Original provider state, app identity and
control files are never replaced by the home channel. Rollback restores current
Cube home writes and normalizes only restored owner data to UID/GID1000.

App transport v2 streams disk-backed ZIPs with limits of 4GiB compressed, 8GiB
expanded, 1GiB per file and 200,000 entries. Home transport has the same byte
limits and 500,000 entries. Existing byte endpoints keep their original smaller
bounds. Neither limit guarantees disk capacity: operator preflight must verify
host recovery storage and guest free space for compressed spools, expanded
staging, existing app/home content and retained crash recovery files. The 5GiB
pilot disk is not proof that every production project fits.

## Tests and performance scope

Focused Go race tests cover atomic provider changes, unchanged IDs/ports,
active-task denial, uncertain-create deduplication, migration and rollback crash
checkpoints, actual hidden-file/Git/data copies, new target writes surviving
rollback, corrupted archive rejection, changed-config recreation journaling and
config-drift refusal,
descriptor/lock exclusion, and authenticated retained-history replay.

The standard migration fixture uses real filesystem archives and SQLite
transactions; hypervisor lifecycle calls are replaced. The opt-in native
`TestLiveChangedConfigRollbackNormalWake` exercises real Docker source retention,
changed runtime config, normal wake recreation with `sandboxspec`/`appenv`, and
HTTP readiness returning the new environment value. It passed on the isolated
outer-VPS fixture in 48.82 seconds, including a lost rename acknowledgment
before its journal update and recovery while global Cube routing was enabled. Its Cube lifecycle is a filesystem fixture;
this is not a new real-Cube production migration measurement.

The separate opt-in `TestLiveDockerCubeRollback` additionally uses real Docker/Cube and a disposable
Node app, restores a transferred Git checkpoint, creates new data and completes a task through the deterministic agent fixture,
then restores data and history into Docker and probes the restarted app. It
requires a marked isolated VM and explicit template/image settings; it is skipped
in ordinary test runs. Task execution uses fake-opencode through runtimed, not real model calls.
Neither fixture establishes arbitrary application database consistency. Transfer is intentionally
offline. No migration archive or dependency-copy operation runs in the normal
preview resume path, so it does not add work to Cube's existing wake hot path.

Run the live fixture only in the marked disposable pilot VM with a freshly built
task-history-capable guest template:

```sh
# Build using the module's Go1.22/CGO Linux toolchain.
go test -c -o /PRIVATE/migration.test ./internal/migration
# Working directory must be control-plane/internal/migration.
CUBE_MIGRATION_LIVE=1 CUBE_MIGRATION_IMAGE=REVIEWED_FIXTURE_IMAGE \
CUBE_MIGRATION_TEMPLATE=REVIEWED_FIXTURE_TEMPLATE \
  /PRIVATE/migration.test -test.run '^TestLiveDockerCubeRollback$' -test.v
```

Successful live runs remove only their newly created Docker/Cube guests and
fixture directory. Failures retain those identities and archives for inspection.

The [slim v8 live run on 2026-09-23](cube-pilot-results/migration-live-v8-2026-09-23.json)
passed the actual Docker → Cube → new writes and task completion → Docker
roundtrip in the isolated benchmark VM. Forward migration took **35.2 seconds**
and rollback took **734 milliseconds**; the entire test took 48.14 seconds.
These are one-time offline copy/lifecycle measurements for a small Node fixture,
**not normal app resume measurements**. The new task ran through runtimed using
the deterministic fake-opencode fixture; no external model was called. The test
verified transferred Git checkpoint revert, authenticated archive checksums,
terminal task events, preservation of new writes and both task histories, stable
IDs/port, and HTTP 200 from the restored Docker app.

The v8 run also verified the fix for the failed v7 experiment: ordinary files on
Cube's overlay can have a different device ID from their containing directories.
Private export now tries the strict single-link path first and performs the
owner-home inode proof only when actual app hardlinks require it. The failed v7
guest, Docker container and fixture directory were explicitly removed after the
successful v8 run; sanitized diagnosis is retained in the report. The slim v8
image/template remain available for further isolated tests. No production
projects were moved, and the production per-owner review and rollout gates remain.
