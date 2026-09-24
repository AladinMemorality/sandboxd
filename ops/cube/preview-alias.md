# Preserve the MyHomeTroc customer origin

The deployed `myhometroc.yml` alias points directly at a Docker service. It must
not remain on that service after Cube cutover. The canonical Baarcha app URL and
`MHT_PUBLIC_ORIGIN` stay unchanged; customer login fragments and WebSocket paths
must not be redirected to a different origin.

`render-preview-alias.py` reads sandbox metadata without changing it and emits
JSON (also valid YAML) for Traefik. It routes the exact reviewed hostname to
`sandbox-wake@file`, rewriting the upstream Host to the canonical sandbox web
preview. The wake handler selects the actual provider and performs current
visibility checks. This is for runtime-public apps with their own application
authentication, including the unlisted MyHomeTroc app. Private aliases require
separate owner-handoff/origin support and are rejected by the renderer. A later
visibility change must never bypass the control plane's authentication.

Example, to a private staging file **outside Traefik's watched directory**:

```sh
umask 077
python3 ops/cube/render-preview-alias.py \
  --database /var/lib/sandboxd/state/sandboxd.db \
  --sandbox 01M37PPK85JN1K0WEMP4ZYER6C \
  --alias myhometroc-tn.preview.65.108.225.153.sslip.io \
  --preview-domain 65.108.225.153.sslip.io > /PRIVATE/myhometroc.yml
```

Before installation, verify this exact generated configuration using the
deployed Traefik version against an isolated HTTP/WebSocket app and both runtime
providers. Check path/query preservation, browser auth, idle wake and that the
original alias remains the browser origin. Retain the existing alias file for
rollback and replace it atomically only with the accepted deployment. Do not
launch another live paired WhatsApp connection for this check.

Local renderer validation is not deployed routing acceptance. Production HTTPS
alias and cookie checks still require access to the VPS and the complete edge
proxy chain. The middleware uses Traefik's documented
[custom request headers](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/headers/).

## Isolated deployed-version proxy check — 24 September

`functional/2026-09-24/preview-alias-proxy.py` passed against the exact installed
Traefik image `sha256:ef751c695afd26e2be41009047f651153dc1e61ace03e6d13f299d8c4be842b8`.
Two disposable resource-limited containers shared a network-none namespace,
published no ports, and were deleted afterward. The fixture verified the
canonical upstream Host, HTTP method, encoded path/query, body and synthetic
application cookie, rejected an unknown alias, and completed a WebSocket
upgrade with an application frame. A non-secret capability query and synthetic
application cookie were absent from Traefik access logs, while RequestHost was
retained. Spoofed forwarded-host input did not alter
the configured target. The backend was a synthetic HTTP server: this is proxy
rewriting acceptance, not a real Cube app, TLS or production cutover check.

The production metadata currently reports MyHomeTroc runtime visibility
`public`, with web port3000; its platform listing remains unlisted. The old
Docker alias has **not** been replaced on production.
