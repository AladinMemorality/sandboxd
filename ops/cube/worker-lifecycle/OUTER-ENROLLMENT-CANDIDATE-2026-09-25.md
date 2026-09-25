# Outer lifecycle enrollment candidate — not installed

The private candidate bundle is staged at
`/opt/baarcha-bench/cube-host-lifecycle-enrollment-20260925`.
Its `review-01` directory contains observed host/worker/controller identities,
artifact hashes, four non-authorizing configuration candidates, a logical SQLite
observation, and the manual clean-stop evidence index. The detailed private
installation checklist is `review-01/INSTALLATION-PLAN-v2.md`.
The stop config contains the worker API credential; none of these private JSON
files belongs in Git or public output. Candidate files are root0600 beneath
root0700 directories. No service, customer data, routing, firewall, or source
installation gate changed during this preparation.

## Validated artifacts

The source archive exactly matches committed
`ef6c16a3c8674babb4e92f1fe8413bc63193b18f`. Its control-plane tree
`b4355d47fc98bb005ba86bcb95f73215d2f48140` also matches
`6c76cc985fba4359d727e2642d548a6f613a6b9c`.

The network-none Linux build used Go 1.22, 2 CPU and 2 GiB. Focused race tests
passed for `internal/workerstop` (16.085s) and `cmd/cube-worker-stop` (2.031s);
`cmd/cube-worker-start` has no additional test files. Package vet and both CLI
builds completed. Both binaries then passed native outer-host `--help` loader
checks, which do not execute coordinator operations. Log: `build-test.log`.

| Binary | SHA256 |
|---|---|
| cube-worker-stop | `ab55a5dda0bfceb6053a1870c7c73413ce2902e53e4c5ea2d2e0f903d20c469f` |
| cube-worker-start | `e3752688e978540c6dbcee2c7d8786fb24e3d409efaed8a2d880d1bc6c85cfb9` |

QEMU binary SHA256 is
`8a35ccba41582fc6c38b9df85fc9e35fa1d42f414d2d7d8090ee9b2f5e7c0854`.
The root/seed images reside on outer RAID-backed ext4; the data image resides
on separate NVMe ext4. The nested data filesystem is XFS. Exact UUIDs, canonical
paths, inode/device metadata, PID/starttime and launch argv are retained privately.
No active qcow was copied, checked for consistency, or hashed in this review.
The supervisor's fixed QEMU argv exactly matches the observed live argv. Its
staged helper SHA256 is
`e1e34848f59f507d0e2ae42b23e2887284248ea0454bd739d827ea716f17e205`;
the source gate is verified false. A later reviewed gate change necessarily
changes that hash and must be reflected in the installation evidence.

The candidate admission config uses the actual reviewed eight-template worker
manifest, SHA256
`bef0756e930600cf6e9f51b4066ef3f3e90de3de07d2a429f1da5c909377917a`,
with four active slots and uniform 2 CPU/2048 MiB templates. The older outer
`reviewed-template-inputs.json` has stale IDs and was excluded.

## Current enrollment blockers

- The consistent read-only SQLite observation has 66 apps/66 sandboxes,
  31 snapshots, zero running coding tasks and zero runtime bindings. It lacks
  `cube_admission`, `cube_admission_policy`, and `cube_recovery`. Apply the reviewed
  controller migrations through a backed-up, coordinated release with customer
  Cube routing disabled before the first coordinator inventory. Do not use stop
  preparation as an undocumented schema-bootstrap shortcut. The real drained
  Prepare operation initializes its admission policy through the guarded client;
  no manual policy insert is needed.
- The existing unit directly owns QEMU. The new supervisor cannot adopt it or
  retroactively hold its locks. Enrollment needs a real clean shutdown and a new
  process under the reviewed supervisor, with retained generation evidence.
- Source installation gate and all candidate host review flags remain false.
  Fresh drain, real coordinator pause proof and real supervisor clean receipt
  paths are deliberately absent. The example pre-drain JSON has an epoch date,
  false flags and unfilled hashes; it is not installed as a receipt.
- Initial empty-node review and current provider/task/job inventory must occur
  after recovery operators finish. This collection did not assert emptiness
  during another agent's recovery work. Boot/PID/controller identities must be
  refreshed after any restart or recreation.
- Independent backup/off-host/application-restore evidence remains separate.
  A live SQLite observation is not a frozen fleet backup; manual clean-reboot
  proof is not evidence that the full production coordinator ran.

## Actual manual proof and final drain scope

`/opt/baarcha-bench/cube-crash-clean-de3-01/report.json` has SHA256
`b7f8b1497fe2451c3c8dab880c02a68b266a93600c86649d16b2a3746784faaa`.
It records successful ordinary and clean-worker-reboot continuation of latest
app/home/PostgreSQL data. It explicitly records
`production_stop_coordinator_tested=false` and `production_accepted=false`.
The candidate evidence index preserves this distinction and the nested retained
stop, persistent readiness, exact QEMU exit and clean-reboot receipt hashes.

Final maintenance must fence new HTTP/tool/preview work and drain open streams,
then stop the exact controller with restart disabled and no active task conflict.
The private writer inventory includes the real loaded units/script hashes for
`baarcha-project-env-apply`, Classroom and Fennec egress timers, live transcription,
chat memory, admin retention and deployment entrypoints. In particular, the
environment-apply job can recreate a sandbox independently of the coding-task
count. Gateway timers reconcile network identities and must not race cutover.
Classroom/Fennec sessions, background workers, database clients, direct operators
and deployment automation require their own drain accounting. Do not mistake
zero coding tasks for complete application quiescence or blanket-stop unrelated
platform services.

Only the actual fixed coordinator cycle under a fresh verified fence may create
the stop marker and genuine pause/clean receipts. Startup then checks the exact
new generation and existing paused admissions before clearing that marker. No
placeholder, elapsed timeout, missing provider record or old manual receipt
authorizes a shortcut. See [the installation checklist](INSTALLATION-CHECKLIST.md)
for the required first-cycle sequence and [final acceptance](FINAL-ACCEPTANCE.md)
for independent backup and restore gates.
