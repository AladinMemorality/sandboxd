# Clean worker boot transition candidate

This source is **not installed or live-tested**. It fixes the immutable boot-pin
transition for an already proved clean, fenced worker cycle. It does not make
the existing worker supervisor an unattended production restart system. The
separate explicit external witness below can recover a stop-blocked supervisor
without changing its actual `worker-lost` result or inventing its clean receipt.

The native startup coordinator previously cleared its marker and published
`tenant_ready` while a stale storage observation still denied every new wake.
It now requires the actual fresh observation, matching controller/native worker
machine, boot and data identities, and both 96 GiB admission baselines before
clearing the marker. Read-only monitoring applies the same check. Shutdown can
still pause and release existing guests when observations are stale or missing.
Neither operation resets storage epochs or grants.

## Calling transaction

`boot_transition.py` runs on the outer Linux host as root. The maintenance parent
must already have applied the reviewed offline Caddy configuration, stopped the
three scheduled runtime writers, drained requests/tasks/provider jobs, disabled
restart and stopped the exact controller, and obtained an actual native pause
proof and supervisor clean-exit receipt. It must keep its four locks across
poweroff, cold capture if requested, worker restart, this transaction, and traffic
restoration:

1. `/opt/baarcha/deploy-release.lock`
2. `/opt/sandboxd/deploy-state/deploy.lock`
3. `/run/lock/cube-operator-acceptance.lock`
4. `/opt/baarcha-bench/cube-workload-operator.lock`

Pass the existing open file descriptions, not newly opened descriptors for the
same paths. Python caller example, after preparing the exact private plan and
new generation directory:

```python
subprocess.run([
    '/usr/bin/python3', '/usr/local/libexec/baarcha-cube-boot-transition.py',
    '--plan', str(plan), '--directory', str(generation_directory),
    '--inherited-lock-fds', ','.join(map(str, lock_fds)), '--execute',
], check=True, pass_fds=tuple(lock_fds), timeout=900)
```

On failure the parent **retains its original locks and offline routing** and
records the pending generation. It must not fall through to a `finally` block
that restores traffic or boots another worker. The helper closes only FDs it
opened itself. A new nested operator lock is held through a foreground pinned
SSH session after boot; its loss refuses continuation. An existing profiling
unit also refuses the transition.

The helper's fixed effects are:

- Verify source/config/binary hashes, outer machine and disk filesystem/inode/
  virtual-size identity; verify the actual supervisor, child PID/start ticks,
  fixed QEMU command and executable; probe the same worker machine/data UUID
  over the existing pinned SSH key.
- Validate the retained old-generation stop marker, complete paused inventory,
  pre-drain receipt, native pause proof and actual supervisor clean-exit receipt.
  A storage observation cannot authorize a new boot by itself.
- Write a private journal containing original and desired config bytes before
  any mutation. CAS only the observer boot pins, matching admission boot pins
  and top-level QEMU/worker generation in `worker-stop.json`, and the admission
  JSON in `runtime-compose.json`. All image, auth, allowlist, template, quota,
  filesystem and observer identity fields are preserved.
- Require the existing persistent observer sequence. Start the actual
  `baarcha-cube-storage-observer.service`; require a larger generation and fresh
  matching identities/free space. Never truncate sequence, admission DB or grants.
- Run the real native offline startup coordinator. It validates current SQLite
  binding/config/owner fingerprints, exact provider inventory and released
  paused admission before publishing its startup evidence and clearing the
  exact stop marker. An interruption after marker clearance can continue only
  from that exact hashed native startup evidence.
- Recreate only `sandboxd`, `cube-management-api` and `cube-management-proxy`,
  using the existing immutable image selection, with no build or pull. Verify
  unchanged environment except admission boot pins, the read-only observation
  mount, paired relay UID/GID/privileges/socket mount and actual network namespace.
  Journal the attempt before invoking Compose; never blindly replay an ambiguous
  recreation. CAS the exact resulting controller ID into `worker-stop.json`.
- Verify current-generation storage, health/ready, and real provider/binding/
  admission observation. Only then record final `tenant_ready`. Normal controller
  startup may resume an existing `always_on` backend under admission; the result
  reports that active count separately from zero guest wakes during offline
  reconciliation. The helper never opens tenant routes.

Interrupted config installation rolls **forward** only from the saved exact old
or new bytes. It never rewinds a new worker boot to old pins. Unknown edits,
missing proof, a new boot during continuation, stale observation, unexpected
binding or failed health leaves the journal pending and requires the parent to
retain its fence. Private journals contain existing operational configuration;
keep them root0600 within root0700 directories and out of public evidence.

## Input plan and proof preparation

The root-private plan has exactly these keys (`validate_plan` is executable):

- `version: 1`, `outer_machine_id`: canonical `/etc/machine-id`.
- `files`: SHA256 map for every fixed path in `PINNED`: worker-start config,
  Compose base/environment/active images, reviewed offline routing, installed
  observer/lifecycle/native start/native stop binaries, and lifecycle manifest.
- `initial`: SHA256 map for the exact existing worker-stop, storage-guard and
  runtime-compose files.
- `controller_image`: the existing immutable `sha256:...` image ID.
- `disk_identity`: `root.qcow2`, `data.qcow2`, `seed.img`, each with `inode`,
  `virtual_bytes` and backing `filesystem_uuid`. Qcow2 virtual size is read from
  the fixed header; physical allocation may change during an ordinary boot.

