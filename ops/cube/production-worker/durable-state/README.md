# Durable critical metadata candidate — no deployment

Pinned v0.7.1 source31d911e430fdf8a8879bd062b8e78066c1a8e89d mounts
`/data/cubelet/state` as tmpfs **inside Cubelet's private mount namespace**.
Checking findmnt in the worker's initial namespace reports the underlying XFS
and misses this. Read-only nsenter confirmed the actual cubebox DB shards at
`state/io.cubelet.internal.v1.cubebox/db/{b..k}/meta.db` were tmpfs. After the
owned running guest's whole-worker loss, these shards were freshly empty; node
inventory was0 and GET404. Paused bindings had previously survived through
Master's separate persistent pause table. Native running recovery failed.

Separately, `pkg/utils/MakeBoltDBOption` defaults to NoSync and NoGrowSync, while
containerd's metadata config also uses no_sync=true. Moving files alone would
leave acknowledged metadata vulnerable to power loss.

## Candidate changes

Patch0006 introduces the explicit `durable_metadata_root` config contract. It
retains volatile task sockets under State, and requires11 exact persistent plugin
paths outside State: cubebox identities, multimeta, storage bindings, volume and
lifetime references, image metadata, cgroups, network allocations, netfiles,
cleanup bookkeeping, containerd metadata and overlay index (volume/lifetime share
one root, containerd metadata and overlay have separate roots).

The root must already exist, root-owned0700, on XFS. Every plugin must explicitly
point to its reviewed child path; images use state_path. Symlinks and unsupported
nested filesystems fail. Missing/no_sync=true containerd settings fail.
Known legacy metadata directories containing any database or symlink fail before plugin startup: the
candidate never copies, deletes, resets or implicitly migrates legacy state.
A distinct `/data/cubelet/persistent-metadata` avoids the existing tmpfs mount.
All guest writable/image data_path settings remain exactly as configured.

CubeStore defaults enable bbolt data/growth syncing; new database directory
ancestors are fsynced before returning success. NoFreelistSync remains enabled:
freelist pages can be reconstructed from durable database pages. This does not
claim that a higher-level asynchronous cache has already flushed, or that every
snapshot artifact is durably committed. Those separate paths still need audit.

`render_config.py OLD_PRIVATE_CONFIG NEW_PRIVATE_CONFIG` writes a new0600 config
only. It verifies the parsed TOML changes exactly the approved metadata fields
and explicit containerd no_sync=false; private credentials and guest data paths
are preserved. It refuses overwrite, unknown State paths and an already-enabled
configuration. No installer, service operation or mount operation is included.

## Deployment gate and migration contract

1. Keep customer routing/admission disabled. Independently inventory running,
   paused, missing-provider and retained current-disk objects. A0 inventory alone
   does not mean no retained state after the observed metadata loss.
2. Fence all lifecycle mutations, stop management components and establish no
   guest task/VMM/open writable-disk descriptor. Preserve consistent current
   databases, configs, old binary, network/BPF identities and physical disk
   manifests. Legacy tmpfs metadata must be escrowed through its actual mount
   namespace while it exists. Never hot-copy live Bolt DBs as a backup.
3. This initial candidate supports a reviewed empty-node enrollment or an
   explicit separately audited offline migration only. It has no legacy database
   converter. Existing failed fixture disks remain quarantined and retained;
   customer data may not be classified as empty or silently orphaned.
4. Prepare the new private XFS metadata root and fsync its parent. Render a new
   private config, preserving the old one. Validate exact binary patch stack
   (both network patches, retention and0006/0007/0008), native CGO and identical protected
   BPF inputs. Do not substitute a CGO-disabled or pristine worker binary.
5. If old State still contains metadata, startup intentionally refuses. An
   operator must first finish offline migration/escrow review. Do not defeat the
   guard by deleting databases or blindly copying tmpfs state. A fresh controlled
   namespace/worker boot after proof is separate from data migration.
