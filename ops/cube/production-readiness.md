# Global Cube rollout: offline readiness inventory

`production-readiness.py` inventories configuration, resource headroom and review artifacts without executing dotenv files, contacting services, submitting model requests, or changing routing. It prints no secret values. This is a deployment review aid, not an authorization mechanism.

The current runtime always denies guest outbound traffic; nonempty operator domain allowances are rejected. Therefore this version always reports `guest_egress_unavailable` and `authorizes_rollout: false`. Neither `SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED=true` nor a correctly hashed document changes that result. Keep the established Docker admission path until a separately reviewed connectivity implementation and deployment acceptance exist. Global creation routing does not transfer existing project data.

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

The latest client inventory observed 59 projects, including the separately
excluded MyHomeTroc project. Its PostgreSQL dependency is excluded with that
project; the other discovered raw socket call targets a service inside its own
guest. Bounded follow-up source reviews account for the initial scan limits.
Custom Java/native tools still need execution against migrated data. The older
58-project home inventory is not a frozen plan for the changed fleet.

A dedicated single-use Cube capture template now restores warmed Chromium and
passes authenticated capture/cleanup. Eight sequential samples passed, with
median create-to-health 186 ms and confirmed deletion 319 ms. A paired two-worker,
eight-job comparison nevertheless took approximately 10.1 seconds on Cube versus
6.1 seconds on Docker: browser page preparation after restoration currently
dominates. Preparing unused blank pages before snapshotting is under test. Do not
enable the Cube capture backend or claim a sustained speedup from restore time
alone. Platform evidence is under `services/capture/benchmarks/2026-09-24/cube/`.

Global admission remains disabled in the configuration loader. The offline
inventory deliberately continues to report blocked rather than interpreting the
new implementation or a boolean operator flag as deployment acceptance.

## Observed production configuration, 2026-09-23

A read-only inspection of the running `baarcha-landing` Next process found `SANDBOXD_AGENT` and `SANDBOXD_MODEL` unset. The deployed client defaults therefore select `claude-code` and `glm-5.3-flash[1m]`. Other agent implementations are not a prerequisite unless the actual product/configuration selects them. OpenRouter credentials were present (presence only was inspected); the merged fallback remains behind the existing owner-authenticated and metered Anthropic Messages route. The Cube relay forwards to that same host-side agent proxy, so fallback adds no guest destination. A real Claude task through the deployed relay, with primary/fallback accounting and cancellation, is still unverified.

The platform bridge is explicitly HTTPS at `baarcha.tn`; previews use `https://%ID%.preview.65.108.225.153.sslip.io`. Ordinary HEAD checks verified DNS and TLS for `baarcha.tn` (200) and a nonexistent preview hostname (404). These establish front-door availability only: they do not prove guest reachability, authenticated preview privacy, iframe cookies, or bridge task execution. The running runtime process had no Cube/relay environment configuration, and the platform had no capture-service socket or capture management-denial CIDR configuration. Its `SANDBOXD_ANTHROPIC_UPSTREAM` was present with HTTPS hostname `baarcha.tn`, confirming the configured metered platform destination; only presence/scheme/hostname/port were inspected. The absent `SANDBOXD_AGENT_PROXY_URL` uses runtime defaults and is not evidence that the existing auth proxy is disabled.

## Connectivity and isolation acceptance

- Model relay: fixed reviewed HTTPS origin, task/project-bound token, the existing server-side model proxy and metering. Do not expose provider credentials or a generic host proxy to guests.
- Platform bridge: scoped callback route for asset/image/screenshot and backend operations. Docker's `host.docker.internal` default is not a Cube route. Model connectivity alone does not provide bridge connectivity.
- Dependency registry and backend API traffic: separate requirements for real owner code. Prepared templates avoid some installs; changed manifests still need permitted registries and bounded installation. General backend applications need a reviewed outbound design, not an assumption that a model relay provides internet access.
- A same-host public relay address can still be a protected host address. Do not allow an entire host/CIDR to make one HTTPS service work. Establish reviewed routing and exact service authorization before enabling connectivity.
- DNS/TLS and previews: review browser access, cross-site iframe cookie behavior, same-site CSRF/cookie scope where applicable, and query-capability redaction at every externally deployed proxy. A valid certificate or unit test does not prove these deployed properties.
- Capture: inventory every public management/worker/NAT address in `CAPTURE_DENY_CIDRS`; private/link-local protection alone does not exclude a machine's public address. The shared service uses single-use isolated Docker workers; its default boundary shares the host kernel and is not a microVM.

