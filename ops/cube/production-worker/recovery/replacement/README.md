# Owned replacement verifier

This command is a separate recovery result, never a same-ID crash-recovery pass.
It creates one new synthetic node-postgres provider only after the original
crashed ID is confirmed unavailable and current-disk conversion is hash-bound to
the old fixture's independently acknowledged **latest** nonce. It performs no
production binding change, power action, source deletion or new SQL/file commit.
Both old and new providers remain for coordinator review.

Build in a copied control-plane tree using Go1.22. `extract_helpers.go` selects
only the reviewed transport/ownership/escrow helpers from
`acceptance/crash/main.go` through Go's AST. It does not copy original prepare,
install, commit, cleanup, run or main functions. Preserve the helper source hash
alongside the executable:

```sh
mkdir control-plane/cmd/operator-replacement
cp replacement_main.go replacement_test.go control-plane/cmd/operator-replacement/
cd control-plane
go run /PATH/extract_helpers.go -source /PATH/acceptance/crash/main.go \
  -out cmd/operator-replacement/crash_helpers.go
gofmt -w cmd/operator-replacement/*.go
go test -race ./cmd/operator-replacement -count=1
go build -o replacement-verifier ./cmd/operator-replacement
```

Use a new root0700 `/opt/baarcha-bench/cube-replacement-JOB` stage and root0600
handoff.json, with expiry at most30minutes ahead:

```json
{"purpose":"DISPOSABLE_CURRENT_DISK_REPLACEMENT","fixture":"OLD-FIXTURE","old_provider_id":"OLD-ID","worker_machine_id":"REVIEWED-ETC-MACHINE-ID","current_archive_sha256":"RESCUE-HOME-TAR-SHA256","native_recovery_failed":true,"expires_at":0}
```

Release the previous coordinator's outer/worker operator locks before invocation;
the new command holds both. The only existing guest must be the exact old ID.
The worker must match the pinned SSH host, hostname, `/etc/machine-id`, data UUID
and changed boot ID from old escrow. The original crash stage must retain its
private escrow, reviewed probe.mjs and report with latest acknowledgement; a
successful native result is rejected. Conversion must have passed canonical Go
validators and preserve the old PID record. Archive hashes, latest marker hash,
actual app/home marker JSON and old probe capability/code are checked before any
Create call.

```sh
./replacement-verifier /opt/baarcha-bench/cube-crash-ORIGINAL \
  /PRIVATE/CONVERTED-DIRECTORY /opt/baarcha-bench/cube-replacement-JOB
```

Creation uses the same reviewed2CPU/2GiB node-postgres template, fresh supervisor
and traffic identity, deny-all egress, and pause rather than kill on timeout.
Creation intent and new ID are journaled before proceeding; failures never retry
Create or delete the old source. The new PostgreSQL/app is quiesced and confirmed
inactive before any import. A derived home ZIP omits only the exact regular
`.baarcha-postgres/data/postmaster.pid`, requiring a bounded positive PID, the
exact old PGDATA path, ready state and start time before the old acknowledgement.
Its content hash, original ZIP hash and derived ZIP hash are recorded. All WAL,
control and user data remain unchanged; the original archive is retained.

Home and app import use canonical runtime endpoints while quiesced. Supervisor
reexec is observed, quiescence reconfirmed, then resume starts PostgreSQL normally.
The verifier waits for real app/database health and the retained old probe,
then sends **only `/verify`** with the old capability/latest nonce. Pass requires
exact latest app file, home file and committed SQL row. Reports explicitly state
`native_same_id_recovery_passed:false`, `binding_changed:false`,
`replacement_latest_app_home_sql_preserved:true` only after verification.

This is still an owned synthetic acceptance tool. It does not implement a
production recovery API, task-history migration, capacity/binding transaction or
source-retention GC. Do not call the production migration complete from its pass.

## Explicit missing-provider branch (owned r4 power loss)

The verifier also supports an authenticated Cube GET404 paired with complete
Master inventory0/1node and a successful Cube-owned tasks query containing only
its header. Transport/500 errors, unavailable nodes, any other guest or any live
task fail. The existing unknown/stopped plus exact-one-old-provider branch remains.
Native recovery remains false in both branches.

For404 only, add `recovery_evidence_sha256` to the handoff, pinning exact bytes of
root0600 `recovery-evidence.json` in the new replacement stage. Copy these six
private evidence files into that stage (retain originals): `cubebox.json`,
`storage.json`, `plan.json`, `rescue-input.json`, `fence.json`, `export-report.json`.
The first three are the precrash metadata/validated plan; the next two are the
actual current-disk capture manifest/fence; the last is the actual rescue output.
The receipt schema is:

```json
{"purpose":"OWNED_CURRENT_DISK_MISSING_PROVIDER","old_provider_id":"OLD-ID","fixture":"OLD-FIXTURE","worker_machine_id":"MACHINE","previous_boot_id":"OLD-BOOT","current_boot_id":"CURRENT-BOOT","current_archive_sha256":"EXACT-HOME-TAR-SHA","files_sha256":{"cubebox.json":"SHA","storage.json":"SHA","plan.json":"SHA","rescue-input.json":"SHA","fence.json":"SHA","export-report.json":"SHA"},"no_task_verified":true,"no_owned_vmm_or_disk_fd_verified":true,"management_fenced":true,"checked_at":0,"expires_at":0}
```

The coordinator must independently establish the three assertions; they are not
inferred from404. checked_at must be within5minutes and expires_at within30minutes.
All raw evidence hashes, old IDs, boot/machine identities, plan→capture metadata
hashes, capture current.ext4→export disk hash, export→conversion archive hash and
latest markers/capability are checked before Create. The verifier repeats the
live empty task check immediately before creating its one replacement.

Build now copies `missing_evidence.go` and `missing_evidence_test.go` alongside the
replacement main/tests. The r2 outer-only build passed six Linux race tests,
including wrong/corrupt/missing evidence rejection. Artifact identity for that earlier build is recorded
in `../results/2026-09-25/replacement-missing-build.json`.

The r3 verifier passed the actual r4 current-disk replacement test. After an
abrupt worker loss, it recovered the latest acknowledged app/home files and SQL
row into a new owned sandbox, without issuing a new commit. The original source
and all archives remain retained. See `../results/2026-09-25/current-disk-replacement.json`.
Native same-ID recovery still failed and production bindings remain unchanged.

Include `testdata/` in the copied command source. The regression fixture is a
synthetic Python-generated ZIP with deflated empty directory entries; Go raw
ZIP Copy rejects those entries, so derivation recreates only their empty directory
headers while preserving modes and raw-copying regular entries. The test verifies
that only the exact stale PostgreSQL PID member is removed.
