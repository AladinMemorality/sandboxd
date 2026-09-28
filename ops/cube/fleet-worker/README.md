## Transfer and placement rule (2026-09-27 correction)

Existing runtimes stay on their durably assigned worker. Reuse that worker's
workspace and dependency cache while it remains available. Do not rebalance an
existing project merely because another worker has more capacity. If the assigned
worker cannot serve it, the destination must fetch an immutable project revision
and required data directly from S3, prepare dependencies locally, pass readiness,
and only then take over routing. Prevent concurrent writers with a durable lease
and fencing; a timeout alone is not proof that the original writer has stopped.

**Never send workspace archives, dependencies, rootfs images, or restore data over
the VPS–B200 management link.** Control messages, routing and bounded observations
can use it. Do not use a controller-side download-and-forward fallback.
Permanent deployment objects contain source, lockfiles, recipes and separately
managed project data/secrets; dependency caches stay on the assigned worker.

The current controller already pins existing runtimes to their stored worker.
Automatic S3 recovery and safe routing takeover are not yet globally enabled.
The 100-copy test does not constitute acceptance of automatic failover.

`publish-test-copy.mjs`, `import-test-copy.py` and `broker-test-imports.py` implement
a temporary test-only transfer: source host → encrypted S3 object → destination
worker, then loopback import and digest verification. The broker transports at
most 32 KiB of scoped job metadata per import, never archive bytes. Worker-local
verified archives are reused. Full dependency trees are included only to preserve
exact test copies; `delete-test-copies.mjs` deletes the recorded S3 objects after
the owned guests have been removed. These are not permanent deployment objects.
The original direct-copy test was stopped with customer bindings unchanged.

## Customer placement and capacity validation — 2026-09-28

68 customer projects have passed S3 relocation, exact content checks, previews and
pause/wake acceptance on B200. Three customer web projects remain on Cube/VPS;
the original inventory also includes two Minecraft tunnels and an old operator
fixture. IDs, URLs, ownership, visibility and task history are preserved.

The second live test proved exactly **96 B200 + 4 VPS running VMs**, both through
the native API and independently through worker-local containerd tasks. All71
customer preflight HTML/entry-asset checks passed. A fresh Avocall browser at100
rendered in11.718s without JavaScript errors or a stuck opening overlay. However,
the first full-load page round timed out, so this is **not completed100-serving
acceptance**. The previous50 lightweight-page test remains the completed result.

The bottleneck investigation found a whole-worker inventory scan inside every
native single-sandbox Get, serialized per worker by CubeMaster. Patches0012 and
0013 remove this redundant scan and make Connect(running) actually renew its TTL.
The controller now uses the authoritative TTL to avoid unnecessary renewals and
gives maintenance mutations their full lifecycle budget. The offline admission
CLI uses the same fleet placement checks as the controller. Native149 tests and
focused Linux controller/recovery/admission tests pass. Deployment and subsequent
load acceptance are recorded in the rollout handoff; do not infer their completion
from this source description. The second run's pending operations require fenced
reconciliation before retrying the serving test.

B200 Cubelet SHA:
`d4c3f2813cd77979d6a456a31eb7615195b0a01d847d91b50f312faea2a12650`.
JavaScript gzip is live, reducing the measured lucide module from1,364,318 to
213,380 bytes with identical decoded content. GPU access remains absent. See
`PROJECT_RELOCATION.md` for the protocol and private evidence locations.

## Previous checkpoint — 2026-09-27, 20:33 UTC

The 100-copy attempt failed acceptance on creation/recovery errors. It did not
reach the 100-running peak. All 100 temporary apps and their native runtimes have
been removed, no test admission remains charged, all 74 customer bindings are
unchanged, and production readiness passes. Worker test caches and source
snapshot archives are removed. Deletion of all 74 encrypted S3 object versions
is blocked by `AccessDenied`; private receipts are retained for exact cleanup.
See `results/2026-09-27-capacity-100/README.md` for evidence and limitations.