The security handoff is [security/results/2026-09-23/HANDOFF.md](security/results/2026-09-23/HANDOFF.md). The 87 attached-program checks were program test invocations, not live packet transmission. Positive connectivity/DNS controls and the complete deployment lifecycle were not accepted. Automatic review previously rejected extended packet testing; this inventory does not resume those tests, enable flags, or supply an alternative test path. Any future security work needs its own permitted, reviewed scope.

## Run the offline inventory

Use private operator-owned copies of the intended runtime/platform environment files and a JSON plan. Do not commit secrets or use the example quantities as production measurements.

```sh
python3 ops/cube/production-readiness.py \
  --runtime-env /private/operator/runtime.env \
  --platform-env /private/operator/platform.env \
  --plan /private/operator/review/plan.json
```

Exit 2 means a valid blocked inventory; exit 1 means invalid/unreadable input. There is intentionally no successful rollout exit code while the source-level connectivity blocker remains. Dotenv input is literal data: variable interpolation, command substitution and shell execution are unsupported. Supply fully resolved values; unquoted inline comments are not stripped. Files are bounded to 1 MiB, and review artifacts to 2 MiB each.

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
    "capture_workers": 2,
    "free_storage_bytes": 100000000000,
    "project_export_bytes": 10000000000,
    "rollback_reserve_bytes": 10000000000,
    "backup_staging_bytes": 10000000000,
    "planned_snapshot_growth_bytes": 10000000000
  },
  "evidence": {}
}
```

Memory budgeting includes active plus additional waking guests, an explicit host reserve, `capture_workers` × 768 MiB (default two workers) and the 512 MiB manager limit. Match this field to the manager's configured pool size. This is a conservative planning sum, not a measured RSS prediction. Host reserve must cover production databases/platform/control-plane, other workloads and margin. Storage includes concurrent exports, retained rollback data, backup staging and expected snapshot growth; account for their actual filesystems separately if they do not share a disk. CPU, I/O contention and p95/p99 wake/queue latency still require deployment-sized acceptance; tiny fixture timings cannot establish fleet capacity.

Each `evidence` entry has `{ "path": "relative/artifact.json", "sha256": "64 lowercase hex characters" }`, with paths confined to the plan directory. Required categories are `network-isolation`, `claude-model-metering`, `bridge-assets`, `dependency-registry`, `preview-browser-tls`, `backup-restore`, `worker-recovery`, `concurrent-load`, and `template-review`. A digest only establishes which bytes a reviewer saw. The script does not interpret a document's claims, freshness, signer, deployment identity or completeness.

Backups and restore proof must cover owner workspace/configuration, private history, runtime bindings, database state, snapshots and any owner files outside the workspace. Source publication is not a full backup. Retain an independently restorable Docker rollback until transferred owner data and stable preview/publish/remix identifiers have been verified. Use fresh fleet inventory, not a historical project count.

## Capture admission and health

The shared capture service exposes read-only health on a separate protected `.health` Unix socket, so capture saturation cannot starve its two bounded health slots. `node services/capture/health.mjs --ready` requires an idle prewarmed worker; `--live` only checks that the manager answers. Inspect `state`, worker counts and queue depth together. A busy pool is not a reason to restart it. Defaults remain two workers, eight queued jobs and a 45-second deadline. Operators can configure 1–8 workers with an explicit RAM budget and 0–8 queued jobs; update this inventory's capacity calculation when deploying a nondefault pool. Each used worker is destroyed and replenished; burst timing includes replacement cost.

Offline regression coverage includes 100 simultaneous requests with exactly two active workers, eight admitted queued jobs and ninety rejected requests, plus single-use cleanup and read-only probes that allocate no workers. This establishes admission bounds, not Docker/browser resource or production load capacity. Existing measured fixture capture results and real-browser functional evidence remain under platform `services/capture/benchmarks/2026-09-23/`.

A later disposable outer-VPS run verified actual 2/4/8-worker admission, correct rendering of all 65 accepted captures, health under saturation and complete cleanup. Eight prepared captures finished in 585 ms with eight warm workers, but full replacement took about 15 seconds; a subsequent saturated 16-capture group took 15.8 seconds and another 19.4 seconds to refill. Thus the user-visible warm speed is real, but sustained fleet throughput is not yet accepted. A separate short profile located the dominant cost in Docker lifecycle: 2458 ms removing the used worker, 3265 ms starting the replacement, then roughly one second of Node/browser readiness. Single-use isolation and confirmed-cleanup bounds remain intact. Specify sustained rate, burst concurrency and acceptable queue latency before accepting production capacity. See that directory's `capacity.json`, `replacement.json` and README before selecting production pool size.

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s ops/cube -p 'test_production_readiness.py'
```
