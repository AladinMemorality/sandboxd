# Direct project previews — 2026-09-28

Project traffic uses `s-<sandbox ID>-<web port>.baarcha.tn`. Cloudflare terminates TLS and connects a named Tunnel to the gateway on the assigned worker. The gateway requests authorization metadata from the controller and sends application bytes directly to that worker's Cube ingress. B200 responses no longer travel through the VPS private link.

## Security and lifecycle

Every request is authorized by the existing controller checks (visibility, owner signature, allowed app port, Origin, current runtime binding and lease). Worker-specific keys are required on the metadata endpoint. Application bodies never go through it. No permission cache is used. A Cloudflare cache-bypass rule for `s-*.baarcha.tn` prevents cached assets bypassing changed visibility. Signed private responses are no-store. Active streams reauthorize every 20 seconds; HMR remains passive. Platform cookies and routing credentials are stripped before reaching apps. Worker reassignment uses a bounded peer fallback until DNS catches up.

The gateway adds bounded modulepreload hints to Vite development HTML, resolving static imports locally on the worker. It does not bundle or cache project source. Only Vite-style no-cache HTML with Vary: Origin is examined; other responses stream normally. At most 64 local module paths, 12 parallel requests, and a 750 ms discovery budget are used. External/API paths are excluded.

## Installed services and configuration

Both workers have `/opt/baarcha-preview/{preview-gateway,cloudflared}` and root-only `/etc/baarcha-preview/{gateway.json,tunnel-token}`. Services: `baarcha-preview-gateway`, `baarcha-preview-tunnel`, `baarcha-preview-peer`. Gateway listens on loopback port 8095, peer transport on each WireGuard address port 18095. VPS authorization transport listens on 10.254.240.1:18094 and forwards to **127.0.0.1:9090** (not the public Traefik port 8091). VPS origin is 127.0.0.1:20080; B200 origin is 10.254.240.2:28080. VPS firewall restricts private ports to B200 over wg-cube-fleet.

Controller environment: `SANDBOXD_PREVIEW_PUBLIC_DOMAIN=baarcha.tn`, `SANDBOXD_PREVIEW_GATEWAY_KEYS` maps worker IDs to secret keys. Never commit keys/tunnel tokens. Private local credentials remain under `cube-migration/private`.

VPS `baarcha-preview-dns.timer` runs `ops/cube/preview-dns-sync.py` every 15 seconds using `/etc/baarcha-preview/dns.json`. It maintains only managed project CNAMEs based on current runtime placement. Wildcard fallback targets B200; specific VPS project records target VPS. Apex/mail/www DNS are unchanged.

Cloudflare account 5188e7d3ec966b4337fd639fa440de48, zone f5721c3f7714af007b17bba1eb8620b7. Tunnel IDs: B200 3985a79c-d111-40b5-8013-c3dbe9796b19; VPS 57f75b91-ce84-44f6-9c66-e93396130c17. Cache ruleset 36da6e04cb3641b89adf1fabce81b800. Free Website remains active; no paid feature enabled. Cloudflare has separate policies/services for video and large-file delivery.

## Deployment and verification

Production evidence, backups, source and controller image are under `/opt/baarcha-bench/direct-preview-20260928` on VPS. Image overlays live at `/opt/sandboxd/deploy-state/{runtime-compose.json,active-images.json}`. Keep `/etc/baarcha-cube/worker-stop.json` synchronized with the controller container ID. Replacing the controller also requires recreating its four management network-namespace sidecars. The first rollout was rolled back to correct this; the next succeeded. Runtime bindings were preserved.

Checks passed: gateway race tests (including WebSocket proxy and permission revocation), controller preview/auth tests, all 81 route access checks (72 public, 9 unsigned private denied), signed private access on both workers, wrong owner and supervisor-port denial. Access checks establish authorization, not full-page rendering for every project.

Brandish original VPS browser render times: 8.85, 8.00, 6.06 seconds. After direct routing and preloads, fresh-browser repeat tests were 3.54 and 3.70 seconds, but one cold run was 17.66 seconds. These measurements preceded disabling edge asset caching and are not final cold-load guarantees. A VPS project rendered in 0.726 seconds. Signed Olive preview on the Mac after cache bypass: 3.38 and 3.11 seconds (already awake; excludes parent page startup).

The user's actual Chrome trace and controller logs identified a separate 9.982-second sandbox resume. Two-minute automatic idle sleep made this frequent. Waking a fully suspended app can still take seconds; Cloudflare does not eliminate runtime startup. Idle policy remains unchanged at 120 seconds. A proposed longer warm-retention policy was not deployed: user correctly requested diagnosis of native Cube resume versus platform startup overhead first.

## Operational rollback

For gateway-only changes install the previous binary atomically and restart the gateway, leaving projects untouched. For controller rollback restore the recorded Compose environment/image under all four operator locks, recreate the controller and its four management sidecars, wait for `/readyz`, update the controller stop pin, and verify bindings. DNS can remain on the new gateway only while the controller supports `/preview-gateway`; restore prior preview URL configuration and routing together if reverting the feature. Do not run the historical deployment script against a different baseline.

## Cold-start trace (Olive, 15:09 UTC)

Controller image with phase timing: `sha256:69a0d006828f150a85d4ed981a9a0ff36e38449c7eb62f9b85901ca2bc27677d`; container `5c6940b5c5f663651a29c6cba9c3098523c56fc0f21ce70a0e7f21cb8c699d86`. Rollout receipt/backups: `timing/` under the evidence root. All 81 bindings retained; readyz healthy.

Actual native CubeShim restore: 96 ms (15:09:35.830684836–15:09:35.927040501). Whole controller start: 7,588 ms. Provider connect block 4,666 ms: pre-connect observation 656 ms, admission 43 ms, connect HTTP call 3,308 ms, post-connect observation 323 ms, placement/finish 333 ms. Supervisor readiness 1,638 ms; config sync 0 ms; network channel/local bookkeeping 644 ms; final response readiness probe approximately 637 ms. Signed browser preview POST took 8,100.6 ms including parent API/network overhead.

These establish that native VM restore is fast and orchestration/network checks dominate. The 3.3-second connect HTTP call includes CubeAPI/CubeMaster work and is not the native VM resume duration. The cold-start issue remains unresolved; do not label the gateway rollout a complete latency fix. No idle-policy change was deployed.

CubeMaster logs for this same trace contain six serial `GetSandboxInfo` requests for Olive at 15:09:34.530, 34.861, 35.230, 37.339, 38.539 and 38.862. Native restore finished at 35.927. Proxy map refresh was logged at 36.696, two-replica proxy cache invalidation completed at 37.010, and the resume update completed at 37.339. This localizes significant delay to repeated distributed observation and routing bookkeeping after native restore. Config synchronization was zero milliseconds. Follow-up should reduce redundant observations and reuse current-request readiness while retaining placement, admission and permission guarantees; it must be measured again before claiming a fixed cold start.
