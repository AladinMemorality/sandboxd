# Global Cube rollout: offline readiness inventory

`production-readiness.py` inventories configuration, resource headroom and review artifacts without executing dotenv files, contacting services, submitting model requests, or changing routing. It prints no secret values. This is a deployment review aid, not an authorization mechanism.

Direct guest NIC egress remains denied. Reviewed pilot apps can use the authenticated reverse broker, but full fleet compatibility and deployed acceptance remain incomplete. This inventory consequently reports `guest_egress_unavailable` and `authorizes_rollout: false`; the diagnostic does not mean the pilot broker is absent. Neither `SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED=true` nor a correctly hashed document establishes acceptance. Keep the established Docker admission path until the remaining application, migration and isolation checks pass. Global creation routing does not transfer existing project data.

## Integration progress, 2026-09-24

Capture selection is independent of app runtime selection. With the default
`CAPTURE_BACKEND=shared`, the inventory requires an explicit positive
`capacity.capture_memory_mib` budget for the warmed browser, but no worker socket.
With `CAPTURE_BACKEND=service`, it requires the socket and budgets the selected
worker count plus manager. Protected management-address exclusions remain
mandatory for both brokers. Optional Cube browser-worker benchmarks are not
acceptance criteria for Cube app migration.

`SITE_ORIGIN` must be the canonical platform origin. Its exact `/files/<id>`
adapter reads only anonymously authorized upload bytes through storage; it does
not permit general HTTP access to the protected platform host or forward cookies.

The migration branch now implements an opt-in host-initiated reverse connection
for reviewed pilot apps. Public HTTP/TLS destinations are resolved and pinned by
the host; model and platform bridge calls use fixed scoped handlers. The guest
NIC still denies outbound traffic. Native Node 22.21 `fetch` and default HTTP/HTTPS
agents passed actual proxy fixtures with `NODE_USE_ENV_PROXY=1`; the inspected
production base is Node 22.23.2. Runtime/API race suites cover reconnection,
credential rotation, stop/resume and task-capability revocation. These local
integration results do not replace deployed model/metering or isolation acceptance.
See [reverse egress](../../docs/cube-reverse-egress.md).

The historical client inventory observed 59 projects. **MyHomeTroc is now in
the requested rollout scope**; the user's later instruction supersedes its
previous in-progress exclusion. Preserve that historical report as evidence of
what was reviewed then, but do not use its exclusion or eligible count for the
new migration plan. Current source review finds MyHomeTroc's PostgreSQL uses a
same-guest Unix socket: the dependency alone does not require outbound database
access. Its Baileys WebSocket client does require explicit HTTPS proxy support
and acceptance; database files, process startup and owner data still require
migration/restore proof. The other previously discovered raw socket call targets
a service inside its own guest. Bounded follow-up source reviews account for the
initial scan limits. Custom Java/native tools still need execution against
migrated data. Neither the older 58-project home inventory nor the historical
59-project client inventory is a frozen plan for the current fleet.

A fresh read-only inventory at 2026-09-24 17:42 UTC observed **61 current
sandboxes**, one active coding task, and 13,298,432,400 regular workspace bytes.
Candidate metadata preflight accepted 59 of 61 and all 31 stored snapshots.
The two live blockers were MyHomeTroc's running PostgreSQL socket and a project
with an active task/changing home. The subsequent isolated MyHomeTroc test
confirmed graceful quiesce removes its socket and permits strict full-home
export/import; do not interpret a live socket as proof that this app cannot
migrate. These are changing-fleet observations, not a drained production plan.
Nine legacy empty-preset candidates and twelve custom-home/tool cases still
need explicit review in the final plan. All 36 observed home symlinks matched
the narrowly reviewed contracts; metadata eligibility does not establish ABI
compatibility or application health.

PostgreSQL is an **opt-in capability**, exposed by the `node-postgres` starter
or an intentionally added worker. The existing seven starters and the default
React Pro selection do not start or initialize a database. The actual v4
[PostgreSQL candidate fixture](../../docs/cube-pilot-results/postgres-candidate-2026-09-24/README.md)
passed SQL persistence across pause/resume, supervisor reexec, explicit manifest
activation, source restore, and quiesced full-home export/import. A fresh source
remix had an empty database, and repeated identical manifest activation did not
restart the app. This synthetic acceptance is independent of global routing.
Owner source restore now
imports into the existing Cube VM instead of deleting its private home; it
rejects active tasks and incompatible templates. Remixes retain the separate
fresh-VM contract and do not inherit private database contents.

