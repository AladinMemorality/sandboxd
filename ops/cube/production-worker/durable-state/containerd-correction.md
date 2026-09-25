# Containerd metadata loader correction (0010)

The empty-worker readiness check after installing a61a43 failed before any guest
was created. Nine database roots used persistent XFS, but containerd metadata
still opened `/data/cubelet/state/io.containerd.metadata.v1.bolt/meta.db` in the
private tmpfs namespace. The declared TOML root_path was insufficient: the real
plugin's BoltConfig did not declare that field, and its InitFn unconditionally
selected PropertyStateDir. The server decoder warned about the unknown field,
then retried non-strict decoding and ignored it. The readiness check correctly
rejected this; its policy has not been relaxed.

Required patch0010 is separate from0006–0008 so the a61 build/evidence remain
unchanged. It adds the actual metadata plugin field and selects that exact root
when configured. Explicit roots must be canonical absolute paths and cannot
request asynchronous database writes. After initialization, directory ancestors
are synced before returning the database. Initialization errors close the opened
DB. Legacy unconfigured paths retain their prior selection behavior.

The backup plugin previously hardcoded the old State path. It now obtains its
source from the actual open Bolt transaction's DB.Path, preserving the existing
backup locking/copy behavior. This does not turn the built-in six-hour local copy
into a complete off-host or power-consistent project backup policy.

Regression tests use the registered plugin InitFn and real server Config.Decode,
a real local content store and a real Bolt database. They assert actual database
path/options, commit a marker, close/reopen it and verify the marker without
creating volatile State. Unsafe explicit configuration is rejected before opening
a fallback DB. A registered backup test copies the actual database and verifies
the latest marker in the resulting Bolt file. Both principal tests fail against
untouched a61 plugin code for their intended path mismatch and pass on0010.
No filesystem mount, tenant guest, raw packet or service mutation is used.

## Other legacy consumers

Search of pinned Cubelet source and installed shell scripts found:

- `cmd/cubecli/commands/unsafe/restoredb.go` derives its target from config.State
  and derives backup location from the old default root. Do not invoke this
  legacy command on the durable layout: it is an unsafe copy operation without
  an independently verified offline/current-state restore transaction.
- `cmd/cubecli/commands/image/fix.go`, `network/ls.go` and
  `unsafe/volumedb.go` assume legacy locations for their respective image,
  network or volume databases. They are maintenance commands, not normal runtime
  plugin initialization. They remain unsupported on this durable deployment;
  do not use them to infer absence of data or repair state.
- Cubebox logs reference `io.containerd.runtime.v2.task/default`, which remains
  transient task state intentionally. It is not the moved containerd metadata DB.

Only the backup runtime plugin was found to open the old containerd metadata
path outside its owner plugin. The installed cubecli is unchanged by building
Cubelet. No automatic restore or maintenance operation is added by0010. An
operator needs reviewed offline restoration and exact current-config paths
before those legacy tools can be made safe for this layout.

## Correcting the empty held worker

The coordinator must retain initial independent cold root/data checkpoints and
the original current-disk escrow. No guests ran on a61. Before replacing it,
hold lifecycle/autostart, stop Cubelet gracefully and escrow BOTH remaining
private tmpfs metadata and the new persistent metadata after all writers stop.
Perform the reviewed clean OS reboot under the hold to remove the old namespace.
Do not delete any old DB to defeat the legacy-state guard.

Install only the new exact binary after verifying the held worker identity,
changed boot ID, zero writers/tasks, preserved config hash and all retained
artifacts. Preserve a61 separately. Use a temporary file in the binary directory,
fsync it, atomic rename and parent directory fsync. Do not run the original
root-absent enrollment installer: the persistent metadata root already exists
and its current files must remain untouched. No config change is needed for0010.

After explicit controlled start, rerun strict actual-FD/XFS/template/quota
readiness. Containerd must now open
`/data/cubelet/persistent-metadata/containerd/meta.db`. Only then run the owned
four-slot workload/fifth-refusal and file/SQL/pause/power acceptance. Build/unit
success does not replace those gates or prove native same-ID cold recovery.


## Built candidate and tests

Candidate `/root/cube-production/containerd-durable-native-candidate/cubelet-candidate`
SHA256 `de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b`.
The source is pinned v0.7.1 plus both network patches,0005,0006–0008 and0010;
optional Rust0009 remains excluded/unbuilt. Exact3 generated BPF objects/native
CGO were verified. All981 copied source files and82 protected input files stayed
unchanged. Existing installed a61 was unchanged by build.

New plugin tests pass with race detection. Both principal tests reproduce their
intended failures against a61. Full affected non-privileged suites and selected
storage recovery/pause suites pass. First broad run failed only upstream
Test_HashCode's1ms wall-clock assertion under bounded CPU/race instrumentation;
that exact test passed unchanged on an isolated retry, and the full affected
command then passed. Preserve both logs; no assertion was weakened. Full storage
integration coverage retains the earlier documented baseline/privileged limits.

Build sources/script/logs are retained in the separate r5 candidate and
`durable-state/results/2026-09-25/containerd-correction`. Readiness now pins this
exact new candidate and still rejects all unexpected volatile metadata DBs.