Current admission is configured for **96 B200 + 4 VPS**. The B200 worker now has
224 vCPU, 224 GiB guest RAM, a 240 GiB process ceiling and a 2 TiB XFS disk;
Cubelet quota is 221000m CPU / 208 GiB RAM. It has no GPU access. Boot pin:
`aa712ab9-e328-46c7-acdc-1770e34bec95`. The production controller is
`f24219dcefc79e46b716b2a52cc417496514103669d5cc657a961ad8b9feb43e`, image
`sha256:9a5a15c19687a6d407a071f54cf51a3f5c17aa86a71597280e547326daa6143e`.
The five-minute preview readiness cache is deployed. Fifty simultaneous
sandboxes remain the largest completed acceptance test. The sections below
record that earlier acceptance and initial worker setup, not current sizing.

# B200 worker VM canary

## Fifty concurrent sandboxes verified — 2026-09-27

The production fleet was tested with **4 VPS +46 B200 active sandboxes**, each
retaining2 vCPU/2 GiB. The real canonical-API test passed at18:12UTC:50 native
running/placement proofs,150 successful private-page checks in three rounds,
51st-create refusal, and B200 pause/wake (11.29seconds). Continuous background
visitors kept fixtures active under the saved120-second idle policy. All51 owned
apps were removed; all74 original bindings remained intact. Customer routing
and background services were restored. Public HTTPS on a subsequent B200 canary
returned200 with signed access and401 without it; owned cleanup passed.

Evidence is in `results/2026-09-27-capacity-50/`. The workloads were lightweight
Vite pages, not fifty concurrent builds. At fifty running, the B200 hypervisor
used54.79GiB of its168GiB ceiling and37.78% Docker CPU (about0.38CPU cores).
The inference container remained running. No GPU is exposed to the worker.
The native connection-renewal path was slow under concurrent requests: the
three-round results record the timings. A same-page pair took22.21s for renewal
and0.282s while cached. The subsequent five-minute readiness cache change passed
focused race tests and is now deployed; the independent idle policy is unchanged.

At this earlier acceptance, the production controller image was
`sha256:918d1cf4b1156c11a593dc7e2e90d19e21127ac8583b8501630d311d2e9d4063`,
with migration0038, durable worker partitions and explicit node placement.
The controller container was
`5912348b37866c34829724da380b44aac2070ac90261e666534fdbead8ebfa03`.
Automatic relocation of existing paused projects and worker-aware automatic
reboot recovery remain incomplete. The subsequent 100-copy stress test failed
acceptance, as recorded above.

The empty B200 pilot was cleanly stopped and expanded to 112 vCPU, 160 GiB guest
RAM, a 168 GiB process ceiling, and a 1 TiB XFS data disk. The stopped pilot
container and pre-change metadata are retained. `start-canary.sh` now accepts
the explicit `capacity-50` profile; `resize-empty-worker.sh` refuses unrelated
containers, nonempty/unreviewed disks and insufficient backing capacity.
No GPU device, host Docker socket, inference data or host root is exposed.

Cubelet is installed and running with the same production patched binary,
SHA256 `de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b`.
Persistent metadata databases are verified on XFS. The worker node
`10.254.240.2` is registered healthy and enabled for production admission. Quota is
106000m CPU/112 GiB RAM; active capacity must still be enforced by the controller.
VPS creates are explicitly pinned and live fleet acceptance has passed. Worker boot pin: `10a17f4e-b974-4837-971a-882b15e7c337`.

The private control forwarding unit exposes CubeOps, CubeMaster, TemplateCenter,
Redis and lifecycle-manager only to the worker WireGuard peer. A worker-local
proxy uses the exact existing production image digest and binds only to
`10.254.240.2` (HTTP28080/admin28082). `prepare-proxy.py` renders private inputs;
the cluster currently has an empty admin token, so the admin listener must remain
behind the authenticated WireGuard peer and guest hard-deny policy. The private
configuration is never a repository artifact. Container restart is disabled
during acceptance. Compute target startup remains disabled at boot.

