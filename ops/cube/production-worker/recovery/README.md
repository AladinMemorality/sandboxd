# Current-disk recovery — owned replacement proof

This is an operator-only candidate for a **stable Baarcha app ID with a replacement
Cube provider ID**. It retains the crashed source and exports the latest current
disk, not the earlier RAM checkpoint or baseline backup. No automatic platform
recovery endpoint is added. No native cold-restart patch is included in the built
Cubelet candidate. The owned whole-worker-loss fixture passed current-disk export and latest
app/home/PostgreSQL recovery into a new sandbox. Native same-ID recovery failed.
This is not production rollout acceptance; operational reconciliation and durable
metadata/pause changes remain separate gates.

## Why this implementation

Pinned upstream `31d911e430fdf8a8879bd062b8e78066c1a8e89d` can cold-boot a kernel
with `cube.snapshot.disable`, but normal Create cannot safely adopt an existing
current disk. `storage/local.go:831` allocates and overwrites storage metadata;
`plugins/workflow/engine.go:539` rolls failed Create back through Destroy;
`services/cubebox/cube_container_create.go:440` has another synchronous Destroy
on bootstrap failure. Create also regenerates container IDs, while the disk's
upper directory is keyed by the original container ID at line1253.

A correct native operation would need a durable recovery journal and disk lease,
an explicit clone/adopt storage mode, a rollback that can delete only new owned
artifacts, original upper-directory mapping, removal of every RAM/app-snapshot
restore flag, exact component/image pinning, fresh bootstrap credentials and
atomic Master/controller reconciliation. Same-ID task replacement adds containerd
identity collision cleanup. New-provider native adoption avoids that collision,
but not the remaining work. Calling existing Create with a disk path or adding a
request builder alone would leave destructive failure paths intact.

The rescue path instead uses existing normal Create/import on a new provider
only **after** extracting the current home. Original storage is never imported
as a live writable device or passed to fsck. Upstream recovery metadata retention
patch0005 protects the metadata needed to locate it.

## Supported initial scope

- One container, one writable ext4 CoW current volume, XFS backing file, retained
  ordered OCI directory layers or one exact ImageReferences-bound PMEM ext4 image,
  and immutable original container identity.
- Full `/home/sandbox`, including workspace, hidden user files, PostgreSQL18
  data/control/WAL and the stale `postmaster.pid` as original evidence.
- No external data volumes, multi-container layouts, EROFS roots, ambiguous
  layer aliases or missing image layers. These fail explicitly.
- Home special nodes and extended metadata fail explicitly. Only a socket inode
  matching `.baarcha-postgres/run/.s.PGSQL.<digits>` or the supervisor's documented
  `.runtimed/sock` endpoint may be omitted, and its exact
  path is recorded. Current preset sockets actually live under `/tmp`, outside
  this export. Regular files at those names are never silently omitted.
- Archive validation rejects escaping links and privilege metadata. Any approved
  template link exception must be bound to exact archive/image identity by the
  validator's separate reviewed plan. Do not extract first and validate later.
- Sticky/setuid/setgid mode bits are rejected explicitly; the runtime import
  contract preserves ordinary permission bits and must not silently strip them.

## 1. Fence and derive the private capture plan

Keep all lifecycle mutations disabled for the crashed provider: controller wake,
Cube Connect/Delete, idle sweeper and automatic cleanup. Reserve its admission
slot until recovery commits. A changed boot ID alone is insufficient fencing.
Prove no current nested task references the disk and keep that proof valid while
capturing. The capture tool's fence receipt records this operator assertion; it
is **not** a distributed fencing protocol.

Before the crash, run the read-only exact schema preflight:

```sh
python3 metadata_capture.py --sandbox-id EXACT-ID --output NEW-PRIVATE-DIRECTORY
```

It saves both inputs and validates `plan.json`, printing only a sanitized success
summary. Its storage parser handles the actual reduced protobuf CLI schema
`{namespace,sandboxID,volumes:[{name,file_path,volume_name,kind,gen}]}`; omitted
`gen` is the protobuf zero value. CLI does not return filesystem type; ext4 is
independently verified inside rescue before journal replay.

