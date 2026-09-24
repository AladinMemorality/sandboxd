# Global Cube rollout

The target is the whole platform: existing projects, new creates, published app
links, remixes, AI tasks, configuration, previews and rollback. The branch is not
yet ready for production cutover. Global scope does not waive any project's
data-compatibility checks or the deployed network-isolation acceptance gate.

## App runtime and screenshots are separate rollouts

Cube executes migrated projects. The sandboxd API remains a compatibility
control plane for stable app IDs, tasks, previews and snapshots; retaining that
API does not mean retaining Docker as the app execution engine.

Screenshots remain one platform feature for authorized Chat and Sandbox agents.
The companion platform is separating renderer selection: `CAPTURE_BACKEND=shared`
retains the warmed browser with fresh contexts and scoped HTTP/WebSocket brokers;
`service` explicitly selects the optional worker manager. There is no automatic
failure fallback. Neither shared Chromium nor Docker workers are microVMs.

The capture-worker results below are historical, independent feature research.
Their throughput is not a Cube app-migration gate. Validate capture authorization
and functionality against the selected renderer during app rollout; replacing
the renderer requires its own release and capacity acceptance.

## Operator selection and durable identity

`SANDBOXD_CUBE_ROLLOUT=allowlist` is the default when Cube is enabled. It requires
the existing explicit `SANDBOXD_CUBE_APP_IDS` list. `global` instead selects Cube
for all new owned-app sandboxes and remixes. Global configuration rejects an
overlapping app allowlist, missing preset templates, or an unconfigured model
relay. Its relay attestation is an operator declaration, not evidence of working
connectivity or isolation. Domain egress remains disabled in this branch.

Global mode does not reinterpret or replace existing Docker rows. The offline
migration journal must verify their data and switch their provider explicitly.
Existing Cube provider identity survives mode changes and VM deletion: turning
the enable flag off does not silently route those projects to Docker. Legacy
unowned create endpoints cannot bypass global selection to create Docker guests.

The current platform's required coding agent is Claude Code. The model relay
must work through the platform's deployed model fallback and metering routes;
mock-upstream tests alone are insufficient. Package registries, bridge callbacks
and arbitrary user backend APIs each need an explicitly reviewed connectivity
path in addition to model inference.

## Existing published links

Legacy raw snapshot IDs and records remain unchanged. Global remix converts only
the frozen `workspace/app` subtree to a bounded sanitized source archive and
imports it into a fresh Cube guest. No home directory, provider authentication,
creator runtime configuration or database becomes remix content. Conversion
uses descriptor-relative access and rejects linked/special files and filesystem
boundaries. It enforces the existing public source size limits.

The source app must still belong to the snapshot owner and have a reviewed
preset/template mapping. Missing or unknown presets are reported for review;
they are never guessed from file names. Private migration's target-preset update
can supply the reviewed mapping for legacy apps. An old snapshot can also restore
an already-migrated Cube app. Restore validates the source archive and imports
it into the same guest, preserving the private home, databases and sandbox ID.
A snapshot requiring a different template must first use an explicit runtime
migration that preserves private data. Forks still create a fresh private home.
Restore cannot implicitly replace an unmigrated Docker guest with Cube.

The 2026-09-23 read-only refresh found **56 sandboxes**, no active coding tasks at
that instant, approximately 11.94 GB of regular app files, and two projects with
individual files above the old 128 MiB migration limit. Nine app presets were
empty. A separate metadata-only query found **30 ready raw snapshots**, all with
existing source apps; one lacked a source-app preset. These are observations,
not a frozen deployment inventory. The [recorded inventory](cube-pilot-results/production-inventory-global-2026-09-23.json)
must be refreshed after admission is drained, immediately before cutover.

## Release acceptance

Before any global switch, require all of the following for the actual deployment:

- Complete same-owner data disposition for every project, including modified
  home configuration, tools, caches, workspace siblings, provider history retained
  privately, hardlinks and large files. Unknown paths block that project.
- Verified private transfer and rollback with changed configuration, canonical
  task history, checksums and a tested restore from independent backups.
- Reviewed templates for every offered preset; successful dependency preparation
  through the production registry route and real frontend/backend workloads.