6. Validate actual plugin DB paths from inside Cubelet's namespace, XFS type,
   explicit sync config, metadata load, template lookup and empty-node health.
   Then run one owned create/acknowledged file+SQL/pause/resume/clean-reboot case,
   followed by a separately coordinated owned abrupt-loss case. Verify latest
   metadata/current disk and latestSQL, not merely template or baseline state.
7. Only after those pass may this candidate be considered for production.
   Rollback stops admission first and preserves newly written persistent metadata;
   switching to the old volatile config is not a safe rollback for new guests.

## Validation and limits

The native Go candidate was built on2026-09-25 with required patches0006/0007/0008
and prior network/retention patches. Full affected non-privileged Go race suites
passed (durability, utils, CubeStore, cubes, cubebox service, server config and
Cubelet command); selected XFS catalog/pause/storage recovery race tests passed.
Both config-renderer tests passed. Native CGO and all three exact protected BPF
objects were verified in the binary. The old source and installed worker binary
remained unchanged. See [results/2026-09-25/summary.json](results/2026-09-25/summary.json)
for hashes, commands, limits and the baseline/full-storage failures.

Candidate SHA256:
`a61a43c531b8e7854dd8ee064db0d1e160d42fcb4d50e7c4b3c98d447e63e75a`.
It is not installed or power-loss validated. Read the concrete
[empty-worker enrollment review](enrollment.md) before any deployment.
For a small latency check, use a disposable XFS metadata directory in the fresh
worker after a CPU handoff. Measure1000 sequential64KiB CubeStore transactions
with durable defaults (report p50/p95 and total), plus32 create/pause/resume
metadata operations on one owned guest. Compare a separately isolated baseline,
not live metadata, and report disk/cgroup/Go version. Async Bolt timings are not
an acceptable replacement for the durability gate. Whole-worker loss must be
coordinated after independently exported synthetic latestSQL evidence.

Patch0007 adds the reviewed Go pause barrier and failure/publication ordering.
Real acknowledged-pause/power-loss acceptance remains open, including native CoW
semantics. Patch0006 alone does not solve pause ordering.

## Build and inspect without deploying

`build-candidate.sh` copies the previously built retention candidate, preserving
both network patches and exact generated BPF/native libcubecow inputs. It applies
0006, 0007 and 0008 in an independent directory, runs the affected CI suites and
builds a native CGO Cubelet. It never installs a binary, edits the running config,
or starts/restarts a Cube service. Test logs and source/input/binary hashes stay
inside that private candidate directory. A fresh run requires a new empty
candidate directory; a failed attempt is retained rather than overwritten.

Use the transient-unit bounds from the recorded build: CPUQuota=200%,
MemoryMax=2G, TasksMax=256, RuntimeMaxSec=1200, PrivateNetwork=yes,
PrivateDevices=yes, ProtectSystem=strict, NoNewPrivileges=yes, and an empty
CapabilityBoundingSet. Only the candidate and Go build-cache directories are
writable; live `/data/cubelet` and Docker sockets are inaccessible. Module
versions/checksums are pinned; cache any missing build dependencies before the
network-isolated run. No Go module updates are authorized by this script.

The full storage race suite was attempted. Its loop-mount integration tests
require devices deliberately unavailable in this unit. The pinned baseline also
has S3 initialization and template-format-pool background/global-config races.
These are recorded failures, not a full-suite pass. The required XFS catalog,
pause, storage-retention and recovery tests are selected separately, using an
explicit synthetic node identity (no actual interface changes).

0008 fixes the independently reproduced CubeBox status-reader race: GetStatus no
longer rewrites a shared legacy pointer on each read, fallback initialization is
serialized, and persistence copies select the canonical synchronized status.
The existing concurrent producer test now joins producers before closing their
DB, and dedicated concurrent reader/copy tests cover the publication path.

See [pause-barrier.md](pause-barrier.md) for pause ordering, charged-state policy,
and the remaining real power-loss and native CoW/Rust gates. Passing a build is
not approval to enroll the node or advertise automatic same-ID cold recovery.