The caller creates a **new** root0700 directory under
`/opt/baarcha-cube/worker-01/boot-transitions/GENERATION` and preserves the plan.
`worker-start.json` must already point to this cycle's exact immutable pause and
clean-exit receipts. It cannot retain the historical empty-enrollment paths.
After stopping the worker, select the actual `pause-<hash>.json` emitted by the
native stop and `clean-stop-<hash>.json` emitted by the supervisor, verify their
proof equality/current generation, and CAS `worker-start.json` before pinning its
hash in the plan. Never generate `verified`, `stopped-clean`, or drain booleans
from a configuration example.

The caller should freeze these hashes while locks are held before the new boot.
The controller remains stopped during cold capture. Both disk identities must
refer to the existing pair; no backup clone, migration target or independently
replaced disk can be adopted by this helper.

## Current-generation maintenance caller and external evidence

`maintenance.py` now implements the narrow nonempty incident cycle. It accepts
the exact plan checked by `validate_plan`, preserves all four exclusive OFDs,
prepends scoped routes to the actual loaded Caddy tree (including leading exact
Motion aliases), drains canonical/runtime writers and pauses the complete native
binding set. `--check` performs read-only live preflight and records busy work as
`deferred`; it never cancels tasks. Execute requires quiet preflight, then allows
up to 1320 seconds for an already accepted task that raced the initial fence.
Already stopped Motion proxy containers remain stopped on reopen. Neither Motion
backend source/data capture nor its backend service restart is included. Its
disconnected upload/voice-deletion tails may finish local or fixed external voice
work; reviewed code has no Cube/controller mutation path. This is not proof of
full Motion-library quiescence. Heavy backup plans are explicitly rejected.

For the unretryable old supervisor, `external_clean.py` attaches a bounded wait4
witness before one QMP system_powerdown, requires guest-origin SHUTDOWN followed
by the exact child exit0, and retains the real supervisor worker-lost record. Its
external receipt closes exactly nine hashed artifacts. Native startup invokes the
pinned fixed verifier and rejects state-only claims. The next supervisor also
requires a one-use root authorization bound to this evidence, the unchanged
three disk identities and dead old process generations. Consumption is durable
and precedes spawning the next QEMU; a partial consumption fails closed.

The actual live supervisor has not been attached or powered down by these tests.
A disposable Linux parent/child proved only real strace format, readiness, normal
exit and safe tracer detach. Private native Go and Python fixtures validate the
remaining contracts; the nonempty real recovery acceptance remains required.

Installation maps lifecycle.py, external_clean.py, boot_transition.py and
maintenance.py to `/usr/local/libexec/baarcha-cube-{worker-lifecycle,external-clean,boot-transition,maintenance}.py`
respectively, root0755, alongside pinned `baarcha-cube-drain-observe.py` and
`baarcha-cube-worker-start`. Do not update the old supervisor state or enable the
unit. Root0700 `worker-01/maintenance` and `worker-01/boot-transitions` parents
are required. Use fresh directories for every check/execution:

```sh
python3 /usr/local/libexec/baarcha-cube-maintenance.py \
  --plan /ROOT_PRIVATE_PLAN.json \
  --directory /opt/baarcha-cube/worker-01/maintenance/check-01 --check
```

After explicit review, `--execute` with a separate fresh directory performs the
cycle. Failure after mutation holds the locks/fence indefinitely for reviewed
recovery; do not add forced unit runtime/stop timeouts. The caller journals STOP
path changes before pause and writes fresh pre-drain evidence afterward. START
changes only after the actual exit proof. No full backup or restore is claimed.

## Remaining unattended production integration and acceptance

The existing `baarcha-cube-worker-01.service` starts only the supervisor. It has
no generic pre-stop traffic/drain caller and no post-boot invocation of this
transaction. A standalone oneshot that releases its locks after partial failure
would be unsafe. **No unit enabling or `ExecStartPost` patch is included here.**
The incident maintenance parent above now provides explicit per-cycle proof
selection and lock ownership. General unattended clean-stop/start integration
still needs an approved systemd caller and failure-recovery policy.
That parent must run before Docker/Caddy teardown on a planned host shutdown;
on a whole-host restart it must prevent the ordinary online Caddy configuration
from exposing the stopped/old-generation controller before reconciliation.
Binding this ordering and its failure state into systemd is an outstanding
acceptance gate, not a claim established by helper unit tests.

Required acceptance before enabling automatic worker startup: a nonempty owned
canary clean cycle, current paused data/SQL preservation, fresh boot pins across
all three configs, larger observer generation without grant reset, current
controller/relay IDs, successful admitted wake, and a negative interrupted cycle
that stays fenced. Power loss remains a separate disk recovery path. The stop-blocked external
witness requires its own actual live acceptance; no fake clean receipt, forced
QEMU stop, stale checkpoint or destructive fallback is allowed.

## Tests

`python3 -m unittest discover -s ops/cube/worker-lifecycle -p 'test_*.py'` covers
CAS and journal failure windows, exact receipt/generation checks, preservation
of policy/auth/scope, no blind recreation replay and fresh-observer refusal.
Native root Linux tests in `internal/workerstop` use the real SQLite schema34,
actual root-owned observation files and concrete HTTP provider fixtures. They
must be run on Linux as root; Darwin passes of other packages do not count as
native startup validation. The shared Go/Python stop-marker hash fixture pins
the interrupted reconciliation evidence contract.
