# Optional PostgreSQL in a project sandbox

PostgreSQL is an explicit persistence option. The `node-postgres` starter creates
a frontend, Express API and private PostgreSQL worker when an owner requests that
kind of application. Existing presets, including the default React Pro starter,
do not start a database, initialize database files or reserve database memory.
Static sites and applications using another storage service keep their normal
runtime. Do not infer a database requirement merely from a framework or package.

The reviewed image includes a pinned worker at `/opt/services/postgres/worker.mjs`.
Installing those files does not start a service. The worker uses PostgreSQL 18.4
from `embedded-postgres@18.4.0-beta.17`; its client is `postgres@3.4.7`.
Only the project's ordinary `sandbox.yaml` workers list opts in:

```yaml
version: 1
web:
  command: node server.mjs
  port: 3000
  health_path: /health
  restart_after_task: true
workers:
  - name: postgres
    command: node /opt/services/postgres/worker.mjs
    restart_after_task: false
build:
  command: ""
```

Keep the existing web command and other workers when adding this recipe to an
owner-requested database application. This uses the established manifest schema;
there is no inferred or automatically enabled services section. An image must
actually contain the reviewed worker before advertising this capability.
Docker rollback or container recreation for a database-enabled project must also
use an image containing `/opt/services/postgres`; the older production base does
not provide this optional capability. Preserve that image reference in backups
and migration plans rather than silently falling back to an incompatible base.

Server code can use:

```js
import {createDatabase, waitForDatabase} from '/opt/services/postgres/connection.mjs';
const sql = createDatabase();
await waitForDatabase(sql);
await sql`create table if not exists notes (body text not null)`;
await sql`insert into notes (body) values (${userSuppliedText})`;
```

The default database is `postgres`, owned by the unprivileged guest account.
Use parameterized SQL. Apply schema changes intentionally and preserve existing
records; `initdb`, dropping the database, or deleting its files is not a repair
step. Applications still implement their own end-user authorization.

## Storage and lifecycle

- All cluster files, including WAL, live under `$HOME/.baarcha-postgres/data`.
  This path is outside `workspace/app` and source publication.
- The Unix socket lives under `/tmp/baarcha-pg-<uid>-<home-hash>`, outside owner
  data backups. Its directory must be owned by the guest UID, be a real directory
  rather than a symlink, and have mode 0700. No database TCP listener is opened.
- Code edits restart the web process but leave the database worker running.
  Cube pause/resume retains its process state. Supervisor reexec or Docker
  recreation stops/restarts the worker using the existing private cluster.
- New projects and source remixes have distinct homes and initialize an empty
  database. Owner source restore must preserve the existing private home; deleting
  and replacing that guest would discard its database and is not an acceptable
  restore implementation.
- Existing `PG_VERSION` must be major 18. A different major, invalid version file
  or unrecognized nonempty data directory causes startup to fail without changing
  it. Upgrades require a separate reviewed data migration, never implicit init.

The worker intentionally accepts no public connection URL or external database
host. The connection module is server-only; never bundle it or its configuration
into frontend code. Same-guest Unix-socket access needs no outbound firewall
exception. Sandbox isolation remains a separate worker/platform requirement.

## Backup, migration and recovery

Physical backup must be a **cold, complete cluster copy**. Quiesce the project's
writers and PostgreSQL worker using the platform's established owner-workspace
quiescence, or stop the entire sandbox. Wait for the PostgreSQL process to exit.
Only then export the full private owner home with `.baarcha-postgres` preserved,
including `data/pg_wal` and every other cluster file. A running-file copy or only
the `base/` directory is not a backup. Do not restart writers while copying.

For reviewed-home-v2 migration add an explicit entry alongside the rest of that
owner's complete reviewed manifest:

```json
{"path":".baarcha-postgres","disposition":"preserve"}
```

This is an entry, not a complete home manifest. Workspace/task history and
retained provider identity keep their existing classifications. Never classify
the whole home from this example, and do not put `/tmp` sockets into persistent
data. Keep PostgreSQL major version, guest UID/account and image identity stable.

Restore only while the destination database and all writers are stopped, using
the platform's verified private-home import. Preserve ownership/mode and verify
the archive digest before starting the worker. Test a real SQL read/write after
recovery; WAL recovery must preserve committed transactions after an unclean
stop. Keep a separate original backup until verification completes. Source
publication, screenshots and application code checkpoints do not back up private
database contents.

The embedded binary package does not include `pg_dump` or `pg_restore`. Generic
logical export/import is deliberately not advertised. A particular app's table
export is app-specific and cannot be represented as a schema-complete database
backup. Independently stored production backups and restore exercises remain
deployment acceptance requirements.