The opt-in [public registry fixture](functional/2026-09-24/registry-clients.md)
passed all seven checks for native Node HTTPS, curl, and fresh npm/pnpm/pip
installs through the actual broker and reviewed Cube image in the isolated
nested cluster. It verifies representative public-registry compatibility, not
all owner dependency graphs or arbitrary native clients. This fixture does not
exercise protected destinations or replace the outstanding security acceptance.

Optional capture-worker experiments are retained in platform
`services/capture/benchmarks/2026-09-24/cube/`. They concern a separate renderer
implementation and do not establish application create/resume/publish/remix speed.
No further capture-worker tuning is required for this migration.

Global admission remains disabled in the configuration loader. The offline
inventory deliberately continues to report blocked rather than interpreting the
new implementation or a boolean operator flag as deployment acceptance.

## Observed production configuration, 2026-09-23

A read-only inspection of the running `baarcha-landing` Next process found `SANDBOXD_AGENT` and `SANDBOXD_MODEL` unset. The deployed client defaults therefore select `claude-code` and `glm-5.3-flash[1m]`. Other agent implementations are not a prerequisite unless the actual product/configuration selects them. OpenRouter credentials were present (presence only was inspected); the merged fallback remains behind the existing owner-authenticated and metered Anthropic Messages route. The Cube relay forwards to that same host-side agent proxy, so fallback adds no guest destination. A later bounded actual Cube Claude task passed through the existing deployed model route and accessed scoped project files; two successful metering rows were observed and completed/cancelled task capabilities were revoked. The relay was an isolated functional fixture, not the final production Cube deployment. Forced fallback and a distinct cancelled-usage/final-credit-ledger result were not established. See [functional acceptance](functional/2026-09-24/README.md).

The platform bridge is explicitly HTTPS at `baarcha.tn`; previews use `https://%ID%.preview.65.108.225.153.sslip.io`. Ordinary HEAD checks verified DNS and TLS for `baarcha.tn` (200) and a nonexistent preview hostname (404). These establish front-door availability only: they do not prove guest reachability, authenticated preview privacy, iframe cookies, or bridge task execution. The running runtime process had no Cube/relay environment configuration, and the platform had no capture-service socket or capture management-denial CIDR configuration. Its `SANDBOXD_ANTHROPIC_UPSTREAM` was present with HTTPS hostname `baarcha.tn`, confirming the configured metered platform destination; only presence/scheme/hostname/port were inspected. The absent `SANDBOXD_AGENT_PROXY_URL` uses runtime defaults and is not evidence that the existing auth proxy is disabled.

## Connectivity and isolation acceptance

- Model relay: fixed reviewed HTTPS origin, task/project-bound token, the existing server-side model proxy and metering. Do not expose provider credentials or a generic host proxy to guests.
- Platform bridge: scoped callback route for asset/image/screenshot and backend operations. Docker's `host.docker.internal` default is not a Cube route. Model connectivity alone does not provide bridge connectivity.
- Dependency registry and backend API traffic: separate requirements for real owner code. Prepared templates avoid some installs; changed manifests still need permitted registries and bounded installation. General backend applications need a reviewed outbound design, not an assumption that a model relay provides internet access.
- A same-host public relay address can still be a protected host address. Do not allow an entire host/CIDR to make one HTTPS service work. Establish reviewed routing and exact service authorization before enabling connectivity.
- DNS/TLS and previews: review browser access, cross-site iframe cookie behavior, same-site CSRF/cookie scope where applicable, and query-capability redaction at every externally deployed proxy. A valid certificate or unit test does not prove these deployed properties.
- Capture: inventory every public management/worker/NAT address in `CAPTURE_DENY_CIDRS`; private/link-local protection alone does not exclude a machine's public address. The default shared renderer uses one warmed Chromium process with a fresh context per capture and scoped request brokering. Browser contexts are not process or microVM isolation. The optional service uses disposable workers and requires its own deployment acceptance.

