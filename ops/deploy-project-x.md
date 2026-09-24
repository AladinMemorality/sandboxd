# Project X Docker runtime deployment

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

The script refuses Cube/reverse-egress activation and existing Cube ownership.
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
Only `sandboxd` is updated with `--no-deps --no-build --pull never`; Traefik and
existing sibling sandbox containers are not restarted. Host isolation must
succeed, the running controller image must match, and both authenticated
readiness and unauthenticated API denial must pass.

On activation failure the candidate controller is stopped before restoring the
old source, environment and image references. The database and tenant files
are **never automatically restored**: this preserves writes accepted during the
release. This rollback requires reviewed additive schema compatibility; it is
not suitable for a future destructive migration without a different plan. If
the candidate cannot be stopped, or rollback readiness/isolation fails, the
script fails explicitly and retains its evidence for operator recovery. It
does not claim successful recovery or copy a database beneath a live process.

Successful deployment writes `succeeded` in its private release directory.
This deploys reviewed Docker behavior; it does not migrate projects or enable
Cube. Enabling Cube requires its separate production acceptance procedure.

Contract tests use fake Docker/isolation commands with real Git worktrees,
SQLite WAL backup, loopback authorization checks and locking:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s ops -p test_deploy_project_x.py -v
```