For manual escrow, save `cubecli cubebox inspect EXACT-ID` to a root0600 file; it contains credentials.
Save only that ID's `cubecli storage ls --raw` JSON value to another0600 file.
Never print either file. `plan.py` checks the ID/namespace/root-volume mapping and
resolves image layer aliases through the saved read-only virtiofs share map, or
validates the PMEM ext4 path against the exact image ID, Medium1 reference and
fixed cubebox_os_image directory. Canonical ContainersMap.ContainerMap and legacy
Containers are supported; conflicting maps fail. Pinned XFS records live writable
clones as kind=snapshot too (storage/cow_store_ops.go); those require the exact
sb-<owned-ID>-rootfs-gen<Gen> name and backing-file basename, never a template,
memory or paused package object:

```sh
python3 plan.py --cubebox PRIVATE-CUBEBOX.json --storage PRIVATE-STORAGE.json \
  --sandbox-id EXACT-ID --output PRIVATE-PLAN.json
```

The output contains source paths and metadata digests, never environment values
or credentials. It cannot change a disk or task. The full metadata remains in
private escrow.

## 2. Capture on the fresh worker without mounting the guest filesystem

Create root0700 `/data/cube-recovery`. Issue a short-lived private fence receipt:

```json
{"purpose":"CUBE_CURRENT_DISK_CAPTURE","sandbox_id":"EXACT-ID","worker_machine_id":"REVIEWED-MACHINE-ID","previous_boot_id":"BEFORE-LOSS","current_boot_id":"CURRENT-BOOT","expires_at":0,"no_task_verified":true,"management_fenced":true}
```

Set `expires_at` to a Unix timestamp no more than30minutes ahead. The script
requires the observed machine ID/current boot, a different prior boot and the exact
sandbox ID. Run only after the crash coordinator releases its worker-local
operator lock; capture acquires the same lock exclusively.

```sh
python3 capture.py --plan PRIVATE-PLAN.json --fence PRIVATE-FENCE.json \
  --output /data/cube-recovery/NEW-UNUSED-JOB
```

Capture requires80GiB free, never overwrites a job, opens the current source
read-only and uses `FICLONE` into a new0600 file. It refuses a streaming fallback.
It records source inode/generation/size, verifies unchanged source identity,
archives the trusted lower directories individually with their whiteouts/xattrs
(`--xattrs --xattrs-include=* --acls` on both capture and extraction),
and hashes every artifact. For explicit pmem_ext4 it instead copies the immutable
image as opaque bytes, verifies source stat identity and two full source digests,
and verifies the destination digest. No worker-side mounting or filesystem parsing
is performed. `rescue-input.json` is written last and fsynced.
Failure leaves a private partial stage; no source deletion or retry rollback.

Copy only this independent bundle to the isolated rescue VM, not live worker
storage, host directory mounts or management credentials. Preserve its private
permissions. Record the transport hashes independently.

## 3. Replay and merge only inside the isolated rescue VM

Require a dedicated `baarcha-cube-rescue` QEMU/KVM VM, no host filesystem shares,
no active non-loopback NIC while parsing guest files, and adequate scratch space
for a10GiB disk plus layer extraction and bounded20GiB export. Root has prepared
a3GiB/2CPU restricted rescue VM with management-only localhost20223 forwarding.
The parser does not run on the worker or outer host.

Place the bundle in `/var/lib/cube-rescue/input`. Install Python3, util-linux,
e2fsprogs, GNU tar and the overlay kernel module before disabling networking.
A local systemd job must lower the reviewed NIC, call the exporter, and restore
that same NIC in its EXIT trap; do not depend on an SSH session surviving the
link-down. The launch/retrieval coordinator owns that wrapper and verifies QEMU
`restrict=on,ipv6=off` and absence of host shares. Invoke:

```sh
python3 rescue_export.py --input /var/lib/cube-rescue/input \
  --work /var/lib/cube-rescue/NEW-UNUSED-WORK
```

The exporter verifies every input hash, copies `current.ext4` to a separate
scratch inode, runs `e2fsck -p` there (accepting only clean/corrected exit0/1), and
mounts it `ro,noload,nosuid,nodev,noexec`. It builds a **read-only** overlay with
`disk/<original-id>/upper` as the highest lower layer followed by the exact
original image layers, retaining whiteout/opaque semantics. A PMEM lower is type
checked and checked read-only with e2fsck -fn (only exit0 accepted), then mounted
ro,noload; it is never repaired or journal-replayed. All input artifact hashes
must remain unchanged. It never runs a
guest executable. It exports merged home as uncompressed PAX tar, hashes it,
records exclusions and unmounts the private mounts. Cleanup failure stops with
artifacts retained. The original input clone's hash must remain unchanged.

Retrieve `home.tar` and `export-report.json` privately after networking is
restored. No owner content should enter logs or repository evidence.

