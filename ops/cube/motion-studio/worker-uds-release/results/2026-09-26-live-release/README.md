# Production Motion socket release accepted

The real release ran on September 26 from 16:19:53 to 16:20:43 UTC: a
49.84-second scoped traffic fence. `window.py` held all four operator locks,
drained work, ran the reviewed natural-exit coordinator, preserved the closed
Motion data/home/config archive, installed the three source files and socket
drop-in, and restored the same controller, its management relays, Motion proxy,
timers and complete original Caddy route tree. The Cube worker was not cycled.

The old Motion process exited naturally with systemd code/status 1/0, without
forced termination. New PID198632, invocation `63eb617803ae4bf3a7c98055063424cf`,
passed TCP and Unix health, exact full seven-project/29-terminal-job state,
missing/wrong bearer denial, source/config hashes and socket DAC. The existing
6GiB/250% limits and TCP listener remain. The temporary debugger was absent.
The socket is service985:980 mode0660, parent0750.

`release-result-v2.json` independently checked live routes, controller/relay
identities, native Cube bindings/storage readiness, writer restoration, terminal
runner exit0 and reacquisition/release of all four outer locks and nested lock.
The first private report queried `runtime_binding` (Cube-only) for fleet counts;
v2 corrects that observation to the canonical sandbox provider column. Both
private reports remain. The fleet is still 73 apps: 1 Cube and 72 Docker.

`canary-after-release.json` records authenticated application acceptance at
16:26:48 UTC: app/home bytes, modes, links, UID, acknowledged PostgreSQL row,
source files, all four prior failed task results/events, preview and private
capture owner/foreign/anonymous access. No paid task or media generation ran.

`tests.txt` contains nine Node and 37 Python passes, zero skips, on the installed
Node v22.23.2, including native pidfd/systemd and installed-Sharp request tails.
`result.json`, `source-manifest.json` and `installed.json` pin tested/installed
inputs. Configuration and credentials remain private.

Private VPS review: `/opt/baarcha-bench/cube-motion-window-review-20260926-01`.
Private immutable plan: `/opt/baarcha-bench/cube-motion-window-plan-20260926-01`.
Private completed journal: `/opt/baarcha-cube/worker-01/maintenance/motion-release-20260926-01`.
The closed Motion archive is in its `motion-release/closed-data-home-config.tar`;
its hash is in the accepted receipt. It is not a complete fleet/worker backup.

The plan is consumed. Do not replay it or use historical PIDs as signal targets.
The completed journal's `locks_held:true` describes the final event before the
parent exited; the independent report establishes actual lock release.
Controller socket mount/app mapping, live guest capability/owned upload/range/
revocation acceptance, customer migration and controller replacement remain.