- Completed network-isolation acceptance, real AI/tool calls, metering, limits,
  cancellation and credential revocation. The earlier worker trial is incomplete.
- Actual HTTPS browser checks for owner/public/unlisted links, privacy settings,
  signed-preview refresh, WebSockets, old remixes, publish, resume and screenshots.
- Representative simultaneous create/wake/remix/publish load, resource quotas,
  disk/snapshot growth and failure/upgrade recovery. Capture-worker replacement
  is an additional requirement only when that separate service is deployed.

Drain new work and wait for active tasks, acquire the migration maintenance fence,
then regenerate the full fleet plan. Any new/missing project or changed eligibility
requires review before execution. Retain source containers and recovery archives
until post-migration application checks and the rollback retention period complete.
Do not present a subset of passing projects as global acceptance.

The optional capture service is deployed separately from guest migration. Its
runbook defines the protected Unix socket, disposable workers, host-address
exclusions and readiness checks. Only an explicitly selected service backend
requires that deployment; retaining the shared renderer does not require a socket.

## Validation of the global migration branch, 2026-09-23

The platform branch includes production's OpenRouter fallback and cold-model
retry changes. Its production build passed. The complete platform test suite,
using Node 22 with Python and a colocated disposable PostgreSQL, passed **1,117
tests**, with **one optional GeoIP fixture skipped** and no failures. A prior run
over an SSH database tunnel failed a two-second first-byte assertion; the same
unchanged assertion passed with the colocated database. These are integration
fixtures, not paid production-model or browser deployment acceptance.

Runtime, supervisor, migration, store and migration CLI suites passed. Focused
race tests cover global routing and private transfers. The new app transfer
round-tripped a **270 MiB incompressible file/archive**, exceeding both old
per-file and compressed-archive limits, with matching canonical checksums.
Streaming does not allocate a whole archive or whole regular file. Limits are
4 GiB compressed, 8 GiB expanded, 1 GiB per file, 200,000 app entries and 64 MiB
ZIP index metadata. ZIP64, cancellation, permissions, malformed metadata,
authentication and redirect refusal are covered. An actual disposable UID1000
supervisor test verified authenticated export/import and the restart fence.

Owner-home transfer requires the [explicit manifest](cube-migration/private-home.md).
The [migration runbook](cube-existing-project-migration.md) describes complete
fleet preflight, reviewed missing-preset assignments, identity digest fencing,
home journals and changed-configuration rollback. A real Docker rollback fixture
verified normal wake with updated app configuration and retained new files; its
Cube side was a filesystem fixture, so it is not additional real-Cube acceptance.

## Current application acceptance, 2026-09-24

A bounded actual Cube Claude Code task successfully used the scoped project
library/file bridge and wrote verified output. The existing platform model route
recorded two successful usage rows, and completed/cancelled task capabilities
were revoked. This was an isolated functional relay; forced fallback and a final
cancelled-credit-ledger assertion remain unverified. See the
[functional evidence](../ops/cube/functional/2026-09-24/README.md).

All seven reviewed preset images passed actual Cube startup, process-preserving
pause/resume and strict workspace/home export checks. The
[preset matrix](cube-pilot-results/reviewed-preset-matrix-2026-09-24.md) pins the
isolated-cluster images/templates and exact acceptance scope. These checks establish
preset behavior, not every existing project's native-tool compatibility or a
comparative speedup. The [actual API lifecycle fixture](../ops/cube/functional/2026-09-24/app-lifecycle.md)
also passed source publication, fresh-owner remix, stop/start and owner restore,
with private sentinels excluded from shared source. Single nested observations
were 6.417 s create-to-supervisor-ready, 4.010 s remix-to-ready and 1.395 s
pause/resume-to-ready; these are not a matched speedup benchmark. A separate
`.env.local` reload hang remains under investigation.

The [independent recovery fixture](cube-pilot-results/independent-backup-restore-2026-09-24.md)
restored a consistent SQLite snapshot, encryption key, source home and recovery
archives after deleting the original test files. It preserved newer data/task
history and rejected corrupt archives. Its lifecycle was stubbed; it does not
establish a real off-host production disaster-recovery rehearsal. Migration and
source-retirement readiness now require running worker processes and a stable
observation window, without adding that delay to ordinary app resume.

