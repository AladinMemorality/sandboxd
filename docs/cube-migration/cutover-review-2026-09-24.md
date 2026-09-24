# Current fleet migration review — 2026-09-24

The fresh read-only review accounts for **61 projects/sandboxes and 31 raw
snapshots**, including MyHomeTroc. It observed zero active coding tasks and no
migration journals. No source was frozen, stopped, migrated, modified or
published. The [sanitized per-project evidence](cutover-review-2026-09-24.json)
records identities, candidate presets, metadata results and private artifact
hashes. This is a live review, not a frozen plan or application-health proof.

## Current compatibility

The deployed `818e275` validator, built directly from its deployed source,
accepted **60/61 preliminary transport rows**. MyHomeTroc's live PostgreSQL
socket is the sole rejected row. All 61 projects and 31 snapshots remain
blocked in fleet preflight because no production template map was supplied.
The nine explicit legacy preset candidates remove snapshot-preset ambiguity;
those proposed assignments have not changed production metadata.

The candidate target counts are 46 React Pro, 12 React Vite, two Next.js and one
Express. Each of the nine empty-preset projects has React/Vite/Tailwind/Radix
package capabilities and a pnpm/Vite startup command; all nine therefore have
an explicit React Pro candidate. Source files and declared startup behavior
must be preserved, not replaced by the starter.

All 36 exceptional owner-home links match existing exact version-two contracts;
there are no unreviewed home links or unsupported app-root links in this scan.
Twelve custom-home/tool cases still require actual restored-application/native
execution. Six projects contain eight files above the former 128 MiB limit;
none exceeds the current 1 GiB individual streaming limit. The fleet has about
13.30 GB of regular app files; the largest app tree is about 1.50 GB. These are
uncompressed, live observations and do not establish guest/archive disk needs.

MyHomeTroc contains **one physical PostgreSQL 18 cluster** and one live socket.
Per-database `PG_VERSION` markers were excluded from the physical-cluster count.
Preserve its complete selected owner-home data and existing manifest; during
maintenance, stop PostgreSQL gracefully, verify the socket disappears naturally,
then repeat strict inventory/export and SQL recovery checks. Do not delete the
socket or database files to force eligibility. PostgreSQL remains optional;
other projects must not acquire a database process or be switched to the
`node-postgres` starter merely because that capability exists in the image.

The Classroom app's selected sibling library is explicitly preserved, but its
existing model URL targets a private gateway. The generic public HTTP(S) egress
policy correctly refuses that destination. This app needs a narrowly scoped
model-service compatibility adapter before cutover; transport eligibility does
not establish model behavior. Exact endpoint metadata and credentials-presence
proof remain in the private `classroom-compatibility-private.json` artifact.

## Capacity must be preserved

Actual Docker inspection found **all 61 sources limited to two CPUs and
2,147,483,648 bytes of RAM**. The aggregate configured limits are 122 CPUs and
122 GiB, not simultaneous measured consumption. The number running changed
from 24 to 19 between live observations. Numeric Node/Java heap-flag scans of
container environment, startup/package declarations and running process
arguments found no overrides; no raw arguments or credential values were saved.

The earlier one-CPU/one-GiB pilot templates would halve every project's limits.
They are not acceptable fleet targets. Prepare reviewed production templates
that preserve at least the current limits and verify their actual VM metadata.
The migration guard adds `--template-resources`, an exact template-ID map:

```json
{
  "REVIEWED_PRODUCTION_TEMPLATE_ID": {
    "cpu_milli": 2000,
    "memory_bytes": 2147483648
  }
}
```

Fleet preflight compares that contract with actual Docker limits. A new
migration validates capacity before creating its journal and before stopping
the source. The source/container/template resource contract is fsynced beside
the recovery archives and cannot silently change during resume. Actual Cube
`GET /sandboxes/{id}` CPU, RAM and template identity must match before import,
verification and acceptance. Missing metadata, unlimited source limits,
missing contracts or reductions fail closed. The operator map itself is not
claimed to be provider verification. No new template-metadata API was invented.

