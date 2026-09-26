# Baarcha Cube controller

Replacement application controller for an entirely migrated Cube fleet. This is
a separate entrypoint and HTTP route table, reusing the reviewed project, task,
secrets, snapshot, tenant-authentication and Cube transport packages. It does
not launch or depend on the sandboxd process.

It has no Docker client, Docker CLI, socket mount, Docker reconciler/reaper,
workspace provisioner, legacy execution API, self-upgrader or telemetry sender.
Cube lifecycle maintenance handles leases, idle pause and task recovery.
The credential-injecting model proxy binds only an ephemeral loopback port;
guests reach it through the existing task-scoped Cube capability relay.

The first release deliberately retains existing SQLite schemas, IDs, encrypted
keys, stored ownership and configuration variable names. Renaming a database,
JWT issuer or public preview path during provider migration would break existing
clients without removing a runtime dependency. The frontend can keep its current
HTTP contract while its configured upstream becomes this service.

## Startup contract

- Existing regular database and encryption-key files; no implicit new database
  or generated replacement key. Every bound runtime credential must decrypt.
- Global Cube configuration with all reviewed preset templates, reverse egress,
  service-token authentication and preview signing keys. Allowlist mode is refused.
- Every existing sandbox must have a Cube binding, owning app, durable Cube app
  selection and matching task owner. The controller never edits a Docker row to
  pretend that its data was migrated.
- No incomplete offline migration or recovery. Admission remains 2 CPU/2 GiB,
  at most four active guests, with the existing root-owned storage observation.
- An exclusive lifetime lock on the canonical database maintenance lock excludes
  both another controller and the old sandboxd daemon's shared lock. The existing
  lock marker must be present. Stop this controller for offline maintenance.
- `/readyz` checks fleet bindings, fresh pinned worker/storage observation and an
  authenticated read-only Cube inventory. `/healthz` remains process liveness.

Configuration is the existing reviewed `SANDBOXD_CUBE_*`, auth, data and model
upstream environment. New options: `CUBE_CONTROLLER_ADDR` (native default
`127.0.0.1:9090`) and `CUBE_RETAINED_HISTORY_ROOT` (default existing workspaces).
Mount retained histories read-only. Mount the exact existing state, key, library,
agent-auth and observer paths; the Motion UDS directory is read-only when enabled.
Never mount `/var/run/docker.sock`. The container image defaults to port 9000.

## Cutover is not yet accepted

This candidate is not a fleet migration or a production release. Existing Docker
projects must first be transferred with the journaled migration tools, accepted
and left stopped with their rollback artifacts. Then stop the old daemon and its
restart policy, change platform/proxy/relay upstreams, start this controller,
and verify owner/private/public previews, editor tasks, history, files, config,
publish/remix and new-project creation. Confirm the old daemon remains stopped.

Worker boot/shutdown coordinators currently pin the old Compose service and
controller identities. Update and test that operational contract before deploying
the replacement. Full paired off-host restoration, planned host lifecycle,
Motion compatibility and a successful real production coding task are still
release gates. Do not infer acceptance from unit tests.

The route table supports Baarcha's current project/task client. Legacy standalone
creation/exec/purge/upgrade routes are absent. Git and terminal operations that
were not ported to Cube remain unavailable; they must not fall back to host
workspaces. Pre-migration task events use a separate read-only archive reader.
