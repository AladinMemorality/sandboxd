# Cube migration integration and acceptance

2026-09-17, paired `codex/cube-migration` branches. Docker remains the default.
The isolated VPS pilot passes the supported runtime and application contracts.
**This is not yet a production replacement:** real model/bridge egress,
dependency preparation, existing-project transfer and operational acceptance
remain release gates. No production projects were moved.

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
platform screenshot fixtures separately measured authenticated capture at 329 ms.

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
3. Prepare dependency-aware source revision templates; changed dependencies
   currently fail explicitly. Git import, legacy Docker snapshot conversion and
   some lockfile schemes remain unsupported. Source naming rules cannot detect
   secrets hardcoded into otherwise publishable code/assets.
4. Use same-site HTTPS preview domains for reliable browser cookies. Cross-site
   sslip.io embeds may be blocked by browser privacy settings. Redact capability
   query strings at every outer proxy; sandboxd logs only URL.Path. Already issued
   preview capabilities remain valid up to five minutes after platform access
   is revoked. The public viewer rechecks permission while open.
5. Implement journaled, resumable existing-workspace migration: quiesce writes,
   validate copy/config, atomically switch provider, retain stopped source, and
   reverse-sync new writes before rollback. No existing-project migration tool
   or production data movement is represented by this pilot.
6. Validate representative project sizes, simultaneous wake/publish/remix load,
   admission quotas, backups/restores, snapshot growth, worker loss and upgrade
   recovery. One pilot creation failed with retained test guests; cleanup resolved
   it. Current small-sample timing is not a capacity or availability guarantee.

MicroVMs isolate tenants from the worker; app and supervisor still share a guest
UID. Proc protections do not prevent same-UID signals or local runtime-file
modification. Do not describe the supervisor as a separate trust boundary inside
one guest. No host mounts or global provider credentials belong in templates.