A later read-only idle census observed 60 `sleep` policies and one `always_on`,
with zero active keepalives. Database status was 31 running and 30 stopped.
The running rows' last activity was under five minutes for four, five to fifteen
minutes for nine, and fifteen to sixty minutes for eighteen. Across all rows,
21 were one to six hours old and one was six to twenty-four hours old. This
point-in-time demand is not an upper bound on concurrent wakes; sizing needs
[durable admission control](durable-admission.md) as well as room for host/control
services and guest overhead.
The private `idle-summary.json` records the exact observation time.

## Private review inputs and commands

Private manifests, explicit preset candidates, path-level blockers, source
resource inspection and supplemental PG/large-file metadata remain under:

`/opt/baarcha-bench/cube-cutover-review-20260924/`

`project-review-private.json` joins every project to its exact manifest,
remaining paths/categories and source limits. `candidates/` contains the
versioned manifests and preset proposals. These private files must not be
committed, published or treated as immutable while workloads continue changing.
The CLI used for the initial report has SHA256
`d4181b61dee70e5bae8923311b07e269c9bc04ec1317bf0b64511c4580afd352`.
Its report predates the additional resource guard described above.

After building the reviewed CLI containing the guard, the following native
host commands are read-only. `SANDBOXD_CUBE_TEMPLATES` must contain actual
reviewed production IDs; do not substitute the isolated pilot's IDs.

```sh
REVIEW=/opt/baarcha-bench/cube-cutover-review-20260924
cube-migrate --database /var/lib/sandboxd/state/sandboxd.db \
  --workspaces /var/lib/sandboxd/workspaces \
  --home-manifests "$REVIEW/candidates/candidate-home-manifests.json" inventory
cube-migrate --database /var/lib/sandboxd/state/sandboxd.db \
  --workspaces /var/lib/sandboxd/workspaces --library /var/lib/sandboxd/library \
  --home-manifests "$REVIEW/candidates/candidate-home-manifests.json" \
  --fleet-presets "$REVIEW/candidates/candidate-preset-assignments.json" \
  --template-resources "$REVIEW/reviewed-production-template-resources.json" \
  fleet-preflight
```

The live identity digest was
`574ecb9211b33ae935abec47b9668281786bdc569518b4248e5f47162f86e05c`.
Regenerate after admission/task drain and maintenance; do not reuse it if
membership or ownership has changed. The existing migration command requires
that final digest, the reviewed preset and manifests, the same resource map,
private recovery archive path and existing encryption key. Follow the
[offline runbook](../cube-existing-project-migration.md); this review does not
execute cutover commands.

## Backup and rollback readiness

The [independent recovery fixture](../cube-pilot-results/independent-backup-restore-2026-09-24.md)
and actual isolated lifecycle/journal checks provide code-level evidence.
Production still needs its independently stored consistent database, exact
key/config, full source homes/control history, library artifacts, recovery
archives and immutable image/container references. Include the new resource
contract with recovery archives. Verify restored owner files, task history,
application/SQL behavior and normal Docker recreation before retiring any
source. A stale database or provider flip is not a rollback for newer Cube
writes. No production backup restoration is claimed by this inventory.

## Capacity guard verification

The isolated Linux race suite passed for `internal/migration`,
`cmd/cube-migrate`, and `internal/docker`. Regression tests reject half-sized
templates before any source stop, missing/unlimited limits, source-plan drift,
changed contracts on resume and missing/undersized actual Cube resource
metadata. A fractional CPU quota rounds upward rather than reducing allowance.
The fleet fixture verifies a two-CPU/two-GiB source passes a matching contract
and becomes blocked by a one-CPU/one-GiB contract. These checks use disposable
filesystem/SQLite fixtures and local fake provider HTTP; no real project was
migrated. Logs are private operator evidence under the review directory:
`capacity-tests.log` and `capacity-final-focused.log`.
