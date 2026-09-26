# Motion natural-drain preparation

Final native validation passed **9 Node tests and 30 Python tests**, with no
skips, under the installed Node v22.23.2 binary. The suite exercised the real
installed Sharp library and three disposable Linux systemd services using pidfds.
Only owned fixtures received the debugger signal or listener-close operation.

The request-tail fixture disconnected its HTTP client while a child process was
pending. After listener close, it completed Sharp image conversion, wrote/fsynced
the image and exited normally. Other tests covered idle exit, active connection
refusal, mismatched PID/UID/argv/cwd/listeners, temporary debugger cleanup, and
the release wrapper's reuse of only its own already observed stopped generation.
Pending voice mutations and ordinary SIGTERM do not qualify as full drain proof.

The exact final source hashes are in `source-manifest.json`; runtime hash and test
log hash are in `result.json`. These fixtures were bounded to one CPU/512MiB per
runner/service and 128 tasks. The wrapper held the four existing operation locks
throughout the native suite. Production Motion's PID, invocation and restart
count were unchanged; no remaining fixture service or loopback debugger listener
was observed afterward, and controller readiness remained `ready`.

Private VPS stage: `/opt/baarcha-bench/cube-motion-natural-drain-20260926-07`.
Earlier failed test stages remain retained. They caught an idle debugger-context
cleanup race, the installed distribution's UID1001 ownership, a startup socket
observation race, and a disposable unit's missing post-exit dependency reference.
The final fixture uses the same enabled-unit dependency pattern as production.

The three-file UDS application candidate and drop-in are unchanged. The operator
release now invokes the tested natural stop; no live worker stop/install/start,
Motion customer migration, full paired backup or global deployment is claimed.
