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
