# Worker lifecycle — reviewed for empty enrollment

The source gate now permits reviewed initial empty-worker enrollment. The fixed
Go coordinator, installed nested retained-stop helper, and real manual reboot
proof support that limited transition. The outer supervisor is not installed
by this change. Its private host review/drain requirements and first real
coordinator cycle remain mandatory. This does not enable customer Cube routing
or claim completed crash recovery and backup acceptance. See the
[source-gate review](SOURCE-GATE-REVIEW-2026-09-25.md).

## Observed existing behavior, 2026-09-25

Read-only `systemctl show/cat` inspection found the outer
`baarcha-cube-worker-01.service` disabled but running, `Restart=no`, no `ExecStop`,
`KillMode=control-group`, `SendSIGKILL=yes`, and a 180-second stop timeout. Ordinary
`systemctl stop` therefore terminates QEMU; it is not an application drain.

Inside the worker, the control target and templatecenter service were enabled;
the other listed services were disabled. The control target explicitly Wants
those disabled units, so disabling individual services does not prevent startup.
The reviewed replacement target must replace the upstream enabled target, and
all 14 named management units need the exact preflight dependency drop-in.
Do not blindly disable/mask services during current recovery capture.

Cubelet's existing stop helper escalates its own PID after 20 seconds; it does not
prove guest VMMs stopped. API/Master/templatecenter use ordinary TERM. Proxy and
lifecycle manager stop their own Docker components. None substitutes for an
app-aware worker drain. Current abrupt-loss evidence reports missing Master/API
identity; autostart must not silently classify that as an empty healthy fleet.

## Outer lifetime and normal stop behavior

`lifecycle.py supervise` uses fixed QEMU argv: existing three disk paths,
40 GiB RAM, 12 vCPU, the same three loopback forwards, no raw networking changes.
It holds an exclusive instance lock and the same shared backup lock/marker as
`cold_pair.py`. Lockfiles are never replaced. Both descriptors pass to the child;
synthetic inheritance tests and the actual isolated QEMU acceptance both confirm
that QEMU retains them. Native disk FD checks remain required by cold
capture. A supervisor failure must never permit a second worker or hot backup.

The candidate unit sends TERM only to the supervisor (`KillMode=process`), has
`SendSIGKILL=no`, `TimeoutStopSec=infinity`, and `Restart=no`. Signal handlers only
request the stop state machine. They never signal QEMU. A failed drain, invalid
receipt, QMP failure, or 180-second graceful-powerdown timeout leaves the
supervisor and QEMU alive with their locks held. There is no implicit retry,
forced termination, automatic restart or arbitrary executable hook. Repeated
TERM does not rerun an ambiguous drain. A healthy drain permits exactly QMP
`system_powerdown`, and success requires actual QEMU exit. Ordinary unrequested
QEMU exit, even status0, is classified as worker loss.

