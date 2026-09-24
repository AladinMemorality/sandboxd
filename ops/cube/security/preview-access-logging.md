# Preview capability access logs

The preview handoff carries a five-minute signed capability in
`/__sandboxd/preview-auth?token=...`. The application clears the query from its
request object, adds `Cache-Control: no-store` and `Referrer-Policy: no-referrer`,
and redirects to a clean relative path. That cannot redact a URL already logged
by an outer proxy.

The checked-in `traefik/traefik.yml` drops the `RequestPath` access-log field,
including any capability query, while retaining `RequestHost` for the idle
activity tailer. Request headers default to `drop`, so authorization/cookies are
not recorded. This affects request-path diagnostics on this sandboxd Traefik;
host, status, timing and activity tracking remain available.

The configuration regression passed:

```sh
bash scripts/check-cube-migration.sh \
  -run TestPreviewCapabilityQueryExcludedFromTraefikAccessLog \
  -timeout 90s ./internal/api
```

This is a checked-in configuration test, not proof that an existing deployment
has reloaded it. Before production use, send a request with a **non-secret test
sentinel** in the handoff query, then inspect every active access-log sink and
error-log sink. Confirm no sentinel, request URI, authorization or platform
cookie is retained, while the request host remains available for idle tracking.
Use an invalid sentinel token; do not paste a real owner capability into logs or
shell history. Repeat through the full browser-facing proxy chain.

The landing repository contains `deploy/baarcha-response.caddy`, a response-cache
header snippet, and documentation referencing `/etc/caddy/Caddyfile`; it does
not contain the complete deployed Caddy access-log configuration. This review
therefore cannot certify the outer Caddy logging behavior. Its preview-host
access logger must omit capability-bearing URIs (or exclude the handoff route)
and sensitive headers before enabling handoff URLs through it. Audit any CDN,
load balancer, analytics and tracing collectors as well. No external Caddy or
production logging configuration was changed by this branch.

## Installed-version isolated checks — 24 September 2026

The actual VPS Caddy2.6.2 binary passed
`CUBE_CADDY_LOG_TEST=1 python3 test_caddy_preview_logs.py` in a separate
loopback-only process with its admin API disabled. An intentionally unavailable
local upstream produced both HTTP access and error entries. The
`caddy-preview-log-filter.caddy` stanza removed the non-secret query, cookie,
authorization and referrer sentinels from both kinds of entry and preserved the
request host. It deliberately omits request URIs/headers and response headers.
Merge this stanza into the deployment's global block; do not replace the complete
Caddyfile with the snippet. No production Caddy configuration was changed.

The installed Traefik image also passed the isolated alias/proxy fixture with
RequestPath dropped and request headers dropped. Non-secret query/cookie
sentinels were absent, while RequestHost remained available. See
[alias acceptance](../preview-alias.md). The inspected **running production**
Traefik configuration still lacks RequestPath dropping; deploying the branch's
static configuration and accepting the entire HTTPS chain remain cutover gates.

## Production activation — 24 September 2026

The outer Caddy global filter and Traefik static redaction are now deployed.
Before activation, adapted Caddy JSON was compared with the running file and
only logging differed. Caddy validation passed. The existing Traefik image
(`sha256:ef751c695afd26e2be41009047f651153dc1e61ace03e6d13f299d8c4be842b8`)
was recreated with only the reviewed path-redaction configuration change.
Platform and MyHomeTroc HTTPS requests returned200 after activation.

The [production probe](results/2026-09-24-production-log-redaction.json) sent
invalid, non-secret query, authorization, cookie and referrer sentinels through
the public HTTPS MyHomeTroc preview endpoint. The corresponding Traefik access
entry retained its host, omitted its path, and none of the four sentinels
appeared in new access entries, Traefik runtime logs or Caddy journal entries.
Caddy access logging is not enabled; its error filter is active. The earlier
isolated error fixture covers filter behavior on an upstream failure; this
production probe did not induce an outage. External CDN logging configuration
has not been attested by this origin-side check.

The first automated post-change public-platform request used Python urllib and
received403; its guard restored Caddy's previous file. Public curl and direct
TLS-origin checks returned200. The validated filter was then reapplied and the
sentinel probe passed. The private backup and complete activation record remain
under `/opt/baarcha-release/20260924T200338Z/preview-log-cutover`.

Caddy documents [filtering nested log fields](https://caddyserver.com/docs/caddyfile/directives/log#filter)
and [default runtime log configuration](https://caddyserver.com/docs/caddyfile/options#log).
