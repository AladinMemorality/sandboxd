# Independent migration recovery fixture — 2026-09-24

Migration archives are private data-transfer artifacts, not a complete disaster
recovery backup. In particular, provider credentials and retained supervisor
identity deliberately remain outside the guest transfer.

## Operator backup set

Before any production cutover, freeze admission and drain active tasks, stop
the control plane under the maintenance fence, and quiesce each source before
its filesystem backup. Keep an independently stored, access-controlled backup
set containing:

- A consistent SQLite snapshot containing project/owner/sandbox identities,
  runtime bindings, config, task records and the migration journal. Use SQLite
  backup or `VACUUM INTO`; copying the main database without its active WAL is
  not sufficient. Verify `PRAGMA integrity_check` on the restored copy.
- The exact encryption key used by that database, from its configured keyfile
  or separately backed-up secret source. A newly generated key cannot decrypt
  existing app config, provider records or Cube bindings.
- Every source owner home, including private app files and `.git`, owner data,
  selected tools/config, retained provider directories and `.runtimed` task
  history/control identity. This is an operator-only backup; retained provider
  and control files must not be imported into Cube using a blanket home copy.
- Published library/snapshot storage, including its frozen source artifacts,
  metadata database references, and retained legacy image/volume artifacts
  required by the chosen recovery path. Preserve original private permissions.
- All migration recovery archives, the exact canonical home manifests and
  checksums recorded in the journal, and any filesystem storage-adapter state.
- Deployment configuration and exact image/template references, encryption
  secret references, workspace/library/archive root paths, and the retained
  Docker container/image identities. Secure credentials separately; do not
  place their values in an evidence report or commit them to source control.

Record checksums and test recovery into a separate environment. Retain the
original backup set through acceptance; do not overwrite it with a partial
rollback result. A post-cutover rollback must first export and verify current
Cube writes. Restoring an old pre-cutover database or owner-home snapshot alone
would discard those writes and can restore stale provider identities.

## Executed fixture

`TestIndependentBackupRestoreResumesRollbackWithoutOriginalFiles` uses real
SQLite and the production archive readers, private installers, ownership
normalization and provider-commit transaction. Only hypervisor lifecycle is
stubbed. Its Docker inspector accepts a single fixed stopped fixture identity;
it never contacts a Docker daemon or a tenant container.

The fixture reaches a durable `rollback_archived` boundary with synthetic new
Cube workspace/home writes and a newly completed task. It creates a consistent
SQLite snapshot and independent copied files for the encryption key, retained
source home and migration archives, then deletes the original database, WAL,
key, source/target homes and original archives. It restores solely from those
copies, checks database integrity and decrypted binding identity, and resumes
rollback. The final data includes new workspace/home writes and task history,
while retained provider and runtime identity remain the source's originals.
Project/sandbox IDs, owner and private visibility are preserved. A corrupted
rollback archive is rejected before changing the restored source; restoring
the correct archive allows an idempotent retry to complete.

The migration package passed under Go 1.22 with the race detector, UID 0,
network disabled, two CPUs and 2 GiB RAM in an isolated test container. No
production project, deployment, credential or visibility was changed.

## Limits

The independent copies are separate temporary directories/inodes, not a test
of a physical off-host backup service or host/disk loss. The fixture contains
synthetic task artifacts rather than an actual model task. It does not restore
library snapshots, boot an independent control-plane deployment or recreate a
real Docker/Cube hypervisor identity. Those operator and live-lifecycle checks
remain distinct acceptance gates; this test must not be presented as a real
production disaster-recovery rehearsal.

## Migration readiness review

A review also found that `PreviewNone` alone cannot establish worker health.
The migration readiness predicate now rejects empty worker-only process lists,
stopped/missing-PID workers, and a healthy web process with failed declared
workers. A two-second observation window requires unchanged process identities
and restart counters, preventing a brief running sample in a crash loop from
passing acceptance. This window is limited to migration and retained-source
retirement; it is not added to ordinary app resume. Legacy retained Docker
supervisors that omit process details keep their existing web-preview readiness
compatibility. The API already exposes worker process state separately and
clears the worker-only preview URL.

The focused race run covers the independent backup test, all worker-health
cases and crash-loop/stable-recovery sequences. Its exact log is retained at
`/opt/baarcha-bench/cube-global-20260924/backup-review/final-focused-race.log`.
