# First full paired backup: concrete preparation

Prepared September25; **no full pair has been captured**. The canonical fixture
app was created as `01M3CZB4HXT2Y8HP8CEY75PCWY`; owner103 and foreign owner104
were preserved after the rejected preset request. Its task, SQL, history and
private screenshot proof must finish before the commands below become useful.
Keep global routing disabled. Root owns every live transition.

The current private input inventory is
`/opt/baarcha-bench/cube-paired-backup-review-20260925-03`:

- `owner-mounts-private.json`:67 Docker bindings,11 running at observation,
  three immutable source images; exactly one `/home/sandbox` bind each.
- `rollback-homes.nul`:67 validated canonical source directories, relative to
  `/`, NUL separated. All owner data, including `.git`, `.runtimed` task events,
  results/checkpoints, provider state and PostgreSQL files, is included.
- `library-root.nul`:the single library tree containing31 ready artifacts.
- `source-images.json`, `controller-inspect-private.json` and
  `postgres-containers-private.json`:exact recovery identities. The controller
  inspection contains secrets; keep it private and encrypt it with the roles.
- `myhometroc-stop-topology-private.json`:observed PID generations, executable,
  data directory and wrapper ancestry. These are observations, not signal targets
  that remain valid indefinitely. Refresh exact identities under the final lock.

These files are **preparation inputs**, not assertions that writers are stopped.
Refresh them after fixture activation and after any legitimate project addition.
Reject unexplained changed containers, homes, images or snapshots. Record both
original and final inventories rather than overwriting the original observation.

## Prepare immutable inputs before the outage

Use a new root0700 generation, mode0600 files, `umask 077`, the existing release
and operator locks, and the reviewed source hashes. Do not reuse historical
controller inspection after the pending allowlist recreation.

1. Save the three source image IDs plus the then-current controller image using
   `docker image save --output NEW/controller-and-docker-images.tar EXACT_IDS`.
   Put the inspect JSON for **every** retained Docker container into the same
   private recovery staging directory. Keep image digests and Docker/Compose
   versions. This is read-only to the source containers and can precede drain.
2. Stage a controller configuration directory with the real Compose inputs
   `/opt/sandboxd/src/docker-compose.yml`, the current environment file,
   `/opt/sandboxd/deploy-state/active-images.json`, active Cube override/relay
   definitions and `/opt/baarcha/landing.env`. Include the actual deployed
   migrations, `/var/lib/sandboxd/agent-auth`, exact image export and inspect
   metadata. Resolve Compose input paths from the actual container labels;
   missing declared inputs block the archive. Do not print environment values.
3. Stage worker launch inputs: `/etc/systemd/system/baarcha-cube-worker-01.service`
   and every actual drop-in, `/etc/baarcha-cube`, the installed lifecycle helper,
   stop/start binaries and migrations, `/opt/baarcha-cube/worker-01/seed.img`,
   `operator-key`, `known_hosts`, actual QEMU argv/hash and host package versions.
   Copy the reviewed startup/hold/observer configuration and its fixed file
   dependencies. A QEMU executable hash alone is not an OS recovery image.
4. The paired root disk already includes nested configurations, credentials,
   persistent metadata and template definitions. The separate worker-config
   role must record their exact pinned paths/hashes and the installed candidate
   manifests, filesystem UUIDs and approved template/resource mappings. Do not
   substitute a newly generated worker identity during restoration.

Archive each completed staging tree with GNU tar preserving numeric owners,
ACLs/xattrs, symlinks and modes; no dereference. Use separate `controller-config`
(including images), `worker-launch` and `worker-config` regular files. Their
private contents are subsequently encrypted. This does not transfer provider
credentials to other owners or to public application archives.

## Shortest consistent role closure

After the actual scoped Caddy/direct-writer fence, task/thumbnail drain and
controller shutdown, retain its exclusive maintenance lock. Check SQLite has
zero active tasks and no ambiguous admissions/recovery. Background customer
processes still write even though the controller is stopped.

Record each exact Docker full ID, restart policy and original running state.
Request `docker stop --time=-1 EXACT_FULL_ID` only for the reviewed originally
running Docker bindings. This does not force-kill on a Docker timeout. Independently
verify every mapped container is stopped and every source mount still matches.
A command timeout is a failure requiring inspection, not permission to kill.
Keep initially stopped containers stopped during recovery.

**MyHomeTroc needs a specific PostgreSQL check.** Read-only inspection found
`postgres → node wrapper → runtimed → tini`. The wrapper forwards SIGTERM/SIGINT
as PostgreSQL SIGINT (fast shutdown). `pg_ctl` is present next to that exact
postgres binary; `pg_controldata` is not. Runtimed, however, escalates a supervised
process group after about5seconds. Consequently Docker's unlimited timeout does
not by itself establish clean PostgreSQL shutdown. Calling `pg_ctl stop` while
runtimed is supervising also permits a restart; do not claim that race is fenced.
No dedicated per-worker stop endpoint is currently exposed. The Cube private
quiesce endpoint uses SIGKILL and is not the Docker shutdown solution.

