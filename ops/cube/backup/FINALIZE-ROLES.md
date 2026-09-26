# Closing configuration after a clean worker stop

`finalize_roles.py` is prepared and unit-tested; it has not captured production
roles. Run it after `capture_roles.py capture-frozen` and a genuine coordinated
worker shutdown, before `cold_pair.py capture`. It never stops, starts, wakes or
repairs a source.

The first phase closes large Docker homes, library and platform PostgreSQL
files before the native pause proof's ten-minute capture window. That phase
cannot contain the next stop's receipts or final `worker-start.json`. This
second phase creates a **new** private directory with three new configuration
archives and a map selecting the already closed heavy roles. It preserves the
old directory and its `complete.json` byte for byte.

The parent must continuously retain the existing platform deployment, runtime
deployment and operator acceptance locks across both phases and the shutdown.
Pass those same open file descriptions through `--inherited-lock-fds`; the
helper refuses to acquire a new set as a substitute. The parent should also
retain its workload lock throughout the overall maintenance transaction.
The helper obtains the exclusive worker lifetime and canonical SQLite locks,
requires the worker unit inactive, and repeats the existing stopped controller,
closed Docker sources, writer-unit and routing checks around every archive.

Prepare a new role config from the original closed config. Only the following
changes are accepted:

- Update the hash of `/etc/baarcha-cube/worker-start.json`, which must have been
  pinned and included in the original worker-config role.
- Add the exact fresh native pause and matching clean-stop receipt paths and
  hashes, and add those same paths to worker-config inputs. An externally
  witnessed stop additionally requires its validated evidence closure below.

Every other source, inventory, key, policy, hash and role entry must match. The
parent's frozen identity sidecar must equal the unique regular member inside
the independently hashed original controller-config tar. The final archives
retain the existing immutable image export and original container definitions.

In the parent process with its lock FDs inherited:

```text
python3 finalize_roles.py \
  --config /PRIVATE-GENERATION/roles-config-final.json \
  --closed-roles /PRIVATE-GENERATION/closed-roles-01 \
  --closed-complete-sha256 ACTUAL-FIRST-PHASE-COMPLETE-SHA256 \
  --output /PRIVATE-GENERATION/closed-config-final-01 \
  --inherited-lock-fds FD1,FD2,FD3
```

The output `complete.json` maps six role files to exact paths/hashes and records
the controller key and selected pause/clean receipts. Use these paths when
assembling the separate cold-pair capture config. A failed or ambiguous run
retains evidence, never overwrites an existing generation, and cannot be
treated as a completed pair or restored application. The parent keeps its locks
and routing fence until the full maintenance transaction is resolved.

Ordinary cycles require the actual supervisor `stopped-clean` receipt. The
separate `externally-stopped-clean` recovery schema additionally requires
`/usr/local/libexec/baarcha-cube-external-clean.py` already installed and pinned
in the original closed role configuration. Its validator rehashes and verifies
the exact nine-file wait/QMP evidence closure. All those files must be added with
their exact validated hashes to the final worker-config archive. The validator
hash must also equal the startup config's `external_verifier_sha256` field.
The validator is called again around each archive; changing the state string or supplying a
receipt without its complete evidence is rejected. Neither path rewrites the
original supervisor status or constitutes independent application restore proof.

Validation: `python3 -m unittest discover -s ops/cube/backup -p 'test_*.py'`.
Tests cover configuration drift, substituted sidecars, changed heavy artifacts,
failed parent generations, receipt mismatch and required lock handoff. They do
not substitute for a native stopped-source capture and independent restore.
