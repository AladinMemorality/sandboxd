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
