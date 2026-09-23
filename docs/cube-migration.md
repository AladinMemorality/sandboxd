# Cube migration integration and acceptance

2026-09-23, paired `codex/cube-migration` branches. Docker remains the default.
The isolated VPS pilot passes the supported runtime and application contracts.
**This is not yet a production replacement:** real model/bridge egress,
production home/tool compatibility, network isolation and operational acceptance
remain release gates. Dependency preparation and journaled transfer are implemented
on this branch, with their limits documented below. No production projects were moved.

## Implemented

- Pinned Cube v0.7.1 lifecycle client; encrypted, durable runtime bindings and
  provider selection that survives VM deletion and operator allowlist changes.
- Fresh-token template bootstrap, explicit UID/GID1000, no effective capabilities,
  all-thread no-new-privileges and non-dumpable supervisors. Build guest binaries
  with `CGO_ENABLED=0`; foreign CGO threads cannot be hardened by this mechanism.
- Authenticated guest status/tasks, event replay, live input/cancel/revert,
  durable results and reconciliation after control-plane interruption. Ambiguous
  submissions return the existing task ID instead of encouraging duplicate work.
- Workspace-scoped files, root `path=.` listing, process logs and ZIP export,
  with bounds and descriptor-relative traversal/link protections.
- Sealed runtime config application/removal with revision acknowledgement;
  `/recreate` preserves the VM identity and avoids restarting an applied revision.
- Stable preview hosts, owner authentication, WebSockets, streaming, app bearer
  headers/cookies, supervisor-port denial and a cached management lease.
- Five-minute preview handoffs issued only through an authenticated service API.
  Host-only HttpOnly cookies; clean relative redirects; reserved cookie-refresh
  endpoint; no capability in ordinary sandbox status responses.
- Immutable sanitized source publication and fresh-template remix. Memory,
  runtime state, owner config, credentials and creator dependencies are not
  copied. Fresh-template dependencies are retained only when manifests/locks match.
- One platform capture service for Chat, sandbox-agent bridge, publish and card
  images. It uses prewarmed single-use isolated browser workers and a scoped
  request broker; application VMs no longer include Chromium. No platform-process
  browser fallback. The companion platform repository documents deployment and
  the exact worker isolation boundary under `landing/services/capture`.
- Private dependency-aware import, reviewed Git import, and journaled offline
  Docker/Cube transfer with verified archives, stable project IDs, task history
  and reverse-copy rollback. See [workspace compatibility](cube-migration/workspace-and-dependencies.md)
  and the [migration runbook](cube-existing-project-migration.md).
- Scoped model relay with task-bound bridge-token digest, expiry, live task
  validation, streaming and revocation. Tests use a mock upstream; default off.

The platform companion handles private iframe/screenshot access, renews cookies
without reloading a running app, checks published/legacy URL permissions, avoids
Cube stop/start during publication, and preserves explicit nonretryable errors.

## Measured isolated pilot

See [raw final run](cube-pilot-results/integration-v4.json). Ubuntu24.04, nested
KVM, 4vCPU/12GiB, NVMe/XFS; small React/Vite + Node fixture, prepared dependencies.
This is a five-resume sample, not a production latency guarantee.

| Operation | Observed time |
| --- | ---: |
| Create, two guests | 344 / 430 ms |
| Authenticated preview resume, median | 463 ms |
| Resume range | 384–1523 ms |
| Publish sanitized source | 48 ms |
| Remix API, fresh template + source import | 800 ms |

Publish excludes screenshot/upload/database work; remix timing ends at the API
response, with frontend/backend readiness checked subsequently. Resume measures
the authenticated preview response and preserves process memory and disk. The
September17 platform screenshot fixture measured authenticated shared-browser
capture at335ms; later measurements below use a different fixture and setup.

The live run verifies actual UI file-query syntax and preview-access URL/cookie
handoff; two guest identities; wrong-tenant denial; frontend/backend preview;
file independence/export; five memory-preserving wake cycles; durable task result
across control-plane restart; runtime config addition/removal; source/remix data
exclusions; UID/groups/capabilities/no-new-privileges; proc token denial; host-path
sentinels and five denied IPv4/IPv6 endpoints. The task CLI is deliberately a
**deterministic OpenCode fixture, not an AI call**. All test guests were removed.

Testing found and fixed real deployment differences: Cube ignored OCI `USER`,
and a thread-local no-new-privileges call did not protect children forked from
other Go threads. Regression tests exercise actual child processes.

## Capture architecture and September23 measurements

Production capture already caches a shared Chromium browser in each platform
process after first use. There is no evidence of a separately managed capture
daemon; a long-running browser found on the VPS belonged to an old backfill job.
The deployed source still uses fixed settle/poll intervals. The branch removes
those waits from the Docker/shared-browser path.

The same complete React/Vite + backend fixture was measured with the deployed
shared-browser code and the revised render-readiness code on the isolated VM:

| Shared-browser operation | Deployed code | Revised readiness |
| --- | ---: | ---: |
| First hero | 1718ms | 713ms |
| Four warm heroes, median | 1480ms | 447ms |

The deployed fixed1200ms settle explains most of this difference. This isolates
browser/readiness changes, excludes sandbox wake, and is **not** a measurement
of the new single-use worker pool. The companion platform service retains raw
reports, source hashes and the reproducible harness.