Template distribution over the private relay is slow. `publish-artifact.mjs`,
`cache-templates.py` and `fetch-artifact.py` prewarm immutable rootfs artifacts through encrypted S3
objects with ephemeral transfer keys, bounded parallel downloads, GCM validation,
and the authoritative raw filesystem hash. They copy no Cube metadata, customer
workspace, AWS credential, or controller master key. Cube still owns template
registration. All nine encrypted artifacts have been published; worker cache
verification and native registration have completed for all nine templates. The source cache lives separately at
`/data/cube-fleet-artifact-cache`; Cube owns its disposable copies under
`/data/cube-fleet-rootfs`, referenced by the normal `cubebox_os_image` path.
Retries delete Cube's copy, so merely prewarming that directory is insufficient.

`artifact-cache-server.py` listens only on loopback18089. Every download first
requires a successful one-byte authenticated range request to the canonical
CubeMaster; the local file must still match its verified generation. It never
logs credential-bearing URLs. Cubelet's `cubemaster_http_addr` points to this
cache, while its other control connections retain their normal private targets.
Install `cube-fleet-artifact-cache.service` in the compute target, with its script
under `/usr/local/lib/baarcha-cube-fleet`. The service has read-only filesystem
access and can connect only to loopback and the private coordinator.
All nine B200 replicas reached READY before live fleet acceptance. The
React/Vite job was `421d7d70-41fa-402a-aed7-e22278dc9601`.

The separate native application canary passed at17:08UTC. It created a VM on
B200 in1.85s, reached its authenticated supervisor in4.52s, imported a small
Node application and served its nonce-bearing page in11.68s. The real reverse
channel used a deny-all egress policy; the page verified no NVIDIA device.
Owned cleanup and empty B200 inventory passed; B200 was re-cordoned. Evidence:
`/opt/baarcha-bench/cube-fleet-20260927/native-canary-04/result.json` on the VPS.
`native-canary.py` requires VPS-only pinned production admission, an empty B200,
fresh storage observations and operator locks. Its small Go channel helper is
`cmd/cube-fleet-canary-channel`. This test does not validate production fleet
admission, fifty concurrent workloads, pause/wake, AI tasks or S3 project moves.

`storage-probe.py` supports fixed forced-command SSH keys for read-only inner and
backing-filesystem measurements. The observer's `--worker b200-01` profile uses
separate pinned identities, sequence state and output directory, with coordinator
monotonic time. Physical-host SSH currently takes about a minute after
authentication. A dedicated root-private persistent SSH connection, restricted
on the host to the read-only probe, lets periodic observations finish normally.
Install `cube-fleet-observer-transport.service` and the B200 observer service and
timer. On transport loss observations fail closed; never extend freshness to
hide a slow or unavailable probe.

Both probes now pass: the B200 backing filesystem UUID is
`9adc3783-c99f-438d-8826-542c0da5e63e`, with about4.9TiB free at enrollment.
The guest has about946GiB free. Preserve observer sequence state independently;
the configured `outer_boot_id` and monotonic clock belong to the VPS coordinator.
Its separate guard and read-only observation mount are now enrolled in the
controller fleet config.

`controller-fleet-release.py` performs the controller-only fleet enrollment
under the existing deployment and worker locks. `accept-fleet.py` creates owned
private apps through the canonical API and verifies distinct pages, native
running state and placement, overflow refusal and pause/wake. The saved admin
idle timeout is120seconds, so the harness sends continuous visitor requests
while it creates the remaining fixtures. Initial pages may take up to60seconds
to become ready; steady-state checks require successful responses.

