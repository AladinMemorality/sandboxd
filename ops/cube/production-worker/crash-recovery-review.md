# Worker restart and crash recovery review

Source is pinned to 31d911e430fdf8a8879bd062b8e78066c1a8e89d. A clean paused-worker
restart and an unplanned loss of a running nested VM are different guarantees.
No unplanned crash test has yet been executed. Existing production traffic
remains on Docker during acceptance. A clean worker OS poweroff and boot has
been exercised with two owned paused fixtures. The first resume continuation
reached the API before it was ready. The resulting paused-metadata defect was
fixed and verified on both retained fixtures; subsequent seven-preset and
12-slot admission acceptance passed. Those checks do not prove recovery of
acknowledged writes after an unplanned running-guest loss.

## Verified source paths

- `Cubelet/cmd/cubelet/main_unix.go:47` handlesTERM/INT by cancelling context and
  calling `Server.Stop`. `Cubelet/services/server/server.go:361` stops service
  endpoints/background loops; it does not enumerate and pause active guests.
  Installed `scripts/systemd/cubelet-stop.sh` invokes `stop_pid_with_timeout20`,
  whose common.sh helper escalatesTERM toKILL. This is not a guest checkpoint.
- `Cubelet/services/cubebox/local.go:218` calls `RecoverAllCubebox` at startup.
  `plugins/cube/internals/cubes/restart.go:24` reloads durable sandbox metadata;
  `RecoverPod` explicitly preserves paused tombstones without a running shim.
- In that same file, `RecoverContainer:136` marks missing containers as finished;
  `loadStatus` marks missing tasks unknown. Surviving shim tasks can reconnect
  after a Cubelet-only process restart. This does not recreate tasks after an
  entire worker/outer-QEMU crash.
- `Cubelet/storage/local.go:323` refreshes XFS backing-file references. Persistent
  disk metadata may survive, but this is not cold boot or same-ID app recovery.
- `CubeAPI/src/services/sandboxes.rs:365` Connect resumes only `Paused`; it can
  return success for other statuses without recreating the task. Its state
  converters around970 default unknown states to `Running`. API200/stateRunning
  is therefore insufficient evidence of application recovery.
- `CubeMaster/pkg/service/sandbox/sandbox_resume_pause.go:286` requires a READY
  pause snapshot binding for same-ID resume. After successful resume, the code
  around470 deletes that binding. A later crash of the running VM cannot simply
  use the previous pause binding as a valid current checkpoint.
- `Cubelet/services/cubebox/service.go:1070` dead-container scan skips paused
  guests. After the default1h TTL it currently logs/counts expired entries;
  this function itself does not call Destroy.
- `cube-lifecycle-manager/internal/sweeper/sweeper.go:179` always continues after
  the AutoPause branch, including pause failure. It does not fall back to Kill.
  Kill is selected only for AutoPause=false. Missing-Master responses can remove
  the sidecar/proxy metadata without deleting the worker's backing disk.
- `Cubelet/storage/local.go:267` startup storage recovery currently deletes the
  entire StorageInfo record and its fallback file when any referenced CoW object
  is missing. A missing memory object is not proof that the current writable
  disk is disposable. `0005-retain-unresolved-storage-metadata.patch` preserves that metadata
  nonfatally and logs the need for operator recovery. Targeted fake-CoW tests
  passed for missing RAM/rootfs, repeated recovery, unaffected healthy records,
  fail-closed reads and repair using original disk identity. The native Cubelet candidate passed storage regressions and exact embedded BPF identity checks.
  The coordinator installed SHA256 `254b5e7b11c7c6f865e3e1816c3737a4041424b7e84b5aba7dc406e596eecbed`
  on the empty new worker, with the prior binary retained for rollback. This is not crash recovery.
- `Cubelet/storage/plugin.go:494` counts every retained sandbox as live for
  volume-reference recovery, without requiring RUNNING. Explicit storage
  cleanup is an operator RPC/CLI path; it must remain disabled for quarantined
  recovery records. This trace does not prove every possible external cleanup
  job or manual action harmless.

## Acceptance still required

Use only an owned synthetic guest, preserve an independent export first, write
and fsync a file and committed PostgreSQL row, then separately test Cubelet-only
process loss and entire worker-process loss under the coordinator's explicit
bounds. Verify the latest file/database state, actual supervisor and service,
provider ID/binding, backing-file identity and cleanup behavior. A paused-only
restart or an API200 response cannot substitute for this test. Do not enable
outer `Restart=on-failure` as a claimed data-recovery solution before that proof.