The tool prerequisite now passed in a disposable container: official
`docker.io/library/postgres@sha256:9e73daeb439141c2b11eea2463f5f1a3b269fd90d897b41cddb7cb440f21aa5d`,
linux/amd64, PostgreSQL18.6,157,265,161image bytes. The publisher's
[official image definition](https://raw.githubusercontent.com/docker-library/official-images/master/library/postgres)
records the18-bookworm image family. The resolved manifest digest is pinned;
subsequent commands do not resolve the mutable tag. See
[actual isolated tool result](postgres18-tools-result-20260925.json).
One CPU/256MiB, networknone, read-only root, no capabilities and zero customer
mounts were used; the tool's shared libraries resolved and the container was
independently absent afterwards. The source binary is owner-writable UID1000
code and was not executed as hostroot merely to obtain its version. Its data's
`PG_VERSION` independently records18.

The exact deferred probe argv, full source container identity, canonical data
path/inode and preconditions are private in
`review-03/postgres18-control-probe.PREPARED.json` (same full directory above).
It uses the immutable image as UID1000 with only that stopped PG data directory
mounted read-only at `/pgdata`, no network,1CPU/256MiB and no default database
entrypoint. Require exit0, no control-file CRC warning and cluster state exactly
`shut down`. It has **not** probed the customer database. Do not execute before
the actual reviewed stop and maintenance-lock checks.

After the actual stop require natural removal of `postmaster.pid` and the exact
observed PostgreSQL socket/lock, and verify the control state using a compatible
trusted PostgreSQL tool in an isolated read-only mount. Absence of a socket alone
is not proof. If clean shutdown is not proven, preserve **all** data and WAL and
require an isolated writable-copy WAL recovery and application SQL check before
accepting the generation. Never delete the socket to make migration pass.
Remaining socket/special-file paths require explicit classification: do not
blindly exclude dotfiles, the entire socket parent, caches or unknown owner data.

Once all data writers are stopped, these exact archive commands use the reviewed
NUL lists (set `INPUT` and a new private `ROLES` directory first):

```sh
tar --create --file="$ROLES/rollback-homes.tar" --directory=/ \
  --numeric-owner --acls --xattrs --sparse --null --verbatim-files-from \
  --files-from="$INPUT/rollback-homes.nul"
tar --create --file="$ROLES/library" --directory=/ \
  --numeric-owner --acls --xattrs --sparse --null --verbatim-files-from \
  --files-from="$INPUT/library-root.nul"
```

Capture stderr privately and require exit0; inspect warnings, especially socket
omissions, before accepting either file. Add the frozen home/container mapping,
image recovery references, migration/recovery archive paths referenced by SQLite
and task-history metadata to an outer `rollback` tar alongside
`rollback-homes.tar`. The current inventory has no migration archives; recheck
actual journal references instead of assuming that remains true. Full owner
homes are an encrypted operator recovery artifact, not the public/private
migration transport's selective home manifest.

For platform PostgreSQL run the installed `/usr/bin/pg_dump`17.11 in custom
format using the existing `DATABASE_URL`, with successful exit and a new private
output, then `/usr/bin/pg_restore --list` into private evidence. Pass parsed
connection parameters through `PGHOST/PGPORT/PGDATABASE/PGUSER/PGPASSWORD`, not a
URL printed in argv/logs. Use `PGCONNECT_TIMEOUT=10`, a bounded operator timeout,
and verify server/client compatibility first. An actual isolated PostgreSQL
restore and checks of fixture owner/app/balance/session/provenance rows are
required; `--list` only verifies readability. The dump is transaction-consistent;
unrelated inference/billing may continue, so do not claim a simultaneous global
snapshot of every platform transaction. Project mutation paths remain fenced.

Close and hash the roles before asking the worker supervisor to stop. This keeps
24GiB home and4.1GiB library preparation outside the fresh pause-proof window.
Root may restart exact Docker containers after their role checkpoint, but must
record that earlier Docker recovery point and prove no canonical/config changes;
otherwise keep them stopped until the common restore point is closed. No restart
is performed by this document or the capture tool.

## Cold pair, original restart, then off-host proof

Construct `capture.json` with the exact schema in `cold_pair.py`:root disk
`/opt/baarcha-cube/worker-01/root.qcow2`, data disk
`/mnt/nvme/baarcha-cube/worker-01/data.qcow2`, database
`/var/lib/sandboxd/state/sandboxd.db`, actual unit/backup lock, and all eight
completed role files. `controller-key` is `/var/lib/sandboxd/secrets.key`;
`pause-receipt` must be the **new actual coordinator pause proof**, not an old
empty receipt or the enclosing clean-stop receipt. It must match the fixture's
binding and be no older than10minutes when capture starts.

At19:08UTC RAIDfree was1,553,528,197,120bytes; NVMe free416,053,002,240bytes.
The capture check reserves568GiB (120+448virtual) **plus roles**. Space must be
rechecked before capture, seal, readback, plaintext and restore; thin virtual
capacity is not independently backed space. Leave original disks untouched.

Run capture/compare/hash, then immediately boot/reconcile the original using real
receipts and restore its previous controller/Docker/traffic state. **Do not keep
it offline for encryption or upload.** Seal, multipart upload, full GET hash,
Mac GPG streaming and strict offline restore follow the commands in
[PAIRED-RESTORE-SEQUENCE.md](PAIRED-RESTORE-SEQUENCE.md). These helpers are already
installed and the real129MiB synthetic transport proof passed; the full worker
has not yet passed them.

The40GiB original and40GiB clone cannot coexist on the62GiB host. Once the
immutable restored pair is verified, schedule a second bounded original-offline
window to boot separate clone copies and validate the actual canonical fixture's
app/home markers, completed task event/result/checkpoint history and latest
acknowledged PostgreSQL rows. Restore the original afterwards. Do not start the
copied production controller against real Docker resources or production routes.
Only actual successful observations may become monitor copy/restore receipts.
