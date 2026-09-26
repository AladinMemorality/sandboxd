# Planned nonempty worker cycle

`planned.py` is the normal maintenance caller for an already reconciled running
worker. It shares the traffic, task, direct-writer and operation-lock fences with
`maintenance.py`, but uses the running supervisor's ordinary stop protocol.
The incident caller still requires an actual `stop-blocked` source generation.

The planned caller requires a fresh `current-generation-planned-cycle` plan,
including its own installed hash, every existing maintenance pin, exact worker
and controller generations, explicit bindings, actual routing and provider job
counts. `--check` observes without changing routing or lifecycle state and
defers when customer work is active. The supervisor's `running-unreconciled`
status is not rewritten: native binding observation and current controller
readiness separately prove that its retained tenant bindings were reconciled.

After draining, the caller writes the native pre-drain receipt and requests
`systemctl stop --no-block baarcha-cube-worker-01.service` exactly once. It
requires the reviewed unit's infinite stop timeout, no SIGKILL, no restart,
process-only kill scope and no separate ExecStop. The existing supervisor owns
guest pause, retained management shutdown, graceful QMP powerdown and waitpid.
Only its immutable `stopped-clean` receipt, exact paused inventory, normal unit
exit and absence of both old processes permit startup. Receipt hashes use the
native Go field order and timestamp format; raw JSON file hashing is different.

Normal startup writes the standard pause/clean receipt references. It neither
creates nor consumes an external recovery authorization. A bounded read-only
management readiness check follows SSH/storage readiness before native startup
reconciliation. The existing transition journal updates boot pins, recreates
the exact controller and management relays, verifies bindings and only then
restores the complete original live routing and original writer states.

Every failure after the drain intent keeps the four operation locks and the
maintenance fence for explicit reconciliation. Never rerun a pending invocation,
rewrite lifecycle status, force-stop QEMU, or turn it into an incident recovery
without inspecting actual state and retained evidence.

This caller does **not** capture a full backup or certify Motion host-library
quiescence. Its receipts explicitly report `full_backup:false`. It is also not
an unattended host-reboot integration or the future Cube-only controller's
deployment configuration.

Run the complete lifecycle suite on native Linux as root before installation:

```sh
python3 -m unittest discover -s ops/cube/worker-lifecycle -p 'test_*.py' -v
```

The new tests cover actual native receipt serialization, mismatched generations,
missing/extra/unpaused guests, failed supervisor stops, immutable clean receipts,
normal service exit ordering and failures that must neither retry power nor
reopen traffic. Existing incident and transition tests remain part of the suite.
