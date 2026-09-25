# Synthetic PostgreSQL crash check

This is a proposed destructive-stage fixture, not approval to crash a worker.
Only the coordinator may interrupt the **new, non-customer worker VM** after
reviewing its identity and inventory. The source here performs no shutdown,
network policy change, guest creation, metadata repair or automatic recovery.
Do not use the old benchmark worker or any production tenant workspace.

## Preconditions and bounded scope

- First pass the current node-postgres full API lifecycle fixture, including
  write/read, publication/remix separation, reload, pause/resume, source restore
  and private-home roundtrip. Independently confirm its guests are deleted.
- Start exactly one new synthetic project on the reviewed node-postgres template,
  using the configured authenticated control plane and reverse channel. Direct
  NIC egress remains denied. Require fresh inventory to contain only this owned
  immutable guest ID, and no template jobs or customer guests. If metadata is
  absent or inconsistent, stop; do not weaken ownership checks.
- Cap the guest at 2 CPUs/2 GiB. One operator request at a time; no more than
  two commits and ten verification requests. Overall preparation/verification
  deadlines: ten minutes each. One power interruption only, performed separately
  by the coordinator. No repeated fault loop, raw packet operation or model call.
- Preserve a private outer-host stage, mode0700/root-owned. Require the stage's
  exact disposable marker, current worker machine ID, guest ID/template/resources,
  node-postgres preset, synthetic app ID and fresh creation timestamp. Escrow
  the guest's supervisor/traffic credentials in a separate0600 file; never put
  credentials in the report, command arguments, console output or repository.

## Probe installation and sequence

`probe.mjs` is a disposable app worker, not production application code. Copy it
only into the new guest's `/home/sandbox/workspace/app`. Install a mode0600
`crash-fixture-token` containing a cryptographically random64hex capability and
`config/crash-fixture.json` containing:

```json
{"purpose":"DISPOSABLE_POSTGRES_CRASH_ONLY","fixture":"<random32hex>"}
```

Add one supervised worker with command `node probe.mjs`, `restart_after_task:
false`, and activate the manifest through the normal API. It listens on3006;
reach it only through the authenticated Cube proxy and fixed guest Host. The
capability is additionally required in `Authorization: Bearer ...`; never log
these headers. It makes no outbound network request and uses the existing
private PostgreSQL Unix socket. Startup does **not** write marker files or SQL.

1. Generate separate baseline/latest32hex nonces on the outer coordinator.
   `POST /commit` body `{fixture,phase:"baseline",nonce:<baseline>}` must return
   `matches:true`. This writes the app marker and home marker, fsyncs their files
   and directories, then commits the SQL row with `synchronous_commit=on`.
   The transaction also requires `fsync=on` and `full_page_writes=on`.
2. Quiesce through runtimed. Export the complete private app and reviewed private
   home to **separate outer-host files**, with the added `.cube-crash-fixture`
   home entry preserved alongside `.baarcha-postgres`. Compute and retain each
   archive's digest, byte count, reviewed manifest, creation time and baseline
   marker. Fsync these files and stage directory before proceeding. Do not use
   source-publication exports for private database backup.
3. Resume normally and wait for the original app's real `/health` SQL check and
   probe. `POST /commit` with the **different latest nonce** must return the exact
   latest marker in app, home and database. Record the acknowledged response and
   time durably outside the worker. Call `POST /verify` with that marker to read
   it back. No more exports, commits, quiesces, pauses or checkpoints afterward.
4. Return a review checkpoint to the coordinator containing sanitized worker and
   guest identities, hashes, timestamps and marker expectations. The coordinator
   checks zero unrelated guests/jobs again, then performs the separately reviewed
   power-loss interruption of the fresh worker VM. A graceful guest stop is not
   an equivalent test. This repository probe does not contain a crash command.
5. After the coordinator boots the same worker and reviews recovery, reconnect
   only the exact escrowed guest through the ordinary authenticated path. Do not
   create/reimport a guest or reissue `/commit`. `POST /verify` must return200 and
   `matches:true` with the **latest** nonce in all three locations. Also perform
   the normal app's actual PostgreSQL health/read, not just control-plane HTTP200.
   The baseline archive is deliberately older, so restoring it cannot pass.
6. Independently compare post-recovery guest ID, template/resources, owner binding
   and credentials with the escrow. Save any mismatch, stale baseline or missing
   data as a failure. Do not implement a fallback silently. A separate reviewed
   manual restoration can later test the older backup and must be labeled as
   such, never as preservation of the latest committed data.
7. Delete only the exact owned guest through the normal controller and independently
   require Cube GET404. Retain sanitized results and private artifacts for review;
   remove the stage credentials only after cleanup is proven.

## Prepared checks and limits

