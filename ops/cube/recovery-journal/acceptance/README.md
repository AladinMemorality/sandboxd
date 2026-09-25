# Owned current-disk recovery journal acceptance

**Owned live acceptance passed on 2026-09-25. Every new execution still requires a separate operator handoff.** This
coordinator creates one synthetic 2-CPU/2-GiB node-postgres guest and uses an
isolated SQLite database and encryption key. It never opens the production
controller database, changes production bindings, powers a VM off, deletes the
old crash source, or writes a new app/home/SQL marker.

The original r4 fixture had no task history. A separately generated terminal
history entry tests canonical history transport and controller preservation;
its checkpoint and prompt explicitly identify it as synthetic. Passing this
fixture must not be described as pre-crash task-history survival.

## Scope and prerequisites

- Original private crash stage, converted current-disk `app.zip`, `home.zip`,
  manifest and conversion report, and retained merged `home.tar` must exist.
- Original latest acknowledgement, app/home marker JSON, reviewed read-only
  probe code and capability must agree. Original native recovery must have failed.
- The old provider must return authenticated404, Master inventory must be zero
  with one node scanned, and Cube task inventory must be empty. The earlier r3
  replacement must first be removed by its owner; this command does not do that.
- The pinned worker hostname, SSH host identity, machine ID, data UUID and changed
  boot ID must match the original escrow. Both operator fixture flocks are held.
- Fresh root0600 missing-provider evidence must establish no old execution,
  drained requests and management fencing. Those facts are not inferred from404.
- Exact API/proxy origins are loopback20300/20080. The fixture attaches the
  authenticated reverse channel with a deny-all outbound policy. No production
  network acceptance flag is changed.

## Build without running guests

Use a complete isolated control-plane snapshot. `prepare.sh` copies the new
coordinator and tests, then uses Go AST allowlists to reuse reviewed crash
transport and replacement archive helpers. It excludes their execution, create,
commit, power and cleanup entrypoints. It also copies `missing_evidence.go`.

```sh
GO=/path/to/go SNAPSHOT=/private/build/control-plane ./prepare.sh
cd /private/build/control-plane
go test -race -count=1 ./cmd/operator-journal-acceptance
go build -o /private/build/journal-acceptance ./cmd/operator-journal-acceptance
```

Keep `migrations/` from this snapshot and invoke from its
`cmd/operator-journal-acceptance` directory, so `../../migrations` is exact.
Record source/helper/binary hashes in a build manifest. Do not run the fixture
inside a PID namespace: recovery.Open checks native `/proc` database writers.

## Operator input (private; never commit credentials)

Create a new root0700 `/opt/baarcha-bench/cube-journal-UNIQUE` directory. Supply
root0600 `handoff.json` with expiry no more than20minutes ahead:

```json
{"purpose":"DISPOSABLE_CURRENT_DISK_JOURNAL","fixture":"ORIGINAL_32_HEX","old_provider_id":"EXACT_OLD_ID","worker_machine_id":"EXACT_MACHINE_ID","current_archive_sha256":"MERGED_HOME_TAR_SHA256","recovery_evidence_sha256":"FRESH_RECEIPT_SHA256","expires_at":0}
```

Supply `native-archive.json` containing only
`{"retained_home_tar_path":"/absolute/private/retained/home.tar","retained_current_disk_path":"/absolute/private/retained/current.ext4"}`. The regular,
root0600 merged archive must match the handoff digest, and the retained native current disk must match the hash and size in the pinned rescue-input manifest. Both are journaled separately; no extraction occurs on the host.
Copy the six retained private metadata/capture/export files and a **fresh**
`recovery-evidence.json` using the existing replacement verifier receipt contract
(see `production-worker/recovery/replacement/README.md`). Its underlying source
metadata, disk and archive hashes must bind the original r4 identity.

Only after explicit handoff, from the prepared command directory:

```sh
/private/build/journal-acceptance run \
 /opt/baarcha-bench/cube-crash-ORIGINAL \
 /opt/baarcha-bench/CONVERTED-CURRENT-DISK \
 /opt/baarcha-bench/cube-journal-UNIQUE
```

The executable has a20-minute context. The external coordinator must bound it
with2CPU/2GiB and retain the private journal on failure. No automatic rerun occurs
if an intent/database already exists. Unknown Create outcomes require journal
inspection and exact adoption, never blind recreation or broad deletion.

## Assertions

1. Seed one synthetic owner/app/stable sandbox/config row and terminal task in a
   fresh SQLite database; retain a separate closed-controller backup and key.
2. Exercise real `recovery.Open → Begin → Fence → Create → TargetClient` against
   the owned target. Attempting Commit before verification must fail.
3. Quiesce the new guest. Import validated home/workspace archives. Only the exact
   old PostgreSQL PID file is omitted in a separate derived home archive; original
   data/WAL and all source archives remain retained.
4. Observe supervisor reexec, reattach, quiesce, import canonical synthetic task
   history, and compare real private app/home export digests and history digest.
5. Apply the frozen synthetic app configuration, check acknowledged revision,
   resume, wait for PostgreSQL app health, and send only the old probe `/verify`.
   Require exact original latest app/home files and committed SQL nonce.
6. Exercise `Verify → Commit`, including idempotent Commit. Reopen the isolated
   database and require stable owner/app/sandbox/config/task and domain identities,
   a changed provider binding, completed journal and retained old-ID quarantine.
7. Recheck exact target journal metadata, delete only that target, independently
   confirm404 and zero provider inventory. Keep isolated journal and archives.

Stable URL identity here means preserved app/sandbox/domain identifiers. This
fixture does not serve the platform UI or exercise its HTTP router. Private task
history is synthetic; canonical runtime config/credential files from the crashed
`.runtimed` are intentionally not copied.

## Actual result

The reviewed binary passed on 2026-09-25 at11:52:56.565972365UTC using the retained
r4 crash-disk archives. The real `Open → Begin → Fence → Create → TargetClient →
Verify → Commit` flow preserved the independently acknowledged latest app/home
files and SQL row, matched quiesced app/home export digests, and round-tripped
the explicitly synthetic task history. Reopening the isolated database verified
atomic provider rebinding with stable owner/app/sandbox/config/task/domain IDs.
The exact owned replacement was deleted and independently returned404; its
admission charge and complete provider inventory were both zero afterward.

See [sanitized live evidence](results/live-journal-2026-09-25.json) and
[build/unit provenance](results/build-only-2026-09-25.json). The build-only record
retains its historical `live_executed:false`; the later live report records the
actual execution. Both pin the same executable and coordinator source hashes.

This does not turn native same-ID recovery into a pass, prove lost pre-crash task
history, exercise the platform HTTP router, or restore a production controller
backup. The controller database/key and task row in this test were synthetic.
Production bindings remained unchanged; original crash disk and archives remain
retained. No new app/home/SQL commit was issued during verification.
