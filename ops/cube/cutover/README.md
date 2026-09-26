# Cutover traffic fence

These Caddy fragments are inert artifacts, not an installed maintenance mode.
Keep the unchanged configuration and its adapted JSON privately before use.
First wrap the existing `baarcha.tn` handlers in an explicit `route`, with the
`drain-platform.caddy` import first inside that route; leave site/TLS settings
outside. Validate/adapt the candidate before reload. A standalone site-level
`route` sorts after `handle` in Caddy and is bypassed: the real routing fixture
caught this in the initial inert candidate. Explicit outer ordering is required.
The fence stops new project operations from web, chat,
voice, direct tool calls and admin actions while preserving already accepted
model and bridge requests. Existing HTTP streams are not canceled by a reload.

After running tasks, chat/voice/tool requests and runtime writers have drained,
replace that import with `offline-platform.caddy`, and import
`offline-previews.caddy` before `reverse_proxy` in an explicit outer route of the
preview-host block, including public aliases, with TLS outside that route.
Check externally that project mutations and previews return503/Retry-After,
while the landing page, authentication and billing remain available. The
platform's `/api/v1/messages` model relay stays available; it does not dispatch
project tools. Stop the controller, confirm no active writer/stream, and acquire
the CLI's exclusive database maintenance fence before regenerating inventory or
exporting owner data. A zero running-task count alone does not prove all writers
have drained. Confirm direct runtime/Traefik host ports are loopback-only or
otherwise fenced; Caddy cannot fence a bypass route.

Do not pause/clone MyHomeTroc while its database or live messaging integration is
still writing. Gracefully stop its source through the migration journal. Keep
original containers and all archives. Refresh fleet identity, full home manifests
and measured export sizes after source quiescence; live estimates may be partial.

Remove only these imports after the new controller, provider bindings, aliases
and owner previews pass acceptance. Validate/adapt and reload Caddy again, then
verify external routes. Restoring routing is not a data rollback: use the
per-project migration journal to retain any writes made on Cube.

For reviewed version2 cohorts, a terminal foreground migration CLI failure stops
further waves. The coordinator retains completed Cube projects and may abort
only known uncommitted targets using the native CLI. It reopens only after all
journals are terminal, every original source is retained, current bindings match,
and accepted projects pass normal API checks. Timeouts, uncertain creation,
postcommit incomplete phases and failed aborts retain the fence for explicit
recovery. Aborted journals and archives are never deleted; retry requires an
explicit audited replan. The result records partial completion accurately.

`proxy_import_limit.py` renders (but does not install) the migration-only4GiB
upload locations from a pinned installed Cube proxy config. Validate with the
actual nginx image, retain the original file, and preserve its bind-mounted inode
when applying a graceful reload. The runtime accepts the same bounded size;
ordinary uploads retain their existing limit. See the production receipt in
`results/2026-09-26-import-proxy-limit` for scope and verification limits.

Explicit retries use a separate version2 cohort of at most four rows, each pinning
`retry_from_runtime_id` to its aborted target. The native `replan` action requires
an offline database fence, reviewed fleet identity, current source eligibility,
unchanged source/app identity, a deleted admission record and provider404 for the
former target. In one transaction it retains the full old journal and task scope
in `runtime_migration_attempt`, then freezes a new plan/task scope. Recovery files
remain in their old directory; new artifacts use a random journaled
`attempt-<generation>` directory. Replan creates no guest. Explicit resume performs
the new migration. A lost replan response is inspected through the journal; do
not replan again blindly.

Source-absent `.cache` scaffolding may be removed only when it is an empty
owned0755 directory or contains only the approved empty owned0644 `.gitkeep`.
Any nonempty cache, changed metadata or symlink is retained. The source manifest
is never expanded or weakened to ignore owner data. This accompanies the existing
exact-stock `.bash_logout` reconciliation at the imported/quiesced boundary.

While previews remain online, their normal resumes consume READY pause
snapshots. Version2 cohorts may refresh only that terminal count after checking
the exact canonical bindings and current native reconciliation. Every other
provider table and status stays pinned. The coordinator samples again after
traffic is fenced, then disables this allowance before pausing guests; only
its acknowledged pauses can advance the frozen baseline afterward. Read-only
preflight and execution receipts retain any observed count changes.