A [guest-browser experiment](cube-pilot-results/capture-v7.json) measured5649ms
first capture,592ms warm median and2597ms after resume. That approach was removed:
all callers now use the central capture service, and slim app guests carry no
browser. The historical script requires the retired capture-capable template;
it is not a current runtime acceptance test.

## Existing-workspace roundtrip

The [v8 live test](cube-pilot-results/migration-live-v8-2026-09-23.json) passed real
Docker→Cube migration, transferred Git checkpoint reversion, new app writes,
a completed runtimed task/event stream, reverse-copy rollback, and a Docker
HTTP200 probe. Migration took35.2s and rollback734ms for the disposable fixture.
These are one-time offline copy operations, not normal Cube resume latency.
The agent was deterministic fake OpenCode, not a real model call. Test resources
were removed; no production project was moved.

## Reproduce

```sh
bash scripts/check-cube-migration.sh ./...
bash scripts/check-cube-migration.sh -race ./internal/api ./internal/runtime ./internal/cube ./internal/store ./cmd/runtimed ./cmd/sandboxd
```

The privileged guest thread test must also run with `CGO_ENABLED=0`; ordinary
race builds use CGO and explicitly skip that one unsupported configuration.
`image/cube/Dockerfile` builds real guest binaries. `scripts/cube-pilot` is a
separate disposable fixture; **never ship its fake agent** in a production image.
`setup.py` and `test.py` require the isolated benchmark VM marker and keep secrets
outside source. Test failures retain identified guests for inspection; successful
runs delete them. Source upload must exclude macOS `._*` metadata.

The full Go suite passed, with focused race suites for ownership, preview/HMR,
files/ZIPs, task recovery, config, publication and model relay. Platform tests,
production build and real Chromium screenshot fixtures passed separately.

## Operator configuration

| Variable | Meaning |
| --- | --- |
| `SANDBOXD_CUBE_ENABLED` | Explicit true to enable |
| `SANDBOXD_CUBE_API_URL`, `SANDBOXD_CUBE_API_KEY` | Private management API/credential |
| `SANDBOXD_CUBE_PROXY_URL`, `SANDBOXD_CUBE_DOMAIN` | Trusted private ingress routing |
| `SANDBOXD_CUBE_TEMPLATES` | Reviewed preset-to-template JSON map |
| `SANDBOXD_CUBE_APP_IDS` | Explicit internal app allowlist |
| `SANDBOXD_PREVIEW_TOKEN_SECRETS` | Preview signing keys, never guest credentials |
| `SANDBOXD_CUBE_AGENT_RELAY_ORIGIN` | Optional HTTPS relay origin |
| `SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED` | Operator attestation, not proof |

Creation remains private and deny-all outbound. Nonempty domain allowances fail
closed: upstream v0.7.1 learns domain addresses into an allow map that precedes
private/metadata deny rules. See the [worker patch and evidence](../ops/cube/security/README.md)
and [model relay contract](cube-model-relay.md). An eBPF verifier/unit pass alone
is not sufficient to enable Internet access or claim deployed isolation.

## Release gates and limitations

1. Prove the patched worker with allowed-domain positive controls, DNS rebinding,
   metadata/host/sibling destinations, forged reply/source-port traffic, IPv6,
   pause/resume and restart. Review CubeEgress L7 independently. Keep domain
   egress disabled until the deployed configuration passes.
2. Deploy reviewed TLS model/bridge routing and test a real Claude task with
   usage attribution, limits, cancellation and credential revocation. Disabled
   Claude requests currently fail503 before task launch. Package downloads and
   arbitrary external backend APIs are unavailable under deny-all egress.
3. Validate dependency preparation with production registry access and supported
   package managers. Matching dependencies use the prepared fast path; changed
   supported manifests install in a sterile environment with a bounded deadline.
   Reviewed Git import is implemented; unsupported lock/config schemes fail
   explicitly. Source naming rules cannot detect secrets hardcoded into code.
4. Validate HTTPS preview handoffs under browser privacy settings. Cross-site
   sslip.io embeds may be blocked. Any move to same-site preview domains needs
   a platform CSRF and cookie-scope review because previews execute tenant code. Redact capability
   query strings at every outer proxy; sandboxd logs only URL.Path. Already issued
   preview capabilities remain valid up to five minutes after platform access
   is revoked. The public viewer rechecks permission while open.
5. Complete existing-project compatibility before fleet transfer. The CLI now
   quiesces writes, verifies private app/history archives and config, atomically
   switches provider, and reverse-copies new data before rollback. Production
   inventory found55projects with additional owner-home material requiring
   explicit handling, plus oversized files. Changed runtime configuration blocks
   rollback until Docker recreation can apply it safely. No production data moved.
6. Validate representative project sizes, simultaneous wake/publish/remix load,
   admission quotas, backups/restores, snapshot growth, worker loss and upgrade
   recovery. One pilot creation failed with retained test guests; cleanup resolved
   it. Current small-sample timing is not a capacity or availability guarantee.

MicroVMs isolate tenants from the worker; app and supervisor still share a guest
UID. Proc protections do not prevent same-UID signals or local runtime-file
modification. Do not describe the supervisor as a separate trust boundary inside
one guest. No host mounts or global provider credentials belong in templates.
