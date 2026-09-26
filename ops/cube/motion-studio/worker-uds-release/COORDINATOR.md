# Narrow worker release coordinator

`release.py` is prepared source, not an installed service or an execution receipt.
It installs exactly the three pinned files in `manifest.json` and
`20-cube-uds.conf`. It does not build, install dependencies, update the customer
frontend, register a template, alter network rules, migrate an app or reopen traffic.

## Fence contract with the maintenance caller

The caller must establish a real maintenance window **before either `--check`
(the default) or `--execute`**. Caddy/queue snapshots alone cannot fence direct
worker calls. The reviewed maintenance caller must:

1. Hold the four existing outer flocks, block public/platform/controller admission,
   and preserve exact actual online routing for its own later restoration.
2. Gracefully stop the exact reviewed Motion proxy Docker container(s), and stop
   `baarcha-motion-access.timer` plus `baarcha-motion-access.service`. Stop the
   pinned controller so it cannot auto-wake a writer during this window. Do not
   delete containers, workspaces, or app identities. A paused Docker container
   does not satisfy this contract; it can retain frozen TCP connections.
3. Verify zero established TCP connections to/from port 8332, then authenticated
   complete project state and zero queued/running jobs. The dedicated worker
   remains running until the coordinator performs its own stop. Pin the full
   project-response canonical hash, not only counts. The caller is responsible
   for an exhaustive writer inventory; this script cannot discover holders of a
   bearer credential. The installed scoped IP/MAC filter and its refresher are
   preserved, not changed or presented as new isolation acceptance.
4. Supply a fresh, root0600 configuration with actual boot/controller/source
   identities, reviewed live maintenance-Caddy JSON hash, and a Linux BOOTTIME
   expiry no more than one hour away. Every fence check verifies the live routing,
   stopped controller/writers/timer, and empty TCP connection set again.

Read-only observation on 26 September found the current Motion proxy binding
`01M3CKN99ZF90BEEA4DS66YAQV`, container
`02503dfa5cf882692436fcef4fa6e049b0480441b9347e506d2dec225cefedb0`, image
`sha256:f05e2d5103acbc1bda4ba22d618eabba93307f914dd26bf2f2f3f4163c458434`.
The worker remains PID3282578 and the refresh timer active. These observations
are **not** a maintenance handoff or execution configuration; refresh all pins.

The caller can pass the four held descriptors through `subprocess.run(...,
pass_fds=tuple(fds))` and `--lock-fds FD0,FD1,FD2,FD3`, in this exact order:

- `/opt/baarcha/deploy-release.lock`
- `/opt/sandboxd/deploy-state/deploy.lock`
- `/run/lock/cube-operator-acceptance.lock`
- `/opt/baarcha-bench/cube-workload-operator.lock`

The coordinator checks inode/device/root ownership. For inherited descriptors,
an independently opened same-inode `LOCK_SH|LOCK_NB` probe must fail **before**
touching the inherited lock; unlocked/shared descriptors are rejected without
conversion. A subsequent same-OFD `LOCK_EX|LOCK_NB` check rejects a different
open-file description holding the exclusive lock. It closes its duplicates
without unlocking the caller, including on failure. Without inherited descriptors it acquires these same locks
itself. No second, unrelated maintenance lock is invented.

## Configuration and invocation

`validate_config()` defines the exact configuration schema. In particular:

- `source_before` maps **all twenty** reviewed comparison paths to `snapshot()`
  output (hash, inode, device, UID/GID, mode); new helper paths must be absent.
- `unit_sha256`, `env_sha256`, and every existing `.conf` drop-in are pinned.
  The original environment file must remain canonical root0600. No credential
  belongs in the configuration, command arguments, journal or stdout.
- `service_properties` includes the actual `User`, `Group`, `MemoryMax=6442450944`,
  `CPUQuotaPerSecUSec=2.500000s`, and `KillMode=control-group` values.
- `project_count` preserves at least the seven known projects, while
  `projects_sha256` covers the full response, including assets, renders and jobs.
  New legitimate activity requires a newly reviewed baseline, never a lower bound
  substituted for the content hash.