This deliberately trades an indefinitely blocked normal shutdown for avoiding
an unplanned VM kill. The supervisor must stop before Docker/Caddy teardown
(ordering is `After=docker.service caddy.service` on startup). A forced host
shutdown, power loss, kernel OOM, administrator kill, or hardware fault can still
destroy the process. These settings are not crash durability. systemd recommends
against `KillMode=process` generally because children can outlive service state;
this narrow supervisor keeps its own process alive on failed stop, holds the
locks, and refuses restarts. Validate this exact behavior on an isolated unit
before installation. Sources: [systemd kill semantics](https://raw.githubusercontent.com/systemd/systemd/v255/man/systemd.kill.xml)
and [service stop semantics](https://raw.githubusercontent.com/systemd/systemd/v255/man/systemd.service.xml).

## Concrete offline stop coordinator — candidate only

`cmd/cube-worker-stop` reads the fixed root0600
`/etc/baarcha-cube/worker-stop.json`. It accepts no executable hooks, arbitrary
worker addresses, shell fragments or guest-provided config. Provider access uses
the fixed outer loopback origin and a root-private deployment
configuration; API key values never appear in stdout, errors or artifacts.
The config file itself is root0600 plaintext operational secret storage, not a
new encryption format. Keep it outside the repository and normal logs.

The sequence is:

1. A reviewed operator applies the traffic/admission fence, drains existing
   requests/streams/tasks and fences direct/provider writers. The operator stops
   the exact controller and disables its Docker restart policy. The helper does
   none of those actions itself. Caddy/stream/provider-job drain is an explicit
   **external trust input**, not something inferred from the inactive container.
2. `cube-worker-stop --config /etc/baarcha-cube/worker-stop.json --inventory`
   takes the controller maintenance lock and performs a mode=ro SQLite inventory
   after independently checking exact inactive container identity. It emits the
   canonical expected binding/config hash and makes no provider changes. Save
   that output privately. Missing schemas, active tasks, incomplete migrations or
   recovery, pending allocations and unbound reservations are refusals.
3. Produce an actual fresh pre-drain receipt, with the exact controller ID,
   worker boot ID, QEMU PID/starttime, inventory hash, loaded Caddy config/evidence
   hashes and verified traffic/stream/direct-writer/provider-job drain flags.
   Receipt age must be at most two minutes at start; copying an example or
   setting booleans without doing the checks is not authorization.
4. The supervisor invokes only `/usr/local/libexec/baarcha-cube-worker-stop` with
   that fixed config. The CLI verifies the current QEMU process generation and
   fixed disk argv, exact stopped controller/restart policy, fresh receipt and
   native `/proc` DB users. It takes the exclusive controller lock and atomically
   publishes `<database>.worker-stop.json`, fsyncing file and parent **before the
   first provider mutation**. A missing/partial/old marker never expires; daemon
   startup refuses it. Existing unfinished markers are never overwritten.
5. All-state provider inventory must exactly equal owned bindings, including
   stable sandbox/app IDs, template, domain and valid running/paused states.
   The bounded API refuses a potentially truncated list. Ordinary guarded
   `Pause` is sent sequentially only to running guests, followed by authoritative
   GET paused. Already paused guests are not woken. No workspace quiescence,
   agent-process shutdown, checkpoint injection, resume or delete occurs. Whole
   VM pause preserves process/RAM state including PostgreSQL buffers. It is not
   an independent proof of abrupt-crash database durability.
6. Controller identity, DB fingerprint and provider inventory are rechecked
   throughout. Pending/charged allocations must all be released by authoritative
   pause acknowledgment. A fixed pinned-key SSH command asks the reviewed worker
   helper to verify hostname/machine/boot/data identities, binary/config hashes,
   persistent metadata paths on the `/data` device and **zero Cube-owned
   containerd tasks**. It invokes only `sync -f /data` and `sync -f /`, then a final
   exact API/DB check precedes durable private proof publication.
7. Only verified proof for the supervisor's current QEMU PID/starttime permits
   QMP `system_powerdown`. The stop CLI remains alive holding controller exclusion
   until that exact QEMU generation disappears. It ignores ordinary TERM/INT/HUP
   and SIGPIPE; stdout failure or stdin EOF never grants lock release. A failed
   Pause/sync/receipt keeps exclusion and the durable startup marker. An
   uncatchable process crash loses flock but leaves the startup marker.

Preparation is bounded (eight-minute flow, bounded provider requests and a
ten-minute supervisor proof-read deadline); timeout does **not** kill QEMU or
expire locks/reservations. The CLI performs no automatic retries of ambiguous
provider mutations. The supervisor preserves blocked CLI children rather than
terminating them. A failed partial attempt requires operator recovery; there is
no automatic marker removal or retry-overwrite.

The cold backup tool recognizes this one fixed supervisor entrypoint and takes
the identical exclusive backup lock plus controller lock after QEMU exits. It
still requires zero tasks/pending allocation, exact paused set and closed source
FDs. Its paired disk capture remains a separate operation. The stop marker must
remain until explicit boot reconciliation validates the same identities and
paused snapshots, current config/history and authenticated app/SQL continuation.
The offline startup command below is the only candidate marker-removal path.

See `stop-config.example.json` and `pre-drain-receipt.example.json` for **invalid,
non-authorizing examples**. After review, build on Linux with the existing Go
version: `go build ./cmd/cube-worker-stop` from `control-plane`. Do not install the
binary or flip the source gate before actual isolated-unit acceptance.

## Startup and observation

Outer preflight checks root-only reviewed config, QEMU hash, exact backing
filesystem UUID, standalone disk paths/formats and 48 GiB reserve. A prior
lifecycle state must be `stopped-clean`; first boot requires an explicit empty
worker review. Unclean previous exit blocks ordinary startup and needs the
separate native recovery procedure. After spawning, state is
`running-unreconciled`; no code sets tenant-ready or starts controller routing.

Nested preflight checks hostname, machine ID, `/data` UUID, exact hashes of all
three patched binaries, and recorded startup scripts/config/image-contract
artifacts. Manifest entries are files only, never executable commands. The
reviewed target does not itself mean readiness. `nested-ready` additionally
requires every listed management unit active, but still returns
`tenant_ready=false`: authenticated API/worker health, expected template/resource
contracts, canonical provider inventory and controller admission reconciliation
are checked by the explicit offline startup command below and still need actual
worker acceptance before traffic can reopen. An enabled
upstream target can bypass the reviewed target unless every service gets its
preflight dependency; installation must audit that explicitly.

`monitor` reads the private local lifecycle status, data free bytes and QEMU
cgroup memory events and writes sanitized local journald JSON only. It also invokes the fixed read-only binding/provider/admission helper and
validates root-private backup/copy/restore evidence as detailed below. Missing or
inconsistent evidence reports unhealthy. No external notifications or runtime
mutations occur. Never automatically delete, restart or expire
reservations in response to an alert.

## Validation and install gates

Run `python3 -m unittest discover -s ops/cube/worker-lifecycle -p 'test_*.py'`.
Tests use dummy child processes, a local UNIX QMP fixture and private temporary
locks; no installed systemd service or VM is touched. Existing backup tests also
exercise exact supervisor recognition and lock exclusion.

Remaining gates: independently perform/attest real traffic and stream drain;
accept the offline boot reconciliation and backup monitoring described below;
review exact private manifests; validate units with `systemd-analyze verify`;
prove pause→clean worker poweroff→paired encrypted backup→startup→authenticated
app/SQL/history continuation and separate isolated restore. Actual abrupt
power-loss recovery is separate acceptance and is not established by these files.


The actual diskless/networkless systemd acceptance on 2026-09-25 passed both
failed-drain and unhandled-powerdown cases in 6.13 seconds. Each transient unit
was capped at one CPU/256 MiB; its diskless QEMU used 32 MiB and one CPU. Ordinary
`systemctl stop` kept the same supervisor and QEMU alive, with both inherited
lock descriptors excluding cold capture and a second instance. Zero OOM events
occurred. Only the exact fixture QMP socket received cleanup `quit`; independent
process/unit checks found no leftovers. See
[`systemd-acceptance-2026-09-25.json`](systemd-acceptance-2026-09-25.json).
This exercises the production Supervisor/lock implementation, with a synthetic
drain callback; it does not exercise actual guest OS clean shutdown, real traffic
drain, Cube pause/sync over a live worker, or the complete backup/restart cycle.
The Go coordinator's Linux race suites separately passed with actual SQLite and
HTTP provider fixtures, including config/task/identity drift, missing-provider
404 preservation, ambiguous Pause and lock/marker retention.


## Explicit offline startup reconciliation (candidate)

Install gate remains false. Build `cmd/cube-worker-start` separately; it takes no
routing, resume or repair switches. Its fixed private files are
`/etc/baarcha-cube/worker-stop.json` (update the **current** worker boot ID and QEMU
PID/starttime, preserving reviewed machine/data/controller identity) and
`/etc/baarcha-cube/worker-start.json` (exact retained pause-proof and clean-receipt
paths). The latter's example is deliberately invalid. Do not substitute a
fabricated stopped-clean receipt for a missing one.

The supervisor now writes an immutable `clean-stop-<proof-hash>.json` beneath its
private worker root only after actual requested powerdown and QEMU exit status0.
If durable receipt publication fails, status becomes worker-lost. Next boot's
mutable status cannot overwrite this evidence. The exact old pause proof and
startup marker contain worker machine/data/boot/process identities and complete
canonical binding/config/owner fingerprints.

Keep controller stopped with restart disabled and traffic/direct writers fenced.
Run the fixed startup binary as native root. It takes the exclusive controller
lock, checks native DB users, verifies the same machine/data and a **different**
boot/process generation, and compares retained clean receipt, original pause
proof, marker, and current canonical SQLite state. A pinned-key worker read
verifies exact installed binary/config hashes, persistent metadata filesystem,
all management services active, >=48 GiB reserve and zero Cube containerd tasks.
Two complete inventories and individual authenticated GETs must prove every
canonical guest still paused with unchanged identity/template/resource contract.
Unknown/missing/running/stopped guests, changed config/tasks, incomplete recovery,
quarantine, or missing/pending/deleted admission rows block startup.

Admission is validated, never reconstructed: exactly four permitted active slots
with the reviewed 2 CPU/2048 MiB profile, and existing released/uncharged rows for
every retained paused binding. Paused/stored project count may exceed four.
No Create/Connect/Pause/Delete or guest command occurs. On success, fsynced
immutable startup evidence and `startup-current.json` precede exact-value/inode
comparison and removal of the matching marker, followed by parent fsync. Any
failure before removal leaves startup blocked. This command returns tenant-ready
**control-plane reconciliation evidence**; it does not start the controller,
remove the traffic fence, wake an app, establish application/SQL continuation,
or authorize an installation without the separate actual acceptance.

## Read-only consistency and backup monitoring

The timer runs only the fixed local helper and emits local journald JSON.
`cube-worker-start --observe` checks current QEMU/startup-evidence generation,
marker absence, pinned worker readiness/reserve, all-state provider inventory,
and matching admission/resource counts. SQLite is opened `mode=ro` with
`query_only`; no `store.Open` schema/bootstrap path or guarded-GET admission
mutation is used. A before/after snapshot mismatch during ordinary concurrent
work yields unhealthy and retries on the next timer, never repairs or wakes.

Install `backup_monitor.py` beside the fixed lifecycle script. Root0600
`/etc/baarcha-cube/backup-monitor.json` selects exact evidence paths and reviewed
freshness policy. `cold_pair.py seal` now emits a root0600 `.manifest.json` sidecar
binding the ciphertext SHA256, capture-manifest hash, recipient fingerprint,
immutable original capture time, separate seal time, and exact file device/inode/size/mtime/ctime. A crash without this
sidecar is unhealthy. Monitoring checks identity and bounded ages without
rehashing a potentially hundred-gigabyte archive every minute. Backup age uses
the original `captured_at` inside the hashed capture manifest, copied verbatim
into the seal sidecar; re-encryption cannot refresh it. Legacy captures without
that timestamp may still be restored, but cannot be sealed/monitored as fresh; it cannot detect
malicious privileged manipulation and is not a fresh cryptographic scan.

Independent copy receipt must have version1, kind `offhost-copy`, matching
manifest/ciphertext hashes, `verified_at`, `verified_bytes`, `offhost:true`, a
nonempty destination ID, and evidence SHA256. Restore receipt must have version1,
kind `application-restore`, those same hashes/time/evidence hash and all six true
results: `decryption_verified`, `disk_integrity_verified`, `bindings_verified`,
`application_data_verified`, `task_history_verified`, `database_data_verified`.
Both private evidence files must exist and match their receipt hashes. These
are externally produced **trusted operator attestations**, not remote probes or
proof manufactured by this monitor. Only genuine completed checks justify the
receipts. Both must refer to the current encrypted archive: a newer backup
without its own verified copy/restore evidence deliberately reports unhealthy.

Defaults in the invalid example are daily backup and 30-day restore age; choose
policy through review. The monitor refuses future timestamps, stale evidence,
wrong recipient/archive/manifest, missing verification, changed ciphertext file,
and current binding/provider/admission inconsistencies. It sends no external
messages and cannot fix, delete, decrypt, restart or resume anything. Actual
large encrypted off-host copy/decryption/restore remains separate acceptance.
