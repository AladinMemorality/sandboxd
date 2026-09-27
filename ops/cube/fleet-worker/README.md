# B200 worker VM canary

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