## 4. Validate, import and prove latest durability before rebinding

Run `archive_validate.py` before any extraction/import. Require PostgreSQL layout
and the exact latest independently acknowledged marker for the synthetic test.
Validation proves archive structure/file markers, **not** WAL/SQL recovery.
Use the canonical app/home-v2 converter and canonical runtime import checks;
plain tar is not accepted directly by those APIs.

Create a separately owned replacement with the exact pinned preset/image and
fresh runtime/traffic credentials. Quiesce its app and PostgreSQL before import.
Do not overwrite a live replacement database. Preserve the raw archive's stale
`postmaster.pid`; any removal must be a separately recorded exact-file operation
only after proving the replacement database is inactive and the file came from
the old provider. Never remove PID locks as a general startup retry.

Start PostgreSQL normally so WAL recovery runs. Verify the latest committed SQL
row and both latest app/home files without issuing another commit, then verify
real app health and unchanged ownership/config. Persist a recovery journal that
links old/new provider IDs, archive hashes and verified marker. Only then atomically
swap the stable Baarcha app's provider binding and admission ownership, invalidate
preview leases and release the old admission charge. Keep old source disk/metadata
until a separate retention decision; failed import/verification cannot delete it.

## Tests and actual recovery evidence

`python3 -m unittest discover -s . -p '*_test.py' -v` covers metadata mapping,
unsafe layouts, bounded fence receipts, archive structure/links, input corruption,
host guards and narrowly excluded sockets. The 33 offline tests pass. The actual current ext4 disk was independently
captured after the owned worker interruption, replayed only on an isolated rescue
copy, and exported. A fresh sandbox imported the verified app/home archives and
passed real health plus the latest committed SQL and file markers without a new
commit. See `results/2026-09-25/current-disk-replacement.json`. The source disk and
original archives remain retained; no production binding was changed.

## Observed PMEM layout and native fixture (2026-09-25)

The retained owned r4 guest uses canonical `ContainersMap.ContainerMap`, an exact
`ImageReferences` Medium1 ext4 root image under the fixed cubebox_os_image path,
and an XFS current writable root generation recorded as `kind=snapshot`.
The actual path has no symlink components. The planner now validates those exact
relationships; it does not infer a writable disk from kind alone or choose a
fallback snapshot.

`pmem_fixture.py` and its bounded rescue-only wrapper passed against two new
128MiB synthetic ext4 files. The actual exporter read the immutable lower without
repair, replayed only a separate current-disk scratch copy, and preserved current
whiteout/opaque-directory semantics, hardlinks, symlinks, modes and latest app/home
markers. Canonical Go private app/home import validation passed. An independent
older baseline and the lower image remained unchanged. The first attempt exposed
a test-helper lookup that did not normalize GNU tar's `./` hardlink prefix; it
was corrected and the complete fixture rerun in a fresh stage. Both attempts'
mounts were cleaned and the NIC restored.

Evidence: `results/2026-09-25/pmem-native.json` and `pmem-build.json`.
These synthetic files contain dummy PostgreSQL control/WAL bytes: this does **not**
prove real database recovery or authorize customer migration. The later actual
PostgreSQL disk test separately passed export, replacement startup and latest SQL
nonce verification; its evidence is `current-disk-replacement.json`.

## Management fencing review

On the reviewed fresh worker, API/templatecenter/Master stop with TERM and have
no custom ExecStop. Proxy and lifecycle manager custom stop scripts stop/remove
only their own Compose components (no volume-delete flag or guest lifecycle API).
Cubelet's stop script sends TERM to its exact PID, waits20seconds and can escalate
to SIGKILL; its unit uses `KillMode=process`. **Stopping Cubelet does not prove a
running guest VMM has stopped.** Its pinned server shutdown closes listeners and
background jobs; the operator must prove absence of tasks separately.

The Cube-owned containerd API is `/data/cubelet/cubelet.sock`, namespace `default`.
The Docker socket `/run/containerd/containerd.sock`, namespace `moby`, is unrelated.
A read-only task inventory is:

```sh
timeout 5s ctr --address /data/cubelet/cubelet.sock --namespace default tasks list
```

An empty result must be paired with the independently changed worker boot ID,
absence of the exact owned shim/VMM and any process holding the current disk,
and a continuing lifecycle mutation fence before capture. A missing/unreachable
API is not an empty task inventory. This tool does not implement distributed
fencing, terminate tasks or claim automatic recovery.
