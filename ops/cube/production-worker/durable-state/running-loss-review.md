# Running worker loss after durable metadata enrollment

This is a source review of upstream commit
`31d911e430fdf8a8879bd062b8e78066c1a8e89d` with installed Cubelet candidate
`de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b`,
including retention patch0005, durable patches0006–0008 and metadata-loader
correction0010. It does not substitute for the coordinator's new abrupt-loss
acceptance. Prior actual recovery proved latest files and PostgreSQL in a
replacement provider, before this durable-state enrollment. Production rollout
status remains in the coordinator's current acceptance report.

## Expected native outcomes

`Cubelet/plugins/cube/internals/cubes/restart.go` loads persisted CubeBox records
at startup. A paused record is retained without probing a shim. For a previously
running record, `RecoverContainer` loads the existing containerd container; it
never calls NewTask or cold-boots a replacement VM:

| Observed storage/task condition | Expected result |
| --- | --- |
| Container metadata exists, task vanished, no previous FinishedAt | UNKNOWN |
| Container metadata is missing, existing running status | FinishedAt is set; EXITED |
| Container/task transport error | UNKNOWN, or recovery fails before the box enters the in-memory list |
| Surviving task after Cubelet-only restart | Existing task status is reloaded |
| Durable paused tombstone and valid pause artifacts | Existing pause/resume path remains available |

A failed `RecoverPod` logs and skips adding that box to the in-memory list. Its
persisted CubeBox record is not deleted by this branch; therefore API404 remains
possible on errors even with persistent databases. `afterRecover` runs after the
box is added and asks storage to recover the original binding. Neither ordering
constitutes application readiness or latest-SQL recovery.

Master must receive the actual refreshed worker summary. Installed API patch0004
reports Unknown/Stopped truthfully and refuses Connect before claiming Running.
A previous successful pause is not a valid running-crash checkpoint: Master
removes its pause binding after resume. Never restore old RAM against a newer
writable disk.

## Cleanup and admission boundaries

- `Cubelet/services/cubebox/service.go:scanDeadContainer` rechecks status and
  logs/counts expired terminated entries. It does not call Destroy. Its comments
  mention historical destroy cascades; the executable path is the relevant fact.
- `services/cubebox/events.go` handles task exit by deleting the task, recording
  exit status, optional configured post-stop hooks, and metadata synchronization.
  It does not directly destroy the entire sandbox/current volume.
- Storage patch0005 retains unresolved StorageInfo plus fallback metadata when a
  referenced CoW object is missing. Normal reads still fail closed. It prevents
  the original startup branch from deleting the whole record merely because one
  RAM/rootfs object is absent.
- `storage/plugin.go:collectLiveSandboxIDs` includes all loaded boxes regardless
  of running state. Volume-reference recovery can remove references for a box
  omitted from that list. `plugins/volume/refcount/store.go:RecoverRefCounts`
  itself only edits reference records; it does not delete backing files. External
  volume plugins are outside the reviewed single-current-volume recovery scope.
- Exported workflow Init/InitHost, explicit Destroy, unsafe cleanup and reset
  operations are destructive. They are not normal plugin InitFn startup and
  must not be used to make recovery readiness pass. Native cubecow library
  internals and independently configured external cleanup jobs are not proven
  harmless by this Go source review.
- `cube-lifecycle-manager/internal/sweeper/sweeper.go` always continues after
  AutoPause, including failure; it has no pause-error-to-kill fallback. With
  AutoPause=false it does call Kill. Its not-found pause handling deletes proxy
  and registry metadata, not the worker disk. Definitive pause failures can
  misleadingly push advisory proxy state back to running; this is not an
  authoritative observation of a live task.
- All platform create paths (API lifecycle, offline migration and offline
  recovery) explicitly select timeout pause and AutoResume=false. They must
  remain the sole tenant admission path. Cube client defaults alone are not a
  substitute for this explicit no-proxy-autoresume selection.
- Required patch0007 keeps UNKNOWN, PAUSING and PauseFailed charged in native
  quota accounting. Ordinary EXITED is excluded by native accounting. The
  platform's durable admission guard independently retains charged reservations
  for unknown/stopped and registered-ID404; it never treats these as confirmed
  release. The controller guard remains necessary.

## Truthful platform state

Reconciliation recognizes the actual typed `cube.ErrRuntimeUnavailable`, removes
its preview lease, and records a generic recovery-required error while retaining
provider binding, encrypted credentials, app/owner, files, task history and
charged admission. A transient provider outage or foreign-ID response does not
fabricate that state. The v1 response exposes `error_code` without probing the
unavailable supervisor or leaking provider errors.

The public preview route returns503 with that explicit code, before minting
preview credentials or attempting another wake. Private-viewer and crawler
checks stay before runtime lookup. The published viewer stops polling, removes
a stale iframe and shows that the runtime is being recovered; ordinary wake
retries are not offered as a recovery method. Generic503 capacity/service
failures retain their existing retry behavior. No automatic replacement exists.

Validation: focused real-HTTP/SQLite Go API race tests passed on Linux with
2CPU/2GiB, network disabled and512MiB executable test tmpfs. They verify unknown,
stopped, known404, outage and foreign identity, retained charged capacity and
no provider mutations. Platform route/wake tests (27), rendered viewer test (1)
and TypeScript noEmit passed. These are code regressions, not power-loss proof.

## Shortest existing safe operator recovery

The implemented path in [recovery/README.md](../recovery/README.md) captures a
fenced clone of the **current** writable volume, with exact image/original upper
identity, and parses it only in the isolated rescue appliance. It replays the
filesystem journal only on a clone, preserves WAL, validates the merged home,
then imports canonical private app/home archives into a quiesced new provider.
The original disk and unmodified archive remain retained. PostgreSQL starts
normally and must verify its latest acknowledged row without recommitting it.

The offline recovery session and durable journal already provide fencing,
archive fingerprints, target identity, config/task preservation and an atomic
binding/admission switch under the stable Baarcha app ID. The synthetic live
acceptance verified this path, including latest SQL. That proof does not make
same-provider native recovery available.

Remaining scope before claiming a general fleet operator procedure:

1. Repeat the owned abrupt-loss acceptance on the currently installed durable
   candidate, checking actual persistent metadata, expected unknown/stopped
   identity, original current disk, and latest-file/SQL recovery.
2. The supplied converter is deliberately fixed to the reviewed synthetic PG
   home scopes and requires its independently captured latest marker. Other
   presets/home entries need an explicit reviewed conversion/import plan;
   unknown directories are rejected, not silently dropped. The generic runtime
   validators and binding journal already exist.
3. Active task recovery needs an explicit task disposition: the journal refuses
   an active task instead of inventing successful completion. Preserve its
   history independently and determine a reviewed interrupted-task transition.
4. Fencing receipts are operator assertions, not a distributed fencing system.
   Before reading/importing, stop all controller/sweeper writers and prove no
   old task/device can still write. Keep source retention and independent backup
   policy; neither persistent metadata nor a same-disk clone protects host disk
   loss.
