# Releasing code with an existing Cube allowlist

Ordinary `ops/deploy-project-x.sh COMMIT` still refuses an enabled Cube allowlist.
An operator may set `PROJECT_X_CUBE_ALLOWLIST_RELEASE_FILE` to a separately
reviewed private JSON file to release code while retaining the exact current
allowlist. This does not enable global routing, add an app, migrate a guest, or
change the existing Cube transport/resource configuration.

The review file must be a canonical root-owned regular0600 file, single link,
inside a root-owned0700 directory on the production host. No symlink ancestors,
unknown fields, duplicate JSON keys, or files larger than16KiB are accepted.
The exact schema is:

```json
{
  "version": 1,
  "candidate_sha": "FULL_40_CHARACTER_REVIEWED_COMMIT",
  "controller_id": "CURRENT_FULL_64_CHARACTER_DOCKER_ID",
  "app_ids": ["EXACT_CURRENT_ALLOWED_APP_ID"],
  "runtime_override_sha256": "SHA256_OF_RUNTIME_COMPOSE_FILE_BYTES",
  "rendered_config_sha256": "SHA256_OF_CANONICAL_RENDERED_COMPOSE_JSON"
}
```

`app_ids` must equal the sorted current `SANDBOXD_CUBE_APP_IDS` set, without
empty/duplicate/noncanonical IDs. The override is
`/opt/sandboxd/deploy-state/runtime-compose.json`. Obtain rendered JSON using the
same project, `.env`, base Compose, durable runtime override and active-image
override used by the script. Canonical JSON means Python
`json.dumps(value,sort_keys=True,separators=(',',':')).encode()`; array order is
preserved. Rendered output contains secrets and must remain private. The review
file itself contains only IDs/hashes.

Root reviews the exact candidate commit, existing app scope and configuration,
then invokes the installed reviewed script:

```sh
PROJECT_X_CUBE_ALLOWLIST_RELEASE_FILE=/ROOT_PRIVATE_REVIEW/allowlist-release.json \
  /opt/sandboxd/deploy.sh FULL_40_CHARACTER_REVIEWED_COMMIT
```

The existing runtime deployment lock covers this process. Review validation runs
before image builds and again after acceptance builds, before source/env changes
or controller stop. The review's exact bytes are retained privately with the
release. A changed file, override, rendered configuration, candidate SHA or old
controller identity refuses the release. Once the controller is recreated, the
old review cannot authorize another deployment. A failed pre-activation build
may reuse the same review only while all pinned inputs still match.

All existing image acceptance, schema/migration equality, worker boot/config
pinning, storage-guard preservation, namespace-relay checks, authenticated API
readiness and rollback checks remain mandatory. Rollback restores the prior
controller image/configuration and refreshes its coordinator identity; it does
not rewind SQLite or tenant data. The operator option is rejected for disabled
Cube and for global mode; it is not a generic deployment bypass.

Validation uses real Git worktrees/SQLite/flocks and fake Docker/provider
operations. The new cases cover an exact allowed release, missing review,
wrong candidate/controller/app scope/override/rendered hash, unsafe mode,
symlink/duplicate keys, changed review during build and stale review after
controller recreation. They create no real guests and prove no live release.
