# Empty worker host enrollment

The actual empty worker cycle completed on2026-09-25 at16:08:10UTC. It required
three reviewed programs, whose exact bytes are archived here:
`executed-20260925.py`, `resume-root-20260925.py` and `cycle-root-20260925.py`.
See `actual-enrollment-2026-09-25.json` for receipt hashes and precise limits.
The original runner alone did **not** pass. Check06 passed before maintenance;
the actual cycle then stopped at a missing installation directory and again
when read-only observer SQLite handles correctly triggered the DB-user fence.

The first continuation added the canonical installation directory and resumed
installation/boot while the original process retained all four locks. The second
used actual supported `pidfd_getfd` to duplicate exactly those four open file
descriptions before releasing the original process, closing its retained SQLite
handles without a deployment-lock gap. Four independent contenders remained
blocked. Only then did the real coordinator stop, produce a clean receipt,
restart and reconcile the worker. No data files or binding credentials were
substituted. Scoped controller traffic returned at16:08:09UTC; the interval from
controller-fenced evidence was approximately20minutes25seconds. This is not a
sub-minute maintenance result. Unrelated inference remained outside the fence.

A legitimate Docker recreation completed during admission drain, before the
controller's consistent closed SQLite backup. The exact one-container delta
was separately reviewed and compared against that backup; the other74 container
identities remained unchanged. Original baseline and failure evidence are
retained. A newly created legitimate project was also accounted for before
maintenance, raising the expected fleet from66 to67.

`execute.py` is a subsequently hardened source candidate: it refuses missing or
unsafe `/usr/local/libexec` before fencing and pins the deterministic-close
observer from commitcd194ae. Its local tests passed; the revised file itself was
not rerun as a fresh live enrollment. Archived continuation files are historical
one-use evidence, **not** generic recovery tools to replay against new PIDs.
Global Cube activation and automatic worker boot remain disabled. This empty
cycle does not prove an application backup/restore or authorize tenant migration.

The runner is deliberately pinned to the reviewed controller, platform revision,
worker machine/boot/QEMU generation, installed nested helper, source artifacts,
existing hold drop-in, eight terminal provider job groups and empty Cube
bindings. Refresh and independently review those identities after any deployment
or reboot; this is an operator procedure, not generic unattended automation.
The platform pin is2e61df6 and fleet count67 from actual check06. Any later
release or fleet drift must be independently reviewed before reusing a candidate.

`--check-only` takes the same four existing deployment/operator locks, performs
read-only queries and creates a new root-private evidence directory. It does not
reload Caddy, stop timers/services, resize disks, install files or set review
flags. `--execute` requires separate root review and the actual operator handoff.
It is not authorized merely by the successful check-only result. Optional
`--resize-data-to-448g` changes only the reviewed stopped data disk and then the
whole-device XFS filesystem; it does not guarantee fully backed NVMe capacity.

A failed mutating phase retains its locks for explicit recovery. The two
`--request-recovery` actions either restore pre-worker traffic when safe or hand
remaining fences to manual recovery. Neither removes a stop marker, forces QEMU
to exit, or silently discards the failure. Existing hold files remain pinned and
are never removed. The initial manual empty-QEMU handoff is separate from the
first genuine supervisor/coordinator stop receipt.

No private config, API key, environment value, tenant file or production SQLite
is included here. Actual private evidence remains in the stage identified in
`check-only-2026-09-25.json`. The native coordinator independently enforces DB
exclusion, binding inventory and startup reconciliation. Unrelated inference
and live transcription are not classified as runtime writers.

Local focused regression command:

```sh
python3 -m unittest discover -s ops/cube/cutover/enrollment -p test_execute.py -v
```

Thirty-one enrollment tests cover known/unknown provider states, route-scope refusal, retained
failure recovery, optional resize, and exact hold-file acceptance/refusal. They
do not replace the separate live evidence or prove the unexecuted hardening. Eight
subprocess tests use actual local flock ownership/contenders with synthetic
lock files and boot IDs; no real worker or customer resources are involved.

The persistent SSH lock-holder runs inside the worker, acknowledges each
heartbeat and is reacquired after every boot before readiness checks. The outer
acceptance lock is a distinct inode/scope and never substitutes for it. Normal
check-only/final completion releases the child explicitly; unexpected exit or
wrong-boot acknowledgment blocks further work. Confirmed exact-QEMU poweroff
permits closing the old SSH pipe with a bounded wait, never killing it. Outer
deployment/workload locks remain held through that reboot interval.

The HTTPS preview probe uses a separately root-private existing-certificate host
input, pinned by SHA and strict host syntax. It runs only after the offline
fence loads. It never uses insecure TLS or a nonexistent synthetic hostname.
