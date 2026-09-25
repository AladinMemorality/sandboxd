# Storage guard enrollment preparation

**The observer alone was installed and validated on 2026-09-25.** See
`actual-installation-2026-09-25.json` for exact hashes, actual first measurement
and timer advancement. `installed-20260925.py` records that one-time operation;
it refuses an existing installation and is not an upgrade command. No guest,
controller, worker or coordinator lifecycle operation was part of installation.
The controller remains on Docker with Cube disabled. The refreshed stop
configuration is prepared privately but has not been installed; live guarded
workload and coordinator acceptance remain separate gates.

The post-cycle worker boot ID is deliberately invalid in the reusable
`pins.NON-AUTHORIZING.json`. Do not replace it with
the previous boot, copy it into production, or treat the rendering tool as proof
that an actual restart/readiness check passed. The outer boot, machine and UUID
pins were read-only observations on2026-09-25. Reverify them before enrollment.

The directory mount override adds only a read-only directory and admission JSON;
it does not enable Cube or change preview routing. Root must sequence this after
the ongoing empty-worker host cycle and reviewed controller release/schema34.
All production traffic/drain/lifecycle operations remain with the root operator.

## Prepare exact private configurations after actual readiness

Preserve the actual post-cycle readiness, QEMU generation, controller identity,
filesystem identity and zero/preserved-guest proof. Produce a root-owned0600
attestation referring to the full retained evidence SHA256:

```json
{
  "version": 1,
  "verified": true,
  "outer_boot_id": "ACTUAL_VERIFIED_OUTER_BOOT_UUID",
  "worker_machine_id": "ACTUAL_VERIFIED_32_HEX_MACHINE_ID",
  "worker_boot_id": "ACTUAL_VERIFIED_NEW_WORKER_BOOT_UUID",
  "data_uuid": "ACTUAL_VERIFIED_XFS_UUID",
  "qemu_pid": 0,
  "qemu_start_time": "ACTUAL_VERIFIED_PROC_START_TICKS",
  "controller_id": "ACTUAL_VERIFIED_64_HEX_CONTAINER_ID",
  "evidence_sha256": "ACTUAL_RETAINED_READINESS_EVIDENCE_SHA256"
}
```

This example fails validation. The helper accepts a completed private operator
attestation; it does not generate readiness evidence or authorize itself.
The existing private stop configuration contains the API credential and must
never be committed or printed. Extract the existing admission JSON privately,
retaining its exact eight-template set. Copy the prepared pins and sanitized
live template-capacity proof into a root-owned staging directory before use.

Render a **new** root0700 output directory (no existing file is modified):

```sh
python3 /opt/baarcha-cube/storage-guard/prepare_enrollment.py \
  --pins /ROOT-PRIVATE/pins.NON-AUTHORIZING.json \
  --boot-receipt /ROOT-PRIVATE/verified-post-cycle.json \
  --admission /ROOT-PRIVATE/current-admission.json \
  --worker-stop /ROOT-PRIVATE/current-worker-stop.json \
  --template-proof /ROOT-PRIVATE/template-capacity-verified.json \
  --out /ROOT-PRIVATE/storage-enrollment-reviewed-NEW
```

The renderer requires matching machine/data/outer boot identities and all eight
independently checked2CPU/2048MiB/10GiB templates. It preserves the API key and
existing operational paths, refreshes QEMU/controller/worker boot identities
from the explicit receipt, sets both stop and admission worker boot pins to the
same value, adds the writable-disk contract, and points the coordinator to the
new immutable migrations directory:
`/opt/baarcha-cube/coordinator-storage-guard-20260925/migrations`.
A changed observer ID, path or filesystem pin is refused when a guard already
exists. Neither an existing output directory nor an unverified placeholder can
be silently overwritten. Output files are0600 and the directory0700.

If the controller is recreated to add the mount, its immutable container ID
changes. Refresh the exact stop configuration from a new independently checked
controller identity **before any coordinator invocation**. The renderer does
not inspect Docker or act as an automatic identity updater.

## Reviewed enrollment sequence — observer step completed separately

1. Keep the existing traffic/admission fence and Cube rollout flags false. Take
   the required controller backup and deploy the reviewed schema34 runtime.
   Verify zero charged rows for first guard enrollment; never clear a pending
   row, delete the policy or reset observer sequence as an enrollment shortcut.
2. Verify the native coordinator hashes against the prepared build receipt.
   Install those exact stop/start binaries at the existing root-owned fixed
   paths; preserve old binaries/config. Copy all34 reviewed migrations to the
   immutable directory above. Update the lifecycle helper's pinned artifact and
   migration manifests before invoking the new coordinator. Do not combine an
   old33-only coordinator configuration with an enrolled34 database.
3. Independently preserve any existing observer state. For a genuinely new
   observer create `/var/lib/sandboxd/cube-storage-observer` root0700, plus
   `/run/sandboxd-cube-storage` root0700. Never remove `sequence.json` on restart.
   Install reviewed `observe.py` under `/opt/baarcha-cube/storage-guard/` and the
   rendered observer configuration as `/etc/baarcha-cube/storage-guard.json`
   root0600. Install the prepared service/timer at their exact systemd names.
4. Run one explicit observer invocation using that configuration. It may only
   read the pinned worker identity/free-space information over localhost20222,
   read outer filesystem counters, and write its own sequence/observation files.
   Verify exact boot/FS/machine pins, monotonic freshness, free counters and root
   file ownership. Only after that proof enable the five-second timer. No guest,
   route, filesystem-growth or lifecycle operation is part of observation.
5. Review the rendered Compose override against the current production compose
   set. Mount the **directory** read-only at the same host/container path with
   `create_host_path:false`; a file mount would freeze the old inode. Preserve
   all existing UDS relay mounts and platform configuration. Recreate only the
   reviewed controller during the authorized maintenance window; neither this
   artifact nor the helper runs Compose. Verify read-only mount, host root UID,
   same kernel boot/boottime clock and exact admission JSON inside the controller.
6. Install the rendered stop config privately and update its current controller
   generation after any recreation. Keep the existing start-receipt configuration
   and actual clean-stop evidence; do not invent replacements. Confirm the new
   migrations/binary manifests and zero unexpected tasks/provider jobs.
7. Run the unchanged four-app workload bounds using the already prepared
   `c4859ef4...` binary and its `storage_guard` field populated with the same
   installed guard object. Use a fresh owned stage/DB, current worker pin and
   normal exclusive handoff; no such run was executed by this preparation.
   Preserve the historical `ccfe6d...` results separately. Then perform the
   actual canonical bound pause/start/restore coordinator acceptance with34.
8. Only root may decide subsequent rollout/routing. Missing/stale observation
   blocks new create/resume/paused-delete but does not block safe pause or
   read-only startup reconciliation. Existing running deletion retains its
   already charged disk reservation until authoritative completion.

The enrolled-guard coordinator tests use actual migrated SQLite, root-owned
observation files, real monotonic clocks and the concrete HTTP Cube client
against an owned fake provider. They demonstrate stale/missing-observer pause,
zero wakes during startup reconciliation, and continued refusal of a fresh wake.
They are regression tests, not a live production coordinator acceptance.
