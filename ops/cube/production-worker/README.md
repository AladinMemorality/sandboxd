# Fresh production-worker preparation

This directory records the fresh worker provisioned on2026-09-24. Ubuntu image
signatures and release/container digests were verified; the new XFS data disk,
fresh credentials and Cube control services were installed. Initial database
startup failures were repaired with a fresh NVMe-backed database, preserving the
two failed initialization volumes. No tenant/customer guest or production route
has been enabled by these provisioning steps. The candidate worker build requires
separate network and workload acceptance; installation is not that acceptance.

## Placement and current capacity

Read-only VPS observations on 2026-09-24 are preserved in
[observed-host-2026-09-24.json](observed-host-2026-09-24.json):

| Resource | Observed / proposed |
| --- | --- |
| VPS | AMD Ryzen 5 3600, 6 physical / 12 logical CPUs, about 62 GiB RAM |
| Headroom at inspection | About 45 GiB available RAM; this is an observation, not a reservation |
| Host OS / hypervisor | Ubuntu24, kernel `6.8.0-136-generic`, QEMU `8.2.2`, KVM AMD nested enabled |
| Root storage | Healthy RAID1 ext4, about 1.5 TiB free |
| NVMe storage | Separate unmirrored ext4 partition, about 512 GiB free |
| Existing benchmark VM | 4 vCPU / 12 GiB, root154 GiB and data100 GiB; about25 GiB data free |
| Selected new VM | 12 vCPU / 40 GiB, root120 GiB on RAID1, data320 GiB on NVMe |
| Preserved per-project allocation | Two CPUs / 2 GiB, as observed across the current61-project fleet |
| Selected guest budget | At most12 active **including waking/creating**, 30GiB host memory quota, 28000m CPU quota |

The selected limit requires the controller admission ledger and burst tests before
activation. Each guest retains2CPU/2GiB; pinned Cube adds approximately0.3CPU and
112MiB host overhead. The40GiB VM reserves10GiB outside the30GiB guest quota.
CPU quota28000m explicitly oversubscribes12 VM vCPUs; outer CPUQuota1000% leaves
about two host logical CPUs for the platform. MemoryHigh42G/MemoryMax44G include
QEMU overhead. Twelve is a combined running/creating/waking cap, not twelve plus
extra wakes. All61 guests cannot run simultaneously on this worker. Paused private
homes, snapshots, image caches and memory snapshots still consume storage.
Retain the old Docker data and separate recovery archives during migration.
The historical 13.3 GB workspace inventory excludes some private-home, image,
database and future snapshot growth; it is not the final disk requirement.
Refresh the drained fleet inventory before execution. NVMe performance does not
provide redundancy; maintain independent/off-host backups and restore proof.

The benchmark VM must remain separate. It contains experimental worker patches,
DNS/TPROXY trial configuration, mutable test images, and unrelated paused guest
`6d96feb3819a41aabbcb04e3058b056b`. Do not start/delete/migrate that guest or copy
the VM, its disks, credentials, database, Redis, pinned BPF maps or configuration
into production. Its running Cubelet SHA is
`7bc2c2e7290e1ba62d0648441388ac11da9f046134683f44da2f8d20a346fa6b`;
the [security handoff](../security/results/2026-09-23/HANDOFF.md) records incomplete
acceptance. A matching binary hash does not make that inherited state production-ready.

Direct-host installation is technically possible with KVM, but is unsuitable
for the current shared host: upstream one-click changes DNS, cgroups, eBPF,
TPROXY/networking and fixed service paths/ports. It would share the production
host with Caddy, Docker tenant guests, PostgreSQL and unrelated services. The
dedicated VM contains those changes and can be rolled back independently.
Nested virtualization has a performance cost; existing benchmarks establish
feasibility, not parity with a dedicated physical Cube worker.

## Pinned inputs and worker requirements

