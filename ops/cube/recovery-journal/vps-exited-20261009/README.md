# VPS exited snapshot recovery — 9 October 2026

The third real-app density run stopped when runtime `38a1bd34cfa54a9aa47b4b121d035e46` restored its main process, then immediately exited with status 0. This is a different symptom from the earlier kernel panic. Its root cause remains unproven.

The canonical sandbox `01M24GEQ88YGHHH3WPC9WD9DY0` was recovered to `d95e537c7aee4dda96d9bbc13f61d4f9`, using a fresh, verified current-disk source archive. The original native disk, a matching clone, controller backup, credentials, and failed journals remain private on the VPS. No B200 request or model call was made.

These are exact-incident operator scripts, not an unattended repair service. They require the existing operator locks, stopped controller, drained provider jobs, exact source identity, absence of live disk handles, and unchanged disk hashes. Recovery uses the existing durable offline Session, with a charged reservation, quarantine, fresh credentials, source/home/history/config verification, and atomic binding commit. Package access is restricted to the npm registry.

`stage.py` retains the native clone and prepares private configuration; `run.py` enters maintenance and starts recovery. The first attempt created the replacement but could not acknowledge its 768 MiB resource profile because the recovery store still required 2 CPUs / 2048 MiB. The shared fix now validates the durable resource contract, retaining the legacy uniform check when no weighted contract exists. Tests cover accepted profiles and rejected CPU/memory mismatches.

`adopt.py` authenticated and adopted the exact existing replacement; it did not create another VM. Its ingress credential was recovered privately from the VPS-local native proxy metadata. The subsequent content checks passed. `continue.py` completed the journal after correcting the incident receipt to use the empty task fingerprint for a source with zero tasks. Both failed attempts are retained.

`retain-source.py` enrolls the unbound, quarantined, natively stopped source in the existing inactive inventory. The ordinary worker audit still rejects it if it becomes active. This preserves the source without confusing it with a canonical app. `wake-check.py` performs repeated preview module and stop/wake checks before resolving the density-run barrier.

Never rerun a creating journal, delete an original, or use an old memory snapshot against a newer disk. Private artifacts and logs must not be committed. The 50-app acceptance run, fresh fleet backup, remaining supervisor rollout and public app checks are tracked separately in `../../vps-capacity-20261008/`.