The `finish-fleet-release*.py` and `reconcile-fleet-test-cleanup.py` files are
incident-specific continuations pinned to exact process/container generations.
They are an audit record, not reusable deployment commands. Continuations retain
the same open-file-description locks without an unlocked gap. The cleanup repair
is restricted to two owned test fixtures with acknowledged native deletion,
404 responses and complete native inventories; it retains storage grants
conservatively and preserves the controller generation and all customer bindings.
Never generalize a native500 response into proof of deletion.

The sections below record the earlier pilot and initial network work.

This is provisioning for a new isolated worker VM, not production admission.
Customer workloads and GPU devices must not be attached during this stage.
The Cube controller remains on its existing four-slot VPS admission path.

The B200 inference host is shared. Cube changes DNS, networking and service paths
inside its worker OS, so the candidate runs in a dedicated KVM VM. Only `/dev/kvm`
and the private directory containing the new virtual disks are passed into the
hypervisor container. No GPU, host Docker socket, model directory or host root
is passed through. The only published port is SSH on B200 loopback `24222`.

Initial bounds are eight CPU threads, 32 GiB guest RAM, a 36 GiB process memory
ceiling including hypervisor overhead, no swap, and 512 processes. Root and data
disks are new standalone sparse qcow2 files on `/raid`; their virtual sizes are
64 GiB and 256 GiB. Sparse sizes do not reserve physical space. These bounds
are for the worker pilot; they do not establish a customer sandbox capacity.

## Inputs and preparation

- The tooling Dockerfile uses the pinned Ubuntu digest and installs QEMU tooling
  in an image, without installing packages on the shared host. Record the final
  image ID and package inventory. Package repositories are resolved at build time;
  an arbitrary later rebuild is not promised to produce identical bytes.
- B200 tooling image built on 2026-09-27:
  `sha256:7fa680c70d6e663af59f8ed63514a0eb5a5ecd9f16ebc3553906a3e1eeb2f9f5`.
- Cloud image:
  `https://cloud-images.ubuntu.com/noble/20260911/noble-server-cloudimg-amd64.img`.
  SHA256 `612b2c0cc1bc413a6cb8c38fd611794caf0f2b436c50013d8b3794db12ad7354`.
  This is the image previously checked against Ubuntu's signed checksums for
  the VPS worker; the fresh B200 download was checked against that exact digest.
- Dedicated private NVMe directory `/raid/baarcha-cube-worker-b200-01`, owned by
  the existing operator UID1013, mode0700. It was created empty; no existing
  disk, model, workload or filesystem was reformatted.
- The fresh cloud-init seed contains only hostname, a new operator SSH public
  key, locked password authentication, and no package installation or commands.
  The corresponding private key stays with the operator, outside the repository.
  No controller, S3 or model credentials are in the seed.

`prepare-canary.sh` checks the input digest again, refuses existing disk files,
converts a standalone root disk, creates the new data disk, and builds the seed.
It does not run the VM or install Cube. `start-canary.sh` requires the prepared
private directory and an exact tooling image ID, starts a bounded CPU-only
container, and records its identity. It refuses an existing container-ID file;
failed starts require inspection rather than blindly restarting another VM.

