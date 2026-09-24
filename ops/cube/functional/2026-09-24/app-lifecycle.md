# Actual application API lifecycle, 2026-09-24

The final disposable functional test passed in 20.41 s using the reviewed React Pro v3 template, real Cube transport, normal sandboxd API handlers and a private disk SQLite database. It exercised create, prepared-source publication/remix, pause/resume, and owner restore. No model requests were made.

| Operation | Observed elapsed |
| --- | ---: |
| Create through supervisor-reported preview Ready | 6,417ms |
| Publish bounded sanitized source artifact | 116ms |
| Remix through supervisor-reported preview Ready | 4,010ms |
| Pause plus resume through preview Ready | 1,395ms |

Create and remix handler-only durations were 303 ms and 939 ms, respectively; these are **not** complete readiness times. These are single functional observations under nested-VM load, not a matched performance comparison or production speedup claim. The separate matched benchmark checks actual HTML and JavaScript delivery; this fixture's readiness assertion is the supervisor's health-checked PreviewReady.

Verified contracts:

- The source archive includes the allowed Markdown marker and excludes the synthetic secrets.json/local.db files and node_modules.
- A remix gets the source marker, a fresh runtime/credential identity and a healthy frontend. Private source data is absent.
- Pause/resume keeps the app and sandbox IDs.
- Owner restore replaces the sandbox while keeping the app ID and restoring the frozen source marker over a later owner edit.
- The final test's guest cleanup completed successfully. The temporary SQLite/key/artifact directories belong to Go's test fixture cleanup; no production database or project was used.

Evidence: `app-lifecycle-report.json`, `app-lifecycle-output.txt`, and the opt-in `cube_lifecycle_acceptance_test.go` (copy into a disposable control-plane/internal/api checkout to run with its shared test helpers). No deployment/network acceptance is asserted.

## Superseded fixture runs and unresolved env-edit behavior

The initial multi-guest fixture inherited `newConfigTestServer` using file::memory: while its SQL pool allowed multiple connections. Concurrent channel monitors could encounter an empty second database, producing `no such table: sandbox`. This invalidated that run's apparent source readiness failure and its cleanup report. The shared helper now uses a private disk database. Two exact leftover fixture guests were independently identified by their reviewed template and sandboxd IDs, deleted, and their absence confirmed with GET 404. Those failed cleanup flags are superseded by this correction; they must not be used as evidence.

A separate corrected-database run revealed a real app compatibility issue before pause: `client.PutFile(ctx, ".env.local", strings.NewReader("PRIVATE_FIXTURE=not-for-remix\n"))` made Vite log `.env.local changed, restarting server...`, then the source did not regain preview readiness during 90 s. The process remained running. The final basic lifecycle fixture uses secrets.json as its private-data sentinel, which does not trigger that reload. **This does not resolve or dismiss env-file editing**: AI-generated env edits are common. A separate same-image Docker/Cube probe is required to diagnose/fix this behavior before global application acceptance.

The isolated test did not set deployment security attestations, change direct guest networking, or touch production tenant files. HTTPS public/private UI links and external-client compatibility remain distinct acceptance checks.
