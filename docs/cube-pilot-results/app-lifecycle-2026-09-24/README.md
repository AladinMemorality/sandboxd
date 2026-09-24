# Matched application lifecycle pilot, 2026-09-24

This measures application runtime operations, not screenshots. No production project, model provider, network policy, or production deployment was changed.

One labelled warm-up followed by three sequential measured repetitions completed for each accepted action. The same clean control-plane source (`2023423c972b3b8c281a9aa53d124c9bdab3b7f7`) and operator harness drove both arms. Source was staged using `git archive HEAD control-plane`, excluding every uncommitted shared-checkout change. The sole source overlay was the operator test fixture. Each app had 1 CPU and 1024 MiB. Every owned sandbox was deleted; the dedicated Docker internal bridge had zero attached containers and was removed.

## Results

Values are medians in seconds; parentheses contain the three-sample range. Raw warm-ups and samples are retained beside this file.

| Operation | Docker | Cube |
| --- | ---: | ---: |
| Create → direct preview HTML + assets ready | 6.616 (6.522–7.162) | 4.511 (4.449–4.682) |
| Resume → direct preview HTML + assets ready | 1.966 (1.700–2.039) | 1.149 (1.131–1.270) |
| Stop + resume → direct preview HTML + assets ready | 4.434 (4.290–4.443) | 1.345 (1.319–1.445) |
| Publish + source directly available again | 3.581 (3.190–4.000) | 0.555 (0.264–0.861) |
| Publish handler alone | 0.290 (0.213–0.356) | 0.117 (0.115–0.122) |
| Remix → direct preview HTML + assets ready | Not comparable in this fixture | 4.891 (4.713–5.134) |

Cube reduced the sampled median create-ready time by about 32%, resume-ready by 42%, and publish/source-available time by 85%. These are small-sample observations under the topology and dependency preparation below, not production capacity or universal speedup estimates.

Cube's create API returned in a median 0.306 seconds, but its app assets were ready at 4.511 seconds. Reporting only the API or VM-restore response would substantially understate the user-facing work remaining.

## What was measured

The harness invokes real control-plane handlers using a private disk-backed SQLite store and synthetic owner identities. Readiness requires successful, nonempty and content-validated responses for `/`, `/src/main.tsx`, and `/@vite/client`. Cube requests pass through the authenticated preview proxy; Docker probes the disposable container's bridge address. Remix also verifies the harmless source marker through the control-plane file API.

These checks verify HTML and representative transformed JavaScript, **not** the full browser module graph, rendered UI, platform wake polling, or supervisor `PreviewReady`. The supervisor's three-second probe interval can add an independent platform-visible delay. Handler duration and subsequent direct readiness are recorded separately.

Docker publishing follows the actual stop → snapshot → restart path. Cube publishing exports sanitized source while the app remains running. `publish_source_ready_ms` includes the relevant stop/restart and the subsequent readiness check; it is not a continuous downtime measurement.

## Topology and dependencies

- Docker ran on the outer VPS using the pinned production base image `sha256:9ec445151344a7baa295b085bebe1a0352ab930601fe0d9ed2bf1283bbaa6678`, including normal cold workspace seeding and startup.
- Cube ran inside the existing disposable 4-vCPU/12-GiB benchmark VM, adding another virtualization layer. Template `tpl-ce9efc43b71248d9a0adfb90` uses reviewed React Pro v3 image `sha256:6bad30fa19584d85f0dafc1680bca5851f1bb05f55a6abe0d6002165227253d0`, derived from the same production ABI with its dependencies prepared in the template.
- These are the intended product paths, not identical cache states or physical placement. The VPS continued hosting unrelated production activity; other planned agent benchmark loads were held during accepted timing runs.
- Docker used a dedicated internal bridge, and its one-shot trusted seed container used `--network none`. Cube retained deny-all direct egress and a protected-address deny-all fixture broker. No network exceptions were added.
- Docker snapshots intentionally omit `node_modules`; the remix startup attempted to reinstall 202 packages, reused zero cached packages, and failed registry DNS access in this offline fixture. Production Docker normally has registry access. The failed warm-up was retained, further Docker remix attempts were explicitly skipped, and **no Docker-versus-Cube remix speedup is claimed**.

## Separate edit checks

After the timing window, a fresh app on each provider received an actual `src/App.tsx` edit through the normal file API. The transformed asset included the new marker on both providers (111 ms Docker; 185 ms Cube). These were functional checks with other test activity allowed, not controlled comparative latency results or browser HMR-render tests.

Both providers then received `.env.local` containing a harmless public Vite variable. Neither satisfied the composite HTML plus transformed new-variable readiness check within the bounded 90-second check. The executed harness did not retain the final failing URL/status or distinguish unavailable HTTP from missing transformed content; the raw results alone cannot establish that every HTTP request was unavailable. Docker's app log showed successful App.tsx HMR followed by `.env.local changed, restarting server...` with no completion; this matches the separate Cube functional diagnostic. The shared environment-reload failure remains unresolved and must not be presented as successful edit compatibility or as a Cube-specific defect. Both failed probes cleaned up their owned guests. The committed harness now retains bounded last-failure path, status, byte count and failure type without response bodies or credentials; that diagnostics-only enhancement was compiled but these probes were not rerun.

## Retained unsuccessful setup attempts

- `report-docker-fixture-long-socket.json`: original deep staging path made the host Unix socket unusable during a direct guest file call. The fixture work root was shortened. Earlier successful readiness samples are retained but excluded from medians.
- `report-docker-fixture-legacy-files.json`: the pinned production guest lacks the new guest `PUT /files` endpoint. The harness was corrected to use the existing control-plane file API on both providers. This was a harness mismatch, not a production file-edit failure.
- `report-cube-fixture-global-gate.json`: the fixture initially requested `CubeAllApps`; startup correctly rejected the fleet-wide reverse-egress gate. It now allowlists only each synthetic fixture app. No runtime gate was bypassed or weakened.
- `report-docker-offline-remix.json`: the bounded offline registry failure described above.

The accepted raw files are `report-docker.json` and `report-cube.json`. Separate edit checks are `report-docker-edit.json` and `report-cube-edit.json`.

## Reproduction provenance

- Clean archived source SHA256: `57de2dcbe40692ec3c1853c655ed8f91cf863700e3e48679d7a1da0f3029f75d`.
- Executed Cube/edit fixture SHA256: `69370c51356ffe3b9c7bb339432f2afa4146fb9ef1cdadfbb0b8f53ebb14726f`.
- Executed Cube/edit Linux binary SHA256: `4226a39f70e8a4300c37c96239a62b3b79a2bf48f6a08200fa65adebb507a36b`.
- Docker's accepted baseline preceded the fixture's Cube-only global-flag→per-app-allowlist correction. That branch is unused by Docker; no runtime or Docker-path code changed. Its earlier binary hash was not retained and is not represented by the Cube/edit binary hash above.
- The archive, executed fixture and binary remain in the private operator stage `/opt/baarcha-bench/cube-global-20260924/app-lifecycle`; guest fixture stage is `/root/app-lifecycle-20260924` inside the disposable VM.

The final committed fixture adds bounded failure diagnostics after these runs. It is an improved reproducer, not byte-identical to the executed fixture identified above.