Verify the VM's SSH host key against its trusted serial console before connecting.
Record its kernel, nested `/dev/kvm`, CPU flags, disk serials/sizes and cgroups.
Format only the verified new guest data disk as XFS with reflink, then mount it
inside the VM. Never format a B200 host block device. The explicit virtio disks
follow [QEMU's device/drive configuration](https://www.qemu.org/docs/master/system/bootindex.html).

## Before enabling Cube scheduling

The worker still needs private two-way control-plane and preview connectivity,
the current production isolation/recovery patches, templates, a bounded guest
quota, independent storage/placement/consumption observations, and lifecycle and
recovery acceptance. The outer VM merely provides the boundary in which to do
that work. Upstream does not claim nested virtualization support; our deployed
VPS uses reviewed nested-worker patches, which must also be verified here.

Do not register an enabled B200 node into the production scheduler before the
controller reserves and verifies actual placement. The old create path is not
node-aware. Cube supports a scheduling-disabled node state; compute registration
cannot set that reserved label itself, so a reviewed registration procedure is
needed to prevent a healthy new node taking ordinary production creates early.

Automatic restart is deliberately disabled for this canary. Shut down cleanly
through the guest or QMP `system_powerdown` and verify process exit before changes
to its disks. A Docker stop or forced QEMU exit is crash recovery, not a clean
checkpoint. No customer backup or production retention policy changes here.

## Initial boot verified — 2026-09-27

The candidate booted and completed cloud-init in about35seconds. SSH identity
was matched to the trusted serial console before login. Kernel
`6.8.0-139-generic`, eight CPUs, nested `/dev/kvm`, the expected64/256GiB disk
serials, and absence of NVIDIA devices were verified from inside the worker VM.
Boot ID `c5a2d884-8a99-4b5b-8a5d-9e79654df4b8` identifies this observation.
Host-key fingerprint `SHA256:4cxrBXHr9xjcRCzOdu+jSm0s2zpP2ytCrBfI/cXAa0A`.
The separate source rebuild tests pass on the B200 host in a bounded runc guest
container; that is not yet an end-to-end Cube microVM deployment test.

The new `/dev/vdb` was verified empty before `initialize-data.py` formatted it.
It is mounted at `/data` as XFS with reflink and project quotas, UUID
`8b9f945d-a7b6-4b53-bdd3-f001b17300f5`. The script is fenced to this initial boot
and refuses an existing filesystem. Its first attempt rejected an overlong label
before formatting; the reviewed shorter label `cube-b200` succeeded.

Docker29.1.3 and worker prerequisites are installed inside the VM. The default
Docker bridge overlapped the outer host's bridge and interrupted SSH replies.
`configure-docker-network.py` verified zero containers, then changed only the
new VM to `10.253.0.1/24` with allocation pool `10.253.128.0/17`. Normal SSH
recovered. The static Go `ssh-relay` and `stdio-ssh.sh` provide an operator recovery
path through hypervisor loopback, retaining end-to-end guest SSH authentication.
Build the relay on Linux with `CGO_ENABLED=0 go build -o ssh-relay ssh-relay.go`,
place it mode0700 in the private worker directory, and use `stdio-ssh.sh` as the
remote SSH ProxyCommand. The earlier shell-only relay did not work; use the Go
binary. No SSH private key was copied to the B200 host.

## Private management link verified

`configure-link.py` prepares an exclusive root0600 WireGuard configuration using
keys generated separately on each endpoint. The private keys stay on their own
hosts; only public keys are exchanged. There is no DNS or default-route change.

- VPS coordinator: `wg-cube-fleet`, `10.254.240.1/24`.
- B200 worker VM: `wg-cube-fleet`, `10.254.240.2/32`.
- Transport: existing private ZeroTier network, VPS endpoint `10.40.14.69:51827`.
- VPS firewall permits UDP51827 only on `zt33ooxlbk` from `10.40.14.68` to
  `10.40.14.69`. A direct public UDP test did not arrive; its temporary firewall
  allowance was removed after the private route passed.
- Both `wg-quick@wg-cube-fleet` units are enabled. Their configs and keys are
  root-private under `/etc/wireguard`. Worker keepalive is25s, interface MTU1380.
- A five-packet warm check in each direction had no loss: averages281ms and289ms.
  This is a short network check, not a soak test or app latency measurement.
- SSH to worker address `10.254.240.2` through the VPS was verified against the
  console-pinned host key. It confirmed XFS, Docker29.1.3 and no NVIDIA device.

Cube worker services/templates are **not installed or registered yet**. The
private link does not itself forward the VPS worker's loopback control services,
validate dynamic preview routing, or enable customer overflow. The production
controller remained container `154e72f54e3261c8287b50cbdde3a309df945033fbcbba96778ece9099f586e1`.

The link follows the [WireGuard quick start](https://www.wireguard.com/quickstart/)
with narrowly scoped peer routes and a private network endpoint.
