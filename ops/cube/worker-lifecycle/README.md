# Worker lifecycle candidate — not installed

These files are preparation only. No host/nested unit was installed, enabled,
started or stopped. A concrete fixed Go stop coordinator now exists, with real
admitted Pause operations and controller database exclusion. The source-level
`STOP_COORDINATOR_IMPLEMENTED=false` installation gate remains false pending root
review and actual disposable-unit acceptance. A manifest flag cannot enable it.

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
a synthetic child inheritance test passes, while actual QEMU descriptor behavior
still needs isolated acceptance. Native disk FD checks remain required by cold
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
No startup reconciliation/marker-removal command is provided yet.

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
must be wired and accepted separately before traffic can reopen. An enabled
upstream target can bypass the reviewed target unless every service gets its
preflight dependency; installation must audit that explicitly.

`monitor` reads the private local lifecycle status, data free bytes and QEMU
cgroup memory events and writes sanitized local journald JSON only. It reports
missing reconciliation/backup-age integration and returns unhealthy until that
wiring exists. No external notifications or runtime mutations occur. Production
monitoring must additionally compare expected bindings with actual provider
state, admission count/pending age, nested component/hash drift, backup freshness
and off-host copy/restore proof. Never automatically delete, restart or expire
reservations in response to an alert.

## Validation and install gates

Run `python3 -m unittest discover -s ops/cube/worker-lifecycle -p 'test_*.py'`.
Tests use dummy child processes, a local UNIX QMP fixture and private temporary
locks; no installed systemd service or VM is touched. Existing backup tests also
exercise exact supervisor recognition and lock exclusion.

Remaining gates: independently perform/attest real traffic and stream drain;
implement boot reconciliation/marker removal and backup-age monitoring;
review exact private manifests; validate units with `systemd-analyze verify`;
prove normal signal/failed-drain/no-forced-kill behavior with a disposable VM;
prove pause→clean worker poweroff→paired encrypted backup→startup→authenticated
app/SQL/history continuation and separate isolated restore. Actual abrupt
power-loss recovery is separate acceptance and is not established by these files.