No production project has been switched. Remaining gates include complete
control-plane lifecycle and migration/rollback acceptance, fresh fleet manifests,
independent backup restoration, deployed isolation and HTTPS link checks, and
representative application load/recovery. The offline inventory continues to
report `authorizes_rollout: false`. Its diagnostic does not mean the implemented
pilot reverse broker is absent.

Historical optional capture-worker experiments remain in the platform's
`services/capture/benchmarks/`. Screenshots retain the separately selected warmed
shared renderer; its authorization and rendering need functional verification,
but worker replacement or capture throughput is not an app-runtime migration gate.

## Merge, deployment and production observation

The user authorized merging, deployment and production monitoring once the release
gates pass. Another generic deployment approval is not required. This authorization
does not turn an incomplete connectivity or data-compatibility check into a pass.

The platform's `.github/workflows/landing.yml` deploys successful pushes to `main`;
merging and pushing there is a production action. Runtime's
`.github/workflows/deploy-project-x.yml` deploys pushes to `deploy/project-x`,
independently of its CI workflow. Do not assume the runtime deployment trigger
waits for all tests. Use the exact revisions and image digests that passed the
required workflows and record both repositories' deployed identities.

Deployment order, after acceptance:

1. Record the running revisions, service configuration references, current fleet
   identity, active tasks, traffic/error/latency baseline and verified backup
   locations. Keep secrets and signed preview capabilities out of evidence.
2. Retain the existing warmed screenshot backend and verify owner/public/private
   captures through its scoped broker. Deploy a replacement manager only as an
   independently selected and validated release. Its worker benchmarks do not
   establish app create/resume/publish/remix performance. Keep the previous
   platform revision available for application rollback.
3. Deploy the reviewed Cube worker/templates/relay and runtime with new admission
   still controlled. Verify the actual HTTPS, model, bridge, dependency and
   isolation paths against these exact artifacts. Recheck readiness after restart.
4. Freeze project creation and task submissions, drain active work, acquire the
   offline maintenance fence and regenerate the entire fleet plan. Resolve every
   project and published-snapshot blocker; execute journaled migration with the
   reviewed fleet identity and per-project manifests. Preserve source data and
   recovery archives. The acceptance scope is the whole fleet, not a passing
   subset. Follow the migration runbook's recovery path for interrupted phases.
5. Deploy the reviewed platform revision and enable global admission only after
   the complete migration and service dependencies have passed. Verify all
   expected projects, provider bindings and published snapshot identities against
   the frozen inventory before reopening normal traffic.

Production tests must use a dedicated operator-owned test project for mutations,
with ordinary authenticated and unauthenticated browser sessions. Exercise new
frontend and backend creation, files and secrets, real AI streaming and billing,
cancel/retry, API and WebSocket previews, stop/resume, publish, old and new remix
links, owner/public/unlisted/private access and screenshots from both Chat and
sandbox agents. Inspect existing fleet health and identifiers without editing
users' applications. Record expected and observed results; HTTPS200 alone is not
application acceptance. Remove only the test resources after checking billing and
publication cleanup, and regenerate the post-test inventory accordingly.

Observe actively for at least 15 minutes after traffic reopens and repeat checks
at one hour. Record platform/runtime/worker restarts and errors, failed or stuck
AI tasks, inference accounting, preview failures, create/wake/remix/publish and
capture queue latency, capture ready/warming/failed counts, CPU, memory and disk
growth. Compare latency with the measured baseline and the pre-agreed sustained
rate/burst targets; warm-only screenshots cannot establish acceptance. Do not
report the one-hour check as completed before it runs, or imply background
monitoring is installed merely because a terminal observation completed.

Any owner-boundary failure, missing data, incorrect billing or unrecoverable
provider binding stops admission immediately. Stop and investigate sustained
application errors, repeated crashes, exhausted disk or accepted capture jobs
failing; a busy capture pool alone is not a restart trigger. Platform code rollback
uses the recorded previous revision. Project rollback uses verified reverse-copy
and the durable migration journal, including current app configuration; changing
the global flag or replaying a stale database backup is not data-safe rollback.
Retire source containers only after post-deployment acceptance and the documented
retention period, using the identity-checked retirement command.
