# Global Cube rollout

The target is the whole platform: existing projects, new creates, published app
links, remixes, AI tasks, configuration, previews and rollback. The branch is not
yet approved for production cutover. Global scope does not waive any project's
data-compatibility checks or the deployed network-isolation acceptance gate.

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
an already-migrated Cube app. Restore validates and retains the source archive
before deleting the current Cube guest, avoiding a second filesystem export.
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
  capture worker replacement, disk/snapshot growth and failure/upgrade recovery.

Drain new work and wait for active tasks, acquire the migration maintenance fence,
then regenerate the full fleet plan. Any new/missing project or changed eligibility
requires review before execution. Retain source containers and recovery archives
until post-migration application checks and the rollback retention period complete.
Do not present a subset of passing projects as global acceptance.

The shared capture service is deployed separately from guest migration. Its
platform runbook defines the protected Unix socket, disposable prewarmed workers,
host-address exclusions and readiness checks. Never deploy capture callers before
the service is ready: there is deliberately no browser fallback in the platform.
