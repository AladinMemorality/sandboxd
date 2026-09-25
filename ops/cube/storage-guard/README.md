# Cube storage admission guard

This is a prepared implementation, not an installed observer or completed live
acceptance. Production Cube remains subject to the operator's rollout gates.
The daemon and `cube-migrate` mutation commands require the guard; isolated
library fixtures may omit it only on a database never enrolled in this policy.

The policy is fixed: at most four charged guests, reviewed 2CPU/2048MiB templates
with **hard 10240MiB writable disks and 2048MiB pause RAM**, at least96GiB observed
free on both the worker data filesystem and its outer backing filesystem, and a
48GiB minimum remaining reserve after conservative accounting. Template IDs must
come from the independently inspected immutable template manifest; setting the
JSON field is an operator assertion, not discovery of a remote disk limit.

Every new create/resume/paused-delete reservation consumes12GiB in the same
SQLite transaction as CPU admission. Recovery replacement consumes another12GiB
even if it retains the failed source's CPU slot. No pause/delete refunds the
current observation epoch. A newer observation seeds its spent bytes from every
grant not authoritatively released before **measurement started**, including
operations released while the observer was collecting its measurements. Thus
rapid create/pause cycles cannot repeatedly spend one stale sample. Unknown or
pending outcomes keep their grants. Existing charged running-connect and delete
retain their existing grant and work without a fresh observer; paused-delete
requires one because upstream can resume before deleting. Pause always remains
available, and storage rejection never triggers idle-RAM eviction/retry.

`CLOCK_BOOTTIME` orders observations, grant/release commits and freshness. Both
processes use the same outer kernel clock; separate Docker time namespaces are
unsupported. Wall-clock/NTP changes do not alter ordering. The file has a pinned
outer boot ID and worker boot ID, machine ID, filesystem UUIDs and observer ID.
An unreviewed reboot or observation mismatch rejects admission. After explicit
operator boot-pin updates, a new strictly increasing generation may start a new
boot epoch: unresolved grants carry; completed prior-boot releases are already
represented in the new free-space measurement. Worker reboot alone does not
reset the outer clock or discard any grants. Missing/corrupt sequence state must
be recovered from its preserved record; do not reset it to bypass a refusal.

## Operator configuration

Add to the existing `SANDBOXD_CUBE_ADMISSION` JSON (retain reviewed templates):

```json
{
  "max_active": 4,
  "cpu_count": 2,
  "memory_mb": 2048,
  "writable_disk_mb": 10240,
  "templates": {"REVIEWED_TEMPLATE_ID": {"cpu_count": 2, "memory_mb": 2048}},
  "storage_guard": {
    "observation_path": "/run/sandboxd-cube-storage/observation.json",
    "observer_id": "OPERATOR_GENERATED_32_LOWERCASE_HEX",
    "outer_boot_id": "REVIEWED_OUTER_BOOT_UUID",
    "worker_machine_id": "REVIEWED_WORKER_32_HEX_MACHINE_ID",
    "expected_boot_id": "REVIEWED_WORKER_BOOT_UUID",
    "inner_fs_uuid": "793c3349-db9c-4815-9842-989ed484f1f8",
    "outer_fs_uuid": "REVIEWED_NVME_FILESYSTEM_UUID"
  }
}
```

Save that exact `storage_guard` object as a root-owned0600 observer config. The
observer is fixed to `/mnt/nvme/baarcha-cube/worker-01/data.qcow2` and the existing
pinned localhost20222 worker SSH identity. It requires `/data` to be XFS directly
on `/dev/vdb`, checks both filesystem UUIDs and both boot identities, and collects
only free-space/identity counters. No guest files are parsed or mounted.

Enrollment order, requiring the normal operator lifecycle/maintenance fence:

1. Apply migration0034 and install the reviewed controller/CLI build. Initial
   guard enrollment requires zero charged rows; later restarts preserve all
   rows, grants and epoch budget. Never delete an old admission policy to enroll.
2. Verify template disk/RAM caps, four-slot native quota, current filesystem
   capacity and independent backups. Provision the observer config and its
   persistent state directory as root0700. Preserve sequence.json in backups
   alongside the admission database; the SQLite policy is the authoritative
   anti-replay check even if an observer sequence is lost.
3. Install the companion service/timer under reviewed host deployment. Run one
   observation, inspect exact pins and free counters, then enable the timer.
   The script writes only its own lock/sequence/observation files. Failures leave
   the last observation untouched and new admissions stop after30seconds.
4. Bind-mount **the directory** `/run/sandboxd-cube-storage` into the controller
   at the same path, read-only. A file bind mount would keep an obsolete inode
   after atomic replacement. It contains no secrets. The directory is root0700
   and the observation0600; the reviewed controller runs as host root. A change
   of controller UID requires a separately reviewed read-only permission policy.
5. Configure the identical guard for controller and offline mutation CLI, with
   both boot pins matching independently verified lifecycle readiness. Changing
   only the observation file cannot change the configured pin. Refresh the
   lifecycle coordinator's reviewed binary/migration manifest too.
6. Run the opt-in four-app workload with its optional `storage_guard` object
   populated, retaining the unchanged workload/deadline/cleanup checks. Previous
   `ccfe6d...` live results predate this guard and are not acceptance of it.

The observer invokes SSH on its timer; **admission never invokes SSH**. Its only
request-time work is bounded local file verification and one SQLite transaction.
The test benchmark reports complete create/finish/pause/finish bookkeeping,
with and without the secure file reader; it is not a VM latency benchmark.

## Scope and limits

The guest disk is a hard bound, not a promise that the protocol's maximum
compressed+expanded imports fit simultaneously. Imports stage inside the same
10GiB guest filesystem and may fail with ENOSPC. Published source archives,
controller SQLite/backups and owner retained history live outside that guest
filesystem and need their existing separate capacity/retention budget. New
untrusted writable-disk resize, arbitrary templates, direct provider API or
provider auto-resume must remain unavailable. Maintenance template builds,
backup/capture copies and unrelated writers on the outer NVMe require separate
operator budgeting/fencing; this guard cannot reserve space against an unrelated
privileged process filling the filesystem. It does not override the upstream
65% scheduler filter, certify full thin-provisioned660GiB entitlement, or turn
448GiB virtual disk capacity into guaranteed physical backing.

Tests: `go test -race ./internal/cube ./internal/store ./internal/api
./cmd/sandboxd ./cmd/cube-migrate` in the bounded Linux root fixture, and
`python3 -m unittest discover -s ops/cube/storage-guard -p 'test_*.py'`.

Validation evidence and exact native source/binary identities are in
[`results/validation-2026-09-25.json`](results/validation-2026-09-25.json).
The seven-package Linux root race suite and nine observer tests passed. Three
100-cycle samples took0.731–0.905ms per guarded bookkeeping cycle versus
0.622–0.778ms without the guard, using a tmpfs database. Sampling noise and the
different production filesystem preclude a precise latency claim. The actual
eight reviewed templates independently passed the live10GiB root-volume check
recorded in `template-capacity-verified-2026-09-25.json`.
