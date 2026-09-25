# Empty worker host enrollment candidate

`execute.py` archives the exact reviewed runner whose read-only `--check-only`
phase passed against the actual VPS on 2026-09-25. The service-changing
`--execute` cycle has **not** been run or accepted by this evidence.

The runner is deliberately pinned to the reviewed controller, platform revision,
worker machine/boot/QEMU generation, installed nested helper, source artifacts,
existing hold drop-in, eight terminal provider job groups and empty Cube
bindings. Refresh and independently review those identities after any deployment
or reboot; this is an operator procedure, not generic unattended automation.
The platform pin reflects the live revision at check02, not a later release.

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

Sixteen tests cover known/unknown provider states, route-scope refusal, retained
failure recovery, optional resize, and exact hold-file acceptance/refusal. They
do not substitute for the still-required real service-changing cycle.
