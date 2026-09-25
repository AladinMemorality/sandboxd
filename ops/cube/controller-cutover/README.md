# Single-app controller enrollment

`run.py` prepares one explicitly reviewed synthetic app for Cube. It does not
migrate owner apps, enable global routing, create/delete guests, power the worker,
build an image, or rewind the controller database. Default invocation performs
only the locked preflight and writes private evidence. Activation requires the
separate `--execute` flag and exact source/input hashes.

The runner consumes the private output of `../controller-enrollment/prepare.py`
plus a reviewed `inputs.json`: current full controller/image IDs, both deployed
revisions, worker boot, zero-Cube database counts, all candidate/original file
hashes, worker-stop and observer configuration hashes, and host relay unit hashes.
No obsolete controller/image/worker generation is inferred from the reused
enrollment helper. Stage the reviewed `execute.py` helper and exact
`06dd5e379fa594ec6fbfa37d36971fa5703ff5969b8766bac98196ee10c0f92e`
`drain_observe.py` alongside the runner. The observer source remains unchanged.

The four standard deployment/operator locks span the entire operation. A nested
worker fixture lock and independent zero-guest/API/CLI/task/job checks exclude
allocation overlap. **These locks do not fence direct operator installers that
ignore them. Obtain the coordinating operator's explicit writer handoff before
activation.** Successful preflight alone is not that handoff.

The sequence is:

1. Verify exact controller, revisions, candidate rendering, installed guard,
   fresh observation, current Caddy/maintenance hashes and unchanged pending
   Caddyfile. Preserve the actual live configuration, including custom aliases.
2. Back up and install only the reviewed maintenance JSON variants; load drain,
   stop the three reviewed writer timers, await tasks/thumbnails/TCP drain, then
   load offline and verify actual TLS route responses. Gracefully stop the exact
   controller with unlimited Docker stop grace but a bounded operator wait.
3. Prove the SQLite descriptors are closed, create and fsync an integrity-checked
   SQLite backup, and independently copy/fsync the key and configurations. This
   is a controller rollback safeguard, not a full paired worker/data backup.
4. Install the two candidate overrides atomically, start the fixed host Unix
   relays, and recreate the controller and both management sidecars together
   using the same immutable images, with no build/pull/dependency recreation.
5. Verify readiness, exact environment/image, actual shared network namespace,
   read-only observer/socket mounts, UID/GID/capability constraints and a fresh
   storage observation. Refresh only `worker-stop.json.controller_id`.
6. Confirm zero allocation and unrelated container identity preservation; restore
   exact prior live Caddy and timer activity. Existing accepted Docker recreations
   may finish during drain only when same app/sandbox/name/image and canonical
   old/new container bindings are proven. Their private before/after evidence is
   retained; post-drain identities then become immutable for the transition.

On preallocation failure the runner restores the original Compose overrides and
same controller image, restores the original relay activity, and restores traffic.
The old container ID cannot be recreated; the replacement ID is pinned in the
stop coordinator. It never restores the SQLite backup over a live/current DB.
Partial override installation is handled independently per file. If traffic was
already reopened, it is re-drained before rollback. Any unexpected allocation,
drift, uncertain shutdown or failed rollback retains operator locks; the report
states whether offline routing was actually confirmed. A private
`release-after-manual-recovery.json` containing exactly
`{"job":"ABSOLUTE_JOB_PATH","operator_owns_remaining_fence":true}` hands that
remaining responsibility back to the operator without claiming restoration.

## Reviewed live alias

`routing_candidate.py` is a pure transformation for the current single added
MotionStudio preview alias. It refuses any other configuration difference. Drain
preserves the complete alias; offline substitutes the already reviewed preview
503 handler while preserving the exact host and `@id`. The separate idempotent
alias installer therefore still sees its route ID during maintenance. All other
routes remain untouched. The alias points through Traefik; it is not a direct
Docker or guest-IP route. The pending on-disk Caddyfile is never applied or edited.

## Validation

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s ops/cube/controller-cutover -p 'test_*.py'
```

Tests cover exact allowlist fields, rejection of global/direct-NIC allowances,
relay privilege/namespace/mount constraints, normalized Compose bind defaults,
canonical drain recreation boundaries, partial-file rollback without DB rewind,
refusal after allocation, post-reopen re-fencing, and preserved alias identity.
Actual read-only preflight records are separate from activation evidence.

The staged operator invocation supplies the reviewed source and config hashes:

```sh
python3 /opt/baarcha-bench/cube-controller-cutover-tools-20260925/run.py \
  --config /opt/baarcha-bench/cube-controller-cutover-tools-20260925/inputs-r2.json \
  --config-sha256 REVIEWED_CONFIG_SHA256 --script-sha256 REVIEWED_RUNNER_SHA256 \
  --job /opt/baarcha-bench/cube-controller-cutover-UNIQUE_REVIEWED_RUN
```

Only after successful preflight and explicit writer handoff may the coordinating
operator add `--execute`, using a new job directory. Preserve every failed
preflight; never overwrite evidence or remove a drift check to make it pass.