Use upstream source `31d911e430fdf8a8879bd062b8e78066c1a8e89d`, tag `v0.7.1`.
The [official release](https://github.com/TencentCloud/CubeSandbox/releases/tag/v0.7.1)
AMD64 archive has SHA256
`a516ed71e90e273d03f053a26bbbf3932e0d747387fbccd25ec76f89b20bc0a1`.
The existing outer-host `/opt/baarcha-bench/cube-20260917/cube-release.tar.gz`
matched that hash and is reusable **as an immutable release input only**.
The adjacent outer source checkout is commit `286960e052263db98672c12f2f3994db25d551d6`,
so do not use it as the pinned build source. Start a fresh source checkout.

The archive's [release manifest](upstream-release-manifest.json) records:

| Component | SHA256 |
| --- | --- |
| Ordinary guest kernel | `3ea4a05e2b74e1b4baaafb21875c9ce0dde8fdfc435723f6c39a98735a64a5a4` |
| Guest OS image | `8262a7eb9cacf423ea9c03f6bf3ae8921b00684f4a18022fe11792b37ea69228` |
| Agent ext4 image | `deb7abfb9003f6b7d7de803ef2086c984ea8d8be0ea6dc7ff96c8ff7b0057df3` |
| Original unpatched Cubelet | `594a4d8c68395d0af744b6e86d43456889c0a6800bfed475779b5ecb1a7b8f00` |

Do not confuse the **worker Linux kernel**, the packaged **microVM guest kernel**,
the packaged **guest OS/agent images**, and Baarcha's **OCI application templates**.
They have independent identities. The tested nested worker currently runs
`6.8.0-139-generic`; a fresh worker must record and pass tests on its actual kernel.
Use Ubuntu24 AMD64 with `/dev/kvm`, nested virtualization, `/dev/net/tun`, BPF
support and mounted bpffs, cgroup CPU/memory controllers, systemd and Docker.
Use an ordinary KVM guest kernel (`CUBE_PVM_ENABLE=0`), not experimental PVM.
`/data/cubelet` must be on XFS with `reflink=1`; the proposal mounts the entire
new data disk at `/data`. Native CGO `cubecow` is necessary for pause/snapshots.

The reusable generated plan deliberately leaves worker selection unresolved;
the executed installation manifest pins the installed candidate hash. Rebuild/review both candidate network patches against pinned
source, configure all protected management addresses and the actual resolver,
then complete attached-worker security/lifecycle acceptance. Plain upstream
must not silently replace that decision. Keep guest NIC egress deny-all and use
the authenticated host-initiated reverse broker; registry/model connectivity is
not a reason to enable broad guest networking or copy global keys into guests.
The new worker's template/guest tests must exercise legitimate inbound replies
as well as denied destinations, reload, pause/resume and restart recovery.

Select a published Ubuntu24 cloud image and verify its digest against Ubuntu's
signed checksums. Record the exact image URL, SHA and resulting kernel. Also pin
the container digests for MySQL, Redis, MinIO, CoreDNS, CubeProxy, lifecycle
manager and CubeEgress: the release includes several mutable image tags. The
generated plan explicitly leaves these unresolved; do not describe the whole
installation as byte-reproducible until they are recorded.

## Prepare inert artifacts

Create a fresh operator SSH key outside the repository, then render files:

```sh
ssh-keygen -t ed25519 -f /private/operator/cube-worker-01-key -N ''
python3 ops/cube/production-worker/render-staging.py \
  --ssh-public-key /private/operator/cube-worker-01-key.pub \
  --output /private/operator/cube-worker-01-staging
```

The output directory must not exist. It is0700 and files are0600. It contains
cloud-init metadata (no package installs or commands), an inert systemd VM unit,
a JSON placement plan, and `cube-install.env` with fresh random API/database/
Redis/MinIO/JWT/template-callback credentials. Nothing is printed except status.
Never commit, log or reuse that credential file across environments. Repeated
rendering refuses to overwrite an existing directory.

Read-only placement refresh, using the coordinating agent's existing SSH master:

```sh
ssh -S /private/tmp/baarcha-cube-cutover-ssh -oControlMaster=no -oBatchMode=yes \
  root@65.108.225.153 'python3 -' \
  < ops/cube/production-worker/preflight.py > /private/operator/worker-host-review.json
```

## Reviewed provisioning sequence (steps1–3 executed for the fresh outer VM)

1. Reserve the proposed resource budget and validate no conflicts on loopback
   SSH20222, API20300 and proxy20080. The old benchmark SSH19222 is unrelated.
   Stage verified release/cloud images, fresh key and rendered files privately.
   Verify the release with `sha256sum` before extraction. Never execute a script
   from the altered benchmark installation.
2. Create fresh directories `/opt/baarcha-cube/worker-01` and
   `/mnt/nvme/baarcha-cube/worker-01`, rejecting preexisting directories/disks.
   Convert the verified Ubuntu cloud image to a **standalone** qcow2 root image
   (no benchmark or shared writable backing file), then resize it to120G:

   ```sh
   qemu-img convert -f qcow2 -O qcow2 VERIFIED_UBUNTU_IMAGE /opt/baarcha-cube/worker-01/root.qcow2
   qemu-img resize /opt/baarcha-cube/worker-01/root.qcow2 120G
   qemu-img create -f qcow2 /mnt/nvme/baarcha-cube/worker-01/data.qcow2 320G
   cloud-localds /opt/baarcha-cube/worker-01/seed.img STAGED_USER_DATA STAGED_META_DATA
   ```

   These commands allocate/modify only the new disks and must run after the
   nonexistence checks. Sparse virtual sizes do not reserve physical space.
   Review host free-space alarms and per-project quotas before admission.
3. Review/install the generated unit and start only the fresh VM. It uses user
   networking, no shared host directory, no host bridge/TAP and only loopback
   forwarded ports. It runs in the foreground under systemd with40GiB guest RAM,
   44GiB hard process memory limit, no swap, twelve CPUs and no automatic restart.
   Record SSH host key from the VM console before trusting the new endpoint.
4. Inside that fresh VM, validate hostname `baarcha-cube-worker-01`, disk serial/
   size and `lsblk`; confirm `/dev/vdb` is the newly created empty320GiB disk.
   Install Docker/systemd tooling, XFS utilities and BPF tools using reviewed
   repositories, recording package versions. Format **only that new guest disk**
   with `mkfs.xfs -m reflink=1 /dev/vdb`, mount `/data` by filesystem UUID and
   verify `xfs_info /data`, `/dev/kvm`, bpffs and enabled cgroup controllers.
   These are VM-local changes; do not format or remount any outer-host device.
5. Extract the verified release in a new VM-local directory. Supply the generated
   `cube-install.env` as its private `.env`, plus pinned dependency-image overrides.
   The upstream `./install.sh` **automatically starts/enables services**. It has
   no inert install step in this runbook. Run only in the fresh isolated VM, with
   no production clients connected. `./smoke.sh` then checks installation health.
6. Install the independently selected patched Cubelet according to its reviewed
   replacement/recovery procedure before creating acceptance guests. Record the
   release manifest, patched binary, actual attached programs, resolved routes,
   DNS and all configuration identities. No benchmark drop-ins/Corefile/TPROXY
   rules are copied. Rotate/review any automatically created admin access.
7. Build/load/register fresh credential-free application templates and complete
   isolated acceptance before wiring the control plane. Use a private registry
   reachable by this new worker, not the benchmark VM's registry or snapshot store.

## Management transport and production routing

The prepared unit binds API20300 and CubeProxy20080 to **127.0.0.1 only**. Those
are usable by a host-side acceptance CLI; they are not usable as container
`127.0.0.1` URLs. The current sandboxd container is `172.19.0.3` on shared
`sandboxd_net` (gateway `172.19.0.1`), which also carries tenant Docker guests.
Do not expose management on that shared gateway or bind it publicly.

Before production, implement one reviewed controller-only transport: either a
dedicated management Docker network with explicit host-ingress filtering for
that network/controller, or a Unix-socket bridge mounted only into a management
sidecar/controller. Merely creating another bridge does not guarantee that host
INPUT is inaccessible from tenant bridges. Verify reachability positively from
the controller and rejection from ordinary Docker and Cube guests. Preserve
CubeProxy's intended Host header; never forward the management API key to app
ports. This transport is **not configured by the staging renderer**. Keep stable
Baarcha preview domains and existing owner cookies; do not publish raw Cube
management or supervisor endpoints. Root-agent integration owns final URLs.

## Application templates and cutover

The source ABI image is
`sha256:9ec445151344a7baa295b085bebe1a0352ab930601fe0d9ed2bf1283bbaa6678`
(Node22.23.2, Python3.13.5, pnpm10.34.4, glibc2.41). It still exists on the host.
The [composed v4 matrix](../../../docs/cube-pilot-results/composed-preset-matrix-2026-09-24/README.md)
records eight tested credential-free preset images. Their **template IDs belong
to the benchmark cluster** and cannot be reused in a fresh database. Transfer
reviewed OCI images by immutable identity, or rebuild with the final selected
runtime source and rerun acceptance; do not export an owner's container image.
Use bootstrap port49983, **two vCPU/2048MiB** preserving the observed production
allocation, fresh per-instance credentials and pause idle policy. The historical
fixture used1vCPU/1GiB and a10GiB writable layer; it is not the production sizing
contract. Recheck each project's storage quota and rerun acceptance at preserved
CPU/memory limits. Validate
react-pro, marketplace, react-vite, nextjs, node-express, fastapi, worker and
opt-in node-postgres independently. Register fresh IDs and record their images,
runtime hashes and resource profiles in the final trusted preset mapping.

Production admission and whole-fleet movement remain separate steps. Do not
change `SANDBOXD_CUBE_ENABLED`, runtime URLs or `SANDBOXD_CUBE_ROLLOUT` while
provisioning. After new-worker acceptance, follow the
[migration journal](../../../docs/cube-existing-project-migration.md): drain new
work, wait for active tasks, take the maintenance fence, refresh the **entire**
fleet plan including MyHomeTroc, take consistent independent backups, migrate
with verified private-home/workspace/database/config/history checks and then
validate previews, real model/bridge operations, publish/remix and owner data.

## Rollback boundaries

Before admission, stop only the new VM after quiescing its guests; production
Docker routing/data remains unchanged. Retain staging/disk evidence for review.
The generated unit does not automatically restart after failure. Before any
normal VM shutdown, pause/drain Cube guests, checkpoint cluster data and issue
QMP `system_powerdown`; `systemctl stop`/SIGTERM or OOM is crash recovery, not
a consistent backup. Test crash/restart recovery on disposable data first.

After migration starts, toggling `CUBE_ENABLED=false` is **not** a rollback:
durable Cube bindings do not become Docker rows. Use the per-project journal's
rollback/recreation path, preserve newer writes, restore matching configuration,
home/database/history and verify Docker readiness before changing the binding.
Retain source Docker volumes, SQLite/key/config backups and export manifests
through the agreed retention period. Copying an actively written qcow2 file is
not a consistent backup. Production storage/key backups must be independently
restorable outside this physical host.

## Local validation

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s ops/cube/production-worker -p 'test_*.py' -v
```

The tests check credential separation/file permissions, overwrite refusal,
injection/private-key rejection and private listener defaults. They do not
establish tenant network isolation. The separately authorized boot created only
the fresh outer VM, private credentials and new disks; Cube installation, tenant
workloads and production routing remain separate.

## Applied startup repairs and selected quota

The initial MySQL volume on the VM's RAID-backed root disk did not initialize
within upstream's80-second readiness loop. After preserving that failed volume,
a second volume initialized but CubeMaster migrations hit a5-second SQL timeout.
Both old volumes are retained; neither contains customer data. The final fresh
volume `support_cube-production-mysql-data-nvme` binds `/data/control/mysql` on
XFS/NVMe using [mysql-nvme-volume.patch](mysql-nvme-volume.patch). Set
`MYSQL_VOLUME=cube-production-mysql-data-nvme` in the private installation env.
Create the directory before starting MySQL; its image assigns the required owner.
The [readiness patch](mysql-startup-timeout.patch) increases the probe window to
600seconds and [service override](mysql-initialization.conf) sets900seconds.
The NVMe database initialized in approximately29seconds, all migrations passed,
and upstream `quickcheck.sh` returned0. Do not rerun the upstream installer over
these local repairs without reapplying/reviewing them.

Lifecycle manager and artifact distribution require CubeMaster to accept its
node IP10.0.2.15 as well as loopback clients. Set CUBEMASTER_HTTP_BIND=0.0.0.0
and the generated CubeMaster/conf.yaml server.http_bind accordingly; changing
the environment alone after installation does not rewrite this generated field. This is internal to the SLIRP-isolated VM; only API3000/proxy80/SSH22
are forwarded to outer **loopback** ports20300/20080/20222. No host route or
firewall change was made. CubeOps remains loopback-only.

[worker-quota.yaml](worker-quota.yaml) records the approved values. Apply only its
quota fields to the installed dynamic configuration, preserving internal endpoint
and other generated fields. Candidate Cubelet is SHA256
`53b09684b59bf1b5609742c1c6b3e17fb22b23ef2320000ddf49716df713346d`.
The original release binary and private configuration were backed up inside the
fresh VM at `/root/cube-production/stock-before-candidate` before replacement.
No customer guest, production route or `network_verified` flag was enabled.

## Benchmark retirement and recovery

The unrelated guest `6d96feb3819a41aabbcb04e3058b056b` remained paused at snapshot
`snap-4d5641643a124f35b70f66dc` throughout retirement. Seven persistent artifacts
were hashed, including its2GiB memory image and2,000,000,000-byte writable disk.
Admission/writer services were quiesced, MySQL dumped consistently, Redis SAVE
verified, and private config/key/Bolt/pause metadata archived. Nine backup files
were copied to and hash-verified under the outer host's private directory
`/opt/baarcha-bench/cube-20260917/retirement-20260924/` before graceful OS shutdown.
The full root/data qcow2 files remain at their original paths; recorded disk
identities and the exact QEMU commandline are beside the backup. No guest was
resumed or deleted. A first overly broad archive attempt was stopped and its
incomplete output removed; the final scoped metadata archive is complete.

To recover the old benchmark, first drain and gracefully stop/reduce the new40GiB
worker so its12GiB predecessor fits physical RAM. Use the retained original QEMU
arguments and disks, then start previously quiesced control services as needed.
Never boot both with full memory reservations or attach either disk to two VMs.
The old VM's experimental network state is retained for recovery only, never
copied into the new production worker. Existing Docker owner data remains the
separate production rollback path.

## Trusted template preparation and disk filter

The eight immutable registry digests are recorded in
[registry-images-2026-09-24.json](registry-images-2026-09-24.json). The registry
binds only127.0.0.1:5000 inside the worker. Its official image digest is recorded
in [installation-2026-09-24.json](installation-2026-09-24.json).
[register-templates.py](register-templates.py) runs inside that fresh worker,
requires at least80GiB actual/data free before **each** sequential trusted build,
and refuses unexpected guests or unfinished previous job records. It preserves
2CPU/2048MiB/10GiB, selects no Cube CA and denies all direct IPv4 egress. Template
build probes use49983/health; application exposed ports are3000/3001/3031, with
3031 restricted to controller access in the platform preview implementation.

The upstream Master config omitted the `disk` scheduler filter. Add `"disk"` to
`scheduler.filter.enable_filters` and set `scheduler.disk_usage_max_percent:65.0`
in `/usr/local/services/cubetoolbox/CubeMaster/conf.yaml`. The original private
file is backed up at `/root/cube-production/cubemaster-before-disk-guard.yaml`.
Restart CubeMaster after the edit. At240GiB capacity this nominally stops new
placements near84GiB remaining, about36GiB above the48GiB requested reserve.
That extra margin covers24GiB for twelve2GiB memory snapshots plus one10GiB
writable allocation. It is **not a strict48GiB minimum**: heartbeats are cached,
existing tenants may write concurrently, builds have temporary artifacts, and
all61 allowed10GiB layers cannot fit simultaneously. Twelve already-active
containers could add up to120GiB of writable data independently of scheduler
admission. Use authoritative before/after statfs checks for the sequential
migration, retain source data, and stop on insufficient measured capacity.

## Applied data growth

The initial240GiB disk was expanded to320GiB after all eight templates were ready,
with no customer guests present. QMP query-block identified only `virtio1` at
`/mnt/nvme/baarcha-cube/worker-01/data.qcow2`; its inode was rechecked and the guest
UUID matched793c3349-db9c-4815-9842-989ed484f1f8 before mutation. QMP block_resize
changed the virtual capacity to343597383680bytes, the guest observed that size
automatically, and `xfs_growfs /data` expanded XFS online. No reformat, reboot,
route change or old-disk mutation occurred. See
[data-growth-2026-09-24.json](data-growth-2026-09-24.json) for UUID, capacity and
health proof, including an overly strict free-space-delta assertion that failed
after successful growth; independent capacity and health checks passed.

After growth the filesystem had319.88GiB usable capacity and284.9GiB free. Outer
NVMe still had479.4GiB free; filling the new disk to its full320GiB would leave
approximately192GiB outer headroom at this observation. Keep the65% filter: its
cutoff becomes208GiB used, leaving112GiB. Current~35GiB baseline plus122GiB of
full61×2GiB memory snapshots plus25GiB owner data is about182GiB, below that
cutoff, before additional history and temporary growth. This does not reserve
610GiB of per-guest writable maxima. Do not truncate the grown qcow2 to roll back:
XFS cannot shrink; recovery retains this disk or restores a complete backup.

## Native storage-retention candidate

`0005-retain-unresolved-storage-metadata.patch` retains the complete DB/fallback
record when one referenced CoW object is missing. Reads still fail closed; an
operator must repair or recover the data. It does not cold-start a crashed guest.

During an exclusive empty-worker handoff, copy that patch and
`build-retention-candidate.sh` into `/root/cube-production`, then run:

```sh
systemd-run --unit=cube-retention-candidate-build --wait --pipe \
  -p CPUQuota=200% -p MemoryMax=2G -p TasksMax=256 -p RuntimeMaxSec=900 \
  /bin/bash /root/cube-production/build-retention-candidate.sh
```

The script requires the exact fresh-worker hostname, pinned upstream commit,
original tested Cubelet binary hash and zero sandbox inventory. It refuses to
overwrite its candidate directory. It copies the already patched source, checks
both network patches, records every network file and the native CoW library,
applies only the retention patch and builds with CGO enabled. The worker-specific
generated protected-address header is checked by exact before/after hash instead
of matching the generic patch's placeholder header. No BPF regeneration occurs.
The original source and installed binary must remain byte-identical after the
build. This command never installs or restarts a service; deployment and crash
acceptance require a separate coordinated step. Evidence is recorded in
`storage-retention-candidate.json` and the candidate directory.
