# Cube migration branch checkpoint

2026-09-17 — branch `codex/cube-migration`, based on `d913a6f`.
The companion Baarcha branch has the same name, based on `632e253`.
This is an opt-in integration foundation. It is **not a usable coding-app pilot
or a production runtime replacement yet**. Nothing has been deployed.

## Implemented

- Pinned Cube v0.7.1 wire contracts for create/get/connect/pause/delete and
  snapshot operations, with bounded requests, explicit policy, redacted errors,
  redirect blocking, and rejection of Cube's reserved `host-mount` metadata.
- An authenticated optional HTTP listener alongside runtimed's existing Unix
  socket. Separate supervisor and private Cube ingress tokens, streaming events,
  cancellation, and removal of the supervisor token from child environments.
- Additive SQLite migration `0024`: stable sandbox IDs, explicit provider, and
  an atomic runtime binding whose credentials use the existing encryption key.
- Operator-selected apps and preset-to-template mapping; Docker remains default.
  Cube create verifies the fresh supervisor credential before marking running.
- Owner-checked lifecycle routes and fail-closed guards for unsupported APIs,
  body-based publish, collection/legacy creation paths, and host maintenance.

The low-level client supports snapshots, but the application publish/remix API
is intentionally blocked until templates can be sanitized. A live memory clone
can copy owner credentials and private data even when subsequent writes use
copy-on-write.

## Local checks

Run from the repository root with Docker available:

```sh
bash scripts/check-cube-migration.sh ./...
```

The runner uses Go 1.22 and disposable containers, with named module/build
caches. It mounts the full repository because API contract tests read
`docs/openapi.yaml`. It does not contact Cube or production services.

Checkpoint validation: the full `go test ./...` suite passed. The Cube client
also passed its 11 tests with the race detector.

The tests cover Cube wire contracts and errors, supervisor authentication and
stream cancellation, encrypted bindings, tenant checks, distinct lifecycle
operations, and rejection before Docker/host operations. They are mock/loopback
integration tests, not proof of deployed guest isolation.

## Operator configuration for the future isolated pilot

Cube is disabled by default. Configuration is server-side only:

| Variable | Meaning |
| --- | --- |
| `SANDBOXD_CUBE_ENABLED` | Explicit `true` to enable |
| `SANDBOXD_CUBE_API_URL` | Private CubeAPI origin |
| `SANDBOXD_CUBE_API_KEY` | Management credential, never sent to guest |
| `SANDBOXD_CUBE_PROXY_URL` | Trusted private CubeProxy origin |
| `SANDBOXD_CUBE_DOMAIN` | Trusted virtual-host routing suffix |
| `SANDBOXD_CUBE_TEMPLATES` | JSON map of supported presets to reviewed templates |
| `SANDBOXD_CUBE_APP_IDS` | Comma-separated internal app allowlist |

Do not enable this branch against user projects yet. Creation requests private
ingress, no internet access, deny-all outbound policy, pause on timeout and no
automatic resume. IPv6 denial must be proven or disabled at the worker boundary:
upstream network-policy code inspected so far does not establish that guarantee.
Management endpoints must remain unreachable from guests.

Templates require the new runtimed binary and a fresh-token bootstrap. Updating
Cube's envVars does not rewrite the environment or authentication token of an
already-running restored process. The create readiness check fails closed if
that bootstrap contract is not met. No live token may be baked into a template.

## Work required before an app pilot

1. Build and exercise a trusted template bootstrap with fresh per-sandbox
   credentials, private management ingress and verified IPv4/IPv6 isolation.
2. Preserve Baarcha preview URLs, ownership, WebSockets/HMR and authenticated
   wake routing. GET may currently show legacy preview information; raw wake is
   blocked for Cube and no usable preview route is promised.
3. Port scoped file/export/process-log operations and config application. Keep
   path traversal and symlink protections inside the guest workspace boundary.
4. Enable coding tasks only after model/bridge egress, metering, live input,
   cancellation and task-result recovery work through the guest channel.
5. Integrate Cube reconciliation, idle/pressure policy, and durable cleanup of
   remote creates whose response or persistence was interrupted. Existing Docker
   jobs skip Cube rows, so automatic Cube state convergence is not implemented.
6. Build sanitized revision templates for publish/remix and test cross-owner
   isolation, then add resumable existing-workspace migration and rollback.

Also required before rollout: audit/event coverage for lifecycle operations,
resource admission/quotas, failure recovery, snapshot capacity/backups, and
concurrent end-to-end latency tests. A management API success is not sufficient
proof that an app is ready or isolated.

## Integration review findings addressed

The independent agent review identified body/collection routes outside the
ID-route guard: legacy app-linked Docker creation could bypass provider/owner
selection, and idempotent creation/listing could disclose private Cube metadata.
The branch checks owner/provider before allocation and filters those serializers.
The review also required recoverable deletion after partial remote/local success
and avoiding a success response before supervisor readiness after a canceled
create. Regression tests accompany these changes.

The companion application branch includes the separately validated screenshot
latency fix, measured VPS benchmarks and the broader migration assessment in
`landing/docs/cube-migration.md`.