The security handoff is [security/results/2026-09-23/HANDOFF.md](security/results/2026-09-23/HANDOFF.md). The 87 attached-program checks were program test invocations, not live packet transmission. Positive connectivity/DNS controls and the complete deployment lifecycle were not accepted. Automatic review previously rejected extended packet testing; this inventory does not resume those tests, enable flags, or supply an alternative test path. Any future security work needs its own permitted, reviewed scope.

## Run the offline inventory

Use private operator-owned copies of the intended runtime/platform environment files and a JSON plan. Do not commit secrets or use the example quantities as production measurements.

```sh
python3 ops/cube/production-readiness.py \
  --runtime-env /private/operator/runtime.env \
  --platform-env /private/operator/platform.env \
  --plan /private/operator/review/plan.json
```

Exit 2 means a valid blocked inventory; exit 1 means invalid/unreadable input. There is intentionally no successful rollout exit code while the deployment acceptance gates remain incomplete. Dotenv input is literal data: variable interpolation, command substitution and shell execution are unsupported. Supply fully resolved values; unquoted inline comments are not stripped. Files are bounded to 1 MiB, and review artifacts to 2 MiB each.

Plan structure (illustrative quantities only):

```json
{
  "required_agents": ["claude-code"],
  "protected_addresses": ["65.108.225.153"],
  "capacity": {
    "host_memory_mib": 16384,
    "reserved_memory_mib": 4096,
    "running_guests": 4,
    "peak_waking_guests": 2,
    "guest_memory_mib": 1024,
    "capture_memory_mib": 2048,
    "free_storage_bytes": 100000000000,
    "project_export_bytes": 10000000000,
    "rollback_reserve_bytes": 10000000000,
    "backup_staging_bytes": 10000000000,
    "planned_snapshot_growth_bytes": 10000000000
  },
  "evidence": {}
}
```

Memory budgeting includes active plus additional waking guests and an explicit host reserve. The default shared renderer requires a measured/planned positive `capture_memory_mib` budget. Only `CAPTURE_BACKEND=service` instead uses `capture_workers` × 768 MiB plus the 512 MiB manager limit; match the worker count to the configured pool size. This is a conservative planning sum, not a measured RSS prediction. Host reserve must cover production databases/platform/control-plane, other workloads and margin. Storage includes concurrent exports, retained rollback data, backup staging and expected snapshot growth; account for their actual filesystems separately if they do not share a disk. CPU, I/O contention and p95/p99 wake/queue latency still require deployment-sized acceptance; tiny fixture timings cannot establish fleet capacity.

Each `evidence` entry has `{ "path": "relative/artifact.json", "sha256": "64 lowercase hex characters" }`, with paths confined to the plan directory. Required categories are `network-isolation`, `claude-model-metering`, `bridge-assets`, `dependency-registry`, `preview-browser-tls`, `backup-restore`, `worker-recovery`, `concurrent-load`, and `template-review`. A digest only establishes which bytes a reviewer saw. The script does not interpret a document's claims, freshness, signer, deployment identity or completeness.

Backups and restore proof must cover owner workspace/configuration, private history, runtime bindings, database state, snapshots and any owner files outside the workspace. Source publication is not a full backup. Retain an independently restorable Docker rollback until transferred owner data and stable preview/publish/remix identifiers have been verified. Use fresh fleet inventory, not a historical project count.

The [actual independent deployment-file recovery fixture](functional/2026-09-24/deployment-recovery.md)
passed using real Docker/Cube lifecycle operations, copied SQLite/key/full-home/
library/archive/config scopes, deletion of original synthetic files and guests,
and restoration into a separate root with real Docker HTTP readiness. This
advances the earlier stubbed recovery evidence. It is still synthetic, on the
same physical host, with fixture-managed path relocation/config delivery; it
does not establish an off-host production restore or full platform service boot.

## Independent capture service reference

The default shared renderer has eight active slots, 64 queued requests and a
45-second per-request deadline. It does not require a worker manager or health
socket. Verify authorized private/public project capture through the platform
when accepting the application rollout.

Only the optional `CAPTURE_BACKEND=service` uses a separate protected `.health`
Unix socket and disposable worker replenishment. Its operator instructions and
historical capacity measurements live in the platform's `services/capture/README.md`
and `services/capture/benchmarks/`. Those results must not be used as app-runtime
performance evidence or as a prerequisite for retaining the shared renderer.

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s ops/cube -p 'test_production_readiness.py'
```