- `fence` contains `caddy_sha256`, explicit `writer_containers`, exactly the two
  access-refresh units in `stopped_units`, and `expires_boottime`.

Stage the unchanged manifest, comparison, drop-in, coordinator, and pinned source
tar together in a private reviewed directory. The coordinator additionally pins
its companion manifest/comparison/drop-in hashes internally. Create a **new**
output directory per invocation; existing stages refuse replay.

```sh
python3 /PRIVATE-STAGE/release.py \
  --config /PRIVATE-STAGE/config.json --config-sha256 REVIEWED_CONFIG_SHA \
  --source-sha256 REVIEWED_RELEASE_PY_SHA \
  --tar /PRIVATE-STAGE/worker-uds-source.tar \
  --stage /PRIVATE-STAGE/check-01 --lock-fds FD0,FD1,FD2,FD3
```

After reviewing that exact check, invoke the same pinned inputs with `--execute`
and a distinct fresh stage. The caller supplies bounded process resources and a
maximum 30-minute wall timeout; individual commands are bounded (backup at most
600 seconds, stop120, start60, readiness30). Use at most one CPU/512MiB for the
coordinator. These are orchestration bounds, not changed worker limits.

## Backup, validation and failure behavior

After stopping only `baarcha-motion-worker.service`, the coordinator verifies
inactive/dead, `Result=success`, and either normal exit0 (`ExecMainCode=1`,
`ExecMainStatus=0`) or ordinary SIGTERM (`2`/`15`). Timeout, OOM, SIGKILL,
core dumps and nonzero exits refuse backup/installation and preserve the fence
for manual review; they do not trigger an automatic stop retry or restart. The
observed result/code/status is journaled. It also requires no PID/control PID,
no remaining cgroup processes, and no process
under the dedicated service UID. It creates a closed GNU tar of data, home,
worker.env, base unit and all prior drop-ins, preserving numeric ownership,
symlinks, hardlinks, ACLs and xattrs without dereferencing or extracting. Special
files require explicit review and cause refusal; they are never silently omitted.
A private bounded inventory and tar hash are fsynced before installation. The
initial limits are 500,000 entries/20GiB logical data and required disk headroom;
exceeding them requires a separate reviewed change.

The source install uses same-directory atomic rename with file/directory fsync.
The worker is stopped throughout the three-file/drop-in change. Verification
checks loaded unit/drop-ins, unchanged limits/credential/TCP configuration,
service UID, socket DAC, both transports' health, full authenticated project
state, and missing/wrong credential401 responses. No upload/render is submitted.

A failure after stop attempts bounded **source-only** rollback while the fence
still holds. Original source bytes/ownership/modes and absent helper/drop-in state
are restored; data/home are never rewound. A file changed by another writer is
not overwritten. Unknown stop/fence/rollback failures remain failed and require
operator review. SIGTERM/INT enters the same cleanup; SIGKILL/power loss cannot
promise cleanup. Existing journals are never automatically resumed or replayed.

Success and rollback both leave routing, controller/writer containers and refresh
units fenced. Only the reviewed caller may reopen them after inspecting the
actual outcome. `fence_reopened:false` means this script did not reopen; it is not
a substitute for the caller's final live routing verification. Do not run this
coordinator while the worker power-recovery incident remains unresolved.

## Local validation

```sh
python3 -m unittest discover -s ops/cube/motion-studio/worker-uds-release -p test_release.py -v
```

Tests exercise candidate/path/link rejection, private metadata, atomic file
replacement, drift refusal, failure ordering and source-only rollback preserving
newer customer bytes, complete project-state comparison, active-job refusal,
expired/drifted fences, running writers/controller, active timer and inflight TCP
refusal. Real local kernel flock tests cover unlocked/shared descriptors without
upgrade, a different holder, the correct shared OFD, and retention of all four
caller locks after an exception. The actual pinned tar also passed parser/hash validation locally.
No live stop/install/start or Linux systemd acceptance was performed by these tests.
