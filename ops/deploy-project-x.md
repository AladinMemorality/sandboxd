# Project X controller deployment

Install the reviewed `ops/deploy-project-x.sh` outside the checkout as
`/opt/sandboxd/deploy.sh`. The existing restricted SSH deployment command can
then call it with a **full 40-character commit SHA**:

```sh
/opt/sandboxd/deploy.sh <full-reviewed-commit-sha>
```

The default checkout is `/opt/sandboxd/src`; private release evidence and the
deployment lock are under `/opt/sandboxd/deploy-state`. The host needs Git,
Docker Compose, Python 3 with SQLite, `flock` and GNU `timeout`. Tests can override
`PROJECT_X_SRC_DIR` and `PROJECT_X_DEPLOY_STATE`; production normally uses the
defaults. Tracked live edits cause a refusal. Untracked routing files, including
`traefik/dynamic/myhometroc.yml`, are retained.

The script preserves the running provider configuration. It refuses a mismatch
between the live controller's Cube settings and resolved Compose configuration,
so this release command cannot perform the initial Cube activation or migration.
Disabled Cube plus existing Cube ownership still fails closed.
It builds the exact detached revision into immutable base/controller tags and
checks the controller version, opt-in PostgreSQL state, Vite cold reload and
native PostgreSQL recovery before changing the live checkout. These disposable
checks have no network access, bounded resources and no tenant data mounts.
Previously completed image builds are reused only if their source-revision
label matches; existing release tags are never overwritten.

Before activation it saves the old checkout, environment, image IDs, dynamic
routing and a consistent SQLite **online backup**, including committed WAL
frames. Artifacts containing credentials are private. The `sandboxd-base:0.3.0`
compatibility tag advances only after acceptance, so existing sandbox rows can
use the new helper on later recreation. Both old image IDs remain retained.
The controller is updated with `--no-deps --no-build --pull never`; Traefik and
existing sibling sandbox containers are not restarted. An already active Cube
deployment also recreates its two management relays, then verifies they share
the **new** controller's network namespace, publish no ports and pass health
checks. It also reads Cube's sandbox listing from inside the controller namespace
and checks that the configured API key succeeds while an unauthenticated request
is denied. Credentials travel over stdin, with response bodies discarded. This
checks API access; the separate deployed workload smoke must verify proxy,
supervisor and application behavior. The same sequence runs when rolling back the controller. Host isolation must
succeed, the running controller image must match, and both authenticated
readiness and unauthenticated API denial must pass.

When `/etc/baarcha-cube/worker-stop.json` is installed, the release also checks
its private ownership, current controller and worker boot, absence of a stop
marker, and exact agreement between the database migrations and the configured
coordinator migration directory (including migration 34). After readiness it
atomically updates only `controller_id`; rollback performs the same update for
the restored controller. A concurrent configuration edit or incompatible schema
leaves the controller stopped for operator review instead of overwriting the
coordinator configuration. Original bytes and update receipts remain in the
private release directory. This does not create a pause receipt or authorize
shutdown. Cube releases require this configuration; Docker-only installations
without it remain supported. `PROJECT_X_WORKER_STOP_CONFIG` overrides its path
for isolated deployment tests.

On activation failure the candidate controller is stopped before restoring the
old source, environment and image references. The database and tenant files
are **never automatically restored**: this preserves writes accepted during the
release. This rollback requires reviewed additive schema compatibility; it is
not suitable for a future destructive migration without a different plan. If
the candidate cannot be stopped, or rollback readiness/isolation fails, the
script fails explicitly and retains its evidence for operator recovery. It
does not claim successful recovery or copy a database beneath a live process.

Successful deployment writes `succeeded` in its private release directory.
This does not migrate projects or enable Cube. Enabling Cube requires its
separate production acceptance procedure. Once accepted and activated, place
the complete private Cube configuration and management relay service definitions
in `/opt/sandboxd/deploy-state/runtime-compose.json`. This durable override is
loaded between the repository Compose file and `active-images.json`; do not put
secrets in source control. Active Cube requires the accepted global mode,
reverse broker and isolation configuration, plus immutable relay image digests.
The release changes only the controller image in the existing image override,
preserving other environment, volume and service settings. The source revision
still needs compatible Cube templates and migration schemas; these checks do
not attest network isolation or replace workload acceptance.

Contract tests use fake Docker/isolation commands with real Git worktrees,
SQLite WAL backup, loopback authorization checks and locking:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s ops -p test_deploy_project_x.py -v
```