Normal planned shutdown should pause/drain while the controller is still
available, then shut down the worker OS. Host shutdown hooks must not assume the
platform controller is still running; they need an independently tested local
worker drain or a pre-shutdown maintenance sequence. Abrupt host loss remains a
separate recovery problem, even with an orderly ExecStop path.

## Cold boot primitives and missing integration

`CubeShim/shim/src/sandbox/sb.rs:849` recognizes `cube.snapshot.disable` and
boots a fresh kernel through `boot_vm` instead of restoring RAM. This alone
is insufficient: `cube.appsnapshot.restore`, pause/runtime snapshot markers,
agent start mode and container identity mapping must all enter cold mode.

`Cubelet/pkg/store/cubebox/cubebox.go:36` persists container Config, labels,
annotations, image references, component versions and recreate-critical network
fields. `services/cubebox/cube_container_create.go:1211` resolves the root volume
from StorageInfo and uses `disk/<original-container-ID>` within that disk.
`agent/src/rpc.rs:2247` mounts its `upper` and `work` against the ordered image
lower directories. The current filesystem is the merged view, not the upper
file tree alone.

Normal Create is unsuitable for recovery: `storage/local.go:831` allocates and
persists a new StorageInfo; its rootfs allocator clones a new writable generation.
`plugins/workflow/engine.go:539` converts Create failure into Destroy rollback.
The same-ID collision code accepts only PAUSED tombstones, not crashed guests.
A complete same-ID recovery operation therefore requires a fenced, journaled
adoption of the current disk with rollback that can never delete retained data,
exact pinned components, cold task/network recreation, bootstrap re-init and
Master/proxy reconciliation. No such endpoint has been added or deployed.

A potentially smaller platform recovery operation can retain the stable Baarcha
app ID while replacing its Cube provider ID. It must first fence the old guest,
clone its current disk, replay the filesystem journal only on the clone, and
export the merged filesystem using exact original image layers. Preserve the
whole `/home/sandbox`, including `.baarcha-postgres/data` and WAL, then import
into a clean replacement and verify PostgreSQL crash recovery before an atomic
binding swap. Never restore old RAM against a newer disk. Whiteouts, opaque
directories, hardlinks, symlinks, permissions and separate volume mounts all need
fixtures; a raw tar of `upper` is not a correct export. Parse guest-controlled
filesystems in an isolated rescue VM/appliance for a production recovery tool;
a private mount namespace alone still shares the host kernel.

Read-only inspection primitives are `cubecli cubebox inspect EXACT-ID` (private
CubeBox JSON, contains secrets; redirect to mode0600 files) and `cubecli storage
ls --raw` (tab-separated ID and JSON; filter the exact owned ID). These expose
the original container IDs, CubeRootfsInfo, current volume path/name/generation,
and image references without starting or modifying the guest.

## Paused Info bug found during the clean restart acceptance

The pinned Master `fillPauseBindingInfoFromMaster` replaces a matching Cubelet
summary with a synthetic row containing only state, IDs and IPs. CubeAPI derives
empty template/metadata and zero resources from that row. This is independent
of Redis durability and can affect any paused Get.

`0003-preserve-paused-sandbox-info.patch` preserves the matching worker summary,
then overlays only the pause binding's state/annotations and placement. With no
worker summary, it uses only durable canonical spec fields; missing information
remains unknown, with no guessed template-default allocation. It never copies
private create annotations or bootstrap tokens into the response. Five new tests and the complete Master sandbox/sandboxspec/pausesnap package
suites passed; the candidate built successfully. The coordinator deployed it on
the fresh worker only and verified both owned paused fixtures now return the
correct template, resources and fixture metadata. This fix does not implement
running-guest crash recovery.

`0004-truthful-sandbox-state.patch` separately preserves Unknown/Stopped/Pausing
in CubeAPI Get/List and requires observed Running before Connect/Resume reports
success. An unavailable state is rejected before resume mutation, with a generic
503 and Retry-After: 2. Four HTTP-backed regression tests and all 147 CubeAPI
tests passed. The release candidate built successfully; the coordinator deployed it on the fresh worker only and verified authenticated
empty inventory plus anonymous401. This agent did not deploy it. Neither patch implements cold boot from the latest disk.