`node --test probe.test.mjs` verifies marker comparison, real filesystem writes,
private modes and rejection of symlink substitution. These tests do not simulate
power loss or prove PostgreSQL crash recovery. `probe.mjs` deliberately keeps the
verification route read-only: an old/missing SQL row or file fails even if the
HTTP server is healthy. Permission to prepare/build this source is not permission
to execute the crash stage. Actual database, guest and post-power-loss acceptance
remain pending until the coordinator runs and records them.

## Runnable outer-host coordinator

Build `main.go` and `main_test.go` in a temporary `control-plane/cmd/operator-crash`
directory. The executable must run on the outer operator host, whose fixed
localhost20300/20080 endpoints and pinned inner SSH20222 key are already configured.
It reads the existing private Cube API credential file without printing it.
Copy the executable and exact `probe.mjs` into a new root0700 stage named
`/opt/baarcha-bench/cube-crash-...`, plus the passed PostgreSQL lifecycle report
as mode0600 `postgres-lifecycle-report.json`.

The coordinator, not this preparation step, creates mode0600 `handoff.json`:

```json
{"purpose":"DISPOSABLE_CUBE_CRASH_HANDOFF","worker_machine_id":"<reviewed /etc/machine-id: 32 lowercase hex>","no_customer_guests":true,"previous_family_cleanup_verified":true,"expires_at":0}
```

Replace the expiry with a Unix timestamp within30 minutes. Then explicitly run:

```sh
./crash-coordinator run /opt/baarcha-bench/cube-crash-REVIEWED_STAGE
```

The process acquires outer and worker-local operator locks, requires zero all-state
inventory and prior PostgreSQL acceptance, creates one owned guest, and journals
credentials privately **before** installation. It installs the bounded probe,
exports baseline app/home, acknowledges distinct latest markers and prints only
`CHECKPOINT_READY`. It then waits up to15 minutes for the operator's separate
power action and mode0600 `power-loss-complete.json`:

```json
{"purpose":"DISPOSABLE_CUBE_CRASH_COMPLETE","fixture":"<report.fixture>","previous_boot_id":"<report.worker_boot_before>","method":"operator-reviewed-power-loss"}
```

Write that confirmation only after independently reviewing the exact fresh worker,
performing the authorized loss and verifying restarted control-plane readiness.
The coordinator reacquires the worker lock lost during reboot, requires a changed
boot ID but unchanged machine ID/data UUID, checks the exact guest metadata/resources,
uses only ordinary connect if the original guest is paused, and verifies the
latest marker in all three stores. It never issues a shutdown, reset, restore,
recreate or import after the power checkpoint.

On complete success it deletes only its exact owned guest and requires GET404.
On a failure **after** the power checkpoint, the guest and escrow remain for
forensic review; the report explicitly says cleanup is unproven. After review,
`./crash-coordinator cleanup STAGE` performs only exact-owner deletion. To resume
verification after an interrupted coordinator, `./crash-coordinator verify STAGE`
requires the same escrow and completed power-loss confirmation; it does not
rewrite markers. Before-checkpoint failures attempt bounded owned cleanup.
The complete coordinator has a30-minute deadline; the guest pauses at its
30-minute timeout. An ambiguous create retains the durable intent for operator
investigation and never deletes guests by broad inventory matching.

The Go unit fixtures use only synthetic local HTTP/filesystem operations. They
check stale or partial data rejection, exclusive private evidence creation,
exact zero inventory parsing, and refusal to delete a foreign-tagged guest.
These are preparation tests; no crash survival result is implied.

Worker identity is pinned SSH host authentication plus exact `/etc/machine-id` (32 lowercase hex), data filesystem UUID, and boot ID. Machine ID alone is not authentication; this avoids assuming QEMU exposes a DMI UUID.

## r4 private fixture installation fix

The installation step first quiesces the fresh owned workspace, exports through
`/export/private-workspace-v2`, and imports through
`/import/private-workspace-v2`. It validates the canonical private archive before
and after modification. Original ZIP headers, permissions and symlink targets
are preserved; the capability is created0600 and other fixture files0644. No
publication filter or source-publication endpoint handles this private material.
The coordinator observes supervisor reexec before resuming.

Regression tests prove that the capability is excluded by the publication filter
but retained in a valid private archive, and exercise the authenticated HTTP
sequence quiesce → private export → status → private import. New attempt stages
must include the full passed PostgreSQL lifecycle report as a root0600 file; the
receipt and proof are not optional packaging.

## Actual r4 result

The reviewed isolated worker interruption was executed once. Native same-ID
recovery failed with authenticated GET404: Cubelet kept critical registration
metadata on a private tmpfs mount. The original current disk and metadata escrow
were retained. Independent current-disk capture, isolated ext4 replay/export,
and a new owned replacement recovered the latest app/home files and committed
PostgreSQL row without another commit. See
`../../recovery/results/2026-09-25/current-disk-replacement.json`.
This is a replacement recovery pass, not native recovery or production acceptance.
