# Candidate installation and initial enrollment checklist — not executed

The source constant `STOP_COORDINATOR_IMPLEMENTED` remains false. No manifest
field overrides it. These instructions are preparation; they neither install a
unit nor authorize a service change. Root performs actual worker acceptance and
approves a reviewed source transition separately. Do not use `sed`, an environment
variable, a wrapper, or an installer-time substitution to evade the gate.

## 1. Freeze the deployment inputs

- Record exact repository commit and Linux native binary SHA256 for
  `cube-worker-stop`, `cube-worker-start`, and the matching control plane. Retain
  the previous controller image and consistent database/key backup.
- Retain the currently installed worker launch unit/scripts/configs and known
  QEMU disk identities. Installing a supervisor cannot adopt an existing QEMU;
  do not launch a second worker. Actual transition needs an independently
  stopped/closed old process and exclusive startup/backup locks.
- Finish the metadata upgrade and independently verify all retained fixture disks
  are accounted for. The worker API, Cubelet/containerd inventory, filesystem and
  DB must agree. Missing provider registration is not proof of an empty worker.
- Complete the owned manual Pause → persistent sync → graceful QMP powerdown →
  boot → authenticated metadata/app/SQL continuation acceptance. This does not
  substitute for the final actual Go coordinator under production maintenance.

## 2. Prepare private manifests, without installing/enabling

Use the schemas in `config/`. Populate them from actual reviewed evidence, never
from guessed paths/UUIDs or metadata returned by untrusted tenant processes.
Save under private staging and validate schema plus file/hash/device facts.

| Machine | Installed path after authorization | Source / contract | Mode |
|---|---|---|---|
| Outer + nested | `/usr/local/libexec/baarcha-cube-worker-lifecycle.py` | reviewed `lifecycle.py` | root0755 |
| Outer | `/usr/local/libexec/backup_monitor.py` | reviewed `backup_monitor.py` | root0644 |
| Outer | `/usr/local/libexec/baarcha-cube-worker-stop` | exact native Linux build | root0755 |
| Outer | `/usr/local/libexec/baarcha-cube-worker-start` | exact native Linux build | root0755 |
| Outer | `/etc/baarcha-cube/lifecycle.json` | host lifecycle schema | root0600 |
| Nested | `/etc/baarcha-cube/lifecycle.json` | nested lifecycle schema | root0600 |
| Outer | `/etc/baarcha-cube/worker-stop.json` | reviewed secret stop config | root0600 |
| Outer | `/etc/baarcha-cube/worker-start.json` | actual prior proof/receipt paths | root0600 |
| Outer | `/etc/baarcha-cube/backup-monitor.json` | real backup/copy/restore evidence | root0600 |

Create no placeholder startup proof or backup receipt. Configure monitoring only
with genuine independently checked receipts; missing evidence should be reported
unhealthy. Preserve a private copy of all approved manifests and hashes.

The outer worker unit is the reviewed `baarcha-cube-worker-01.service`; monitor
unit/timer are separate. Nested reviewed target/preflight are separate from the
outer systemd manager. All 14 nested units listed in `lifecycle.py:SERVICES` need
the reviewed preflight dependency drop-in; merely disabling the upstream target
is insufficient because another unit can directly start a dependency. Audit the
upstream enabled control target and templatecenter explicitly. Validate candidate
unit files with `systemd-analyze verify` before any `daemon-reload` or enablement.

## 3. Resolve initial enrollment without fabricated shutdown evidence

The first installation has no genuine supervisor-generated clean-stop receipt.
Do **not** invent `stopped-clean`, write a fake `.worker-stop.json`, clear an old
marker, or route traffic because the API says zero guests.

Prepare a private `initial-empty-worker-review` record following its schema. It
records actual empty canonical Cube bindings/provider guests/tasks/pending
admissions, plus separately retained fixture disk inventory, installed patch
manifest, persistent metadata proof and manual acceptance hashes. It is an
operator evidence record, not executable permission or tenant readiness.

After root's explicit installation/source-gate review, the existing first-boot
branch may be used only when the lifecycle status file has **never existed** and
empty-node review is complete. Set `first_boot_empty_reviewed` in the reviewed
host manifest; no existing lifecycle status may be deleted to reach this branch.
The supervisor then creates its actual `running-unreconciled` status and holds
the real lifetime locks. It does not claim a prior clean stop or tenant readiness.
This step is worker management enrollment, with Cube customer routing disabled.

The existing startup reconciliation command intentionally cannot accept this
initial run as if it had a previous pause receipt. The first real empty
coordinator stop → clean poweroff → boot cycle must occur under the final
controller/traffic maintenance fence. It creates the genuine stop marker,
immutable paused proof and supervisor clean receipt, then the normal offline
startup command validates and clears that exact marker. No special bypass or
synthetic prior generation is needed. Until that cycle passes, do not call this
worker ready for customer cutover.

## 4. Final actual coordinator acceptance under production maintenance

- Freeze admission/traffic and drain existing streams/tasks/direct/provider
  writers. Stop exact controller container and disable its restart policy.
- Populate current QEMU PID/starttime, worker machine/boot/data IDs, exact
  controller ID, four-slot template contract and private evidence directory.
- Run the fixed offline `--inventory`, independently review its exact output,
  then create the real fresh pre-drain receipt. The controller-name and immutable
  ID checks are intentionally not configurable for a synthetic substitute.
- Invoke ordinary stop only through the reviewed supervisor/coordinator. It
  checks all bindings, takes the exclusive DB lock, writes the marker, pauses
  and verifies guests, syncs persistent worker state, then requests graceful
  powerdown. Failed stop retains the live worker and fences; never force it to
  complete merely to proceed with installation.
- Verify actual QEMU exit, retained immutable clean receipt and lock release.
  Capture/seal the consistent disk pair and controller recovery set separately.
- Boot through reviewed preflight/units. Refresh **current** boot/PID fields in
  private stop config and point start config to the actual old proof/clean receipt.
  Run the fixed offline `cube-worker-start`, retaining exact output privately.
- Check explicit readiness evidence and absent matching marker, expected paused
  inventory, no lost bindings, zero pending admissions and tested four-slot
  policy. No app was woken by reconciliation. Verify selected actual continuation
  separately before reopening traffic; preserve untested gates honestly.

## 5. Monitoring and recovery acceptance

Enable only after checking the installed observer runs with its intended private
read permissions and local journald output. Verify intentional stale backup,
missing off-host receipt and binding disagreement produce unhealthy; never
fabricate successful receipts to silence the monitor. Record capture-time age,
not encryption time. No action automatically expires a reservation or repairs a
missing runtime. Retain exact rollback configs/images and newly persistent
metadata; reverting to volatile worker metadata is not an acceptable rollback.

No item above authorizes editing customer files, relaxing network isolation,
changing app resource limits, copying global credentials to guests, or enabling
Cube routing before the separate full-fleet acceptance.
