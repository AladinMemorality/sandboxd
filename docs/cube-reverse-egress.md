# Cube reverse egress transport

This is an opt-in implementation under `control-plane/internal/egress`. It does
not open the guest NIC, change the worker's deny-all policy, or replace the
outstanding worker isolation attestation. The packet-test restriction described
in `ops/cube/security/results/2026-09-23/HANDOFF.md` remains in effect. The tests
below are ordinary localhost HTTP/WebSocket fixtures; no production network
policy or project was changed.

## Trust and routing

The control plane opens `/egress/channel` on the existing authenticated runtimed
control endpoint. The runtime supervisor bearer is required before WebSocket
upgrade or replacement; browser Origins are rejected. Host code supplies an
immutable sandbox ID and fresh binding/credential generation. Neither identity
nor host credentials are accepted in guest protocol frames. The owner must
cancel the host session when its persisted binding, credentials or lifecycle
changes. An authenticated replacement closes the old connection and all streams.

Tenant processes use a separate loopback HTTP proxy, normally
`http://127.0.0.1:3032`. The runtime mux reserves:

| Guest request | Host operation |
| --- | --- |
| Absolute HTTP URL or CONNECT host:port | Resolve and pin an approved public IPv4 destination |
| `/__cube/model/v1/cube-model/{sandboxID}/{taskID}/v1/messages` (also `/count_tokens`) | Fixed model handler; POST only, no query except literal `beta=true` |
| `/__cube/bridge` | Rewrite exactly to `/api/bridge`; fixed bridge handler, POST only, no query |

The host revalidates canonical model paths against the **host** sandbox ID.
Callbacks additionally validate the live task and task-scoped capability, and
perform metering/authorization through the existing platform routes. The model
callback retains `X-Api-Key` and `X-Baarcha-Bridge`; the bridge retains its bearer
`Authorization`. Cookie, forwarding, proxy authorization, and arbitrary guest
headers are not passed to fixed services. Missing callbacks fail closed.

There is no opaque CONNECT exception to a management IP, platform hostname, or
model/bridge origin. Fixed callbacks alone can reach their configured service.
They cannot select a URL, path or method from arbitrary tenant input. Platform
provider fallback, including OpenRouter, happens behind the existing host model
relay, not by distributing its credentials to guests.

## Public destination policy

A nonempty explicit management-address inventory is mandatory at startup. The
host integration must include all public management/worker addresses, local
interfaces, and the management/proxy/relay/bridge domain inventory. Empty or
invalid policy is an error. Inventory completeness is an operator responsibility;
these checks cannot discover another machine's management addresses.

Only IPv4 destinations are dialed. DNS uses A lookups (`ip4`); **all returned A
records** must pass before a numeric address is selected. No second lookup occurs
at dial time. Private, loopback, link-local/metadata, CGNAT, multicast, reserved,
documentation/benchmark networks, and explicit operator prefixes are denied.
Protected domain names and their subdomains are denied. IPv6/mapped literals are
unsupported; AAAA records are not used. A fresh stream repeats authorization,
so a later DNS change cannot inherit a previous authorization. Default public
ports are TCP 80 and 443; additional ports require explicit host configuration.

This is first-hop network isolation, not a content firewall or DLP service. A
public server can relay application traffic elsewhere, and shared public CDN
addresses cannot be attributed exclusively to one hostname by an opaque CONNECT
tunnel. No host or model capability is injected into public streams. If policy
requires blocking every application-layer route to a platform/CDN name, CONNECT
alone cannot prove that property; use a separately reviewed L7 policy instead.

## Bounds and lifecycle

The channel admits at most 32 streams and at most 32 still-running host handlers.
Each direction has a fixed 64 KiB receive ring per stream, totaling at most 2 MiB
of queued payload per endpoint. This is the payload-window bound, not the total
process heap: sockets, framing, TLS/client buffers, headers and Go state add
bounded overhead. Senders receive credit only as the peer consumes bytes. One
slow reader cannot allocate a growing fragment list or block the shared reader.

JSON frames are limited to 32 KiB; data chunks to 16 KiB. Unknown fields, duplicate
stream IDs, invalid credit, wrong-direction opens, and malformed protocol revoke
the connection. Cancellation wakes blocked readers/writers and closes upstream
sockets. Short requests close their slot after draining the response; they do
not retain slots until the lifetime deadline. A separate host-handler admission
bound prevents rapid open/cancel churn from accumulating callbacks.

Dial/DNS work has a 10 s deadline, guest channel opens 12 s, writes 10 s, streams 15 min.
Ping/pong liveness uses 30 s heartbeats and 90 s read deadlines. Fixed request bodies
are capped at 16 MiB; request and response header blocks at 16 KiB. Responses and SSE
are streamed, not aggregated. Early rejected uploads are interrupted and joined;
HTTP/1 full-duplex mode prevents the local server from waiting for a rejected
upload before returning its error. Fixed callbacks must honor request context;
RunHost joins them during shutdown.

## Client compatibility

Set `HTTP_PROXY`, `HTTPS_PROXY` and their lowercase variants to the loopback proxy.
Set both `NO_PROXY` variants for `localhost,127.0.0.1,::1`, including the fixed
model/bridge endpoints. These are routing hints; deny-all NIC policy remains the
actual direct-network boundary.

The local suite exercises actual curl and `npm ping` plus Go HTTP transport
through HTTP and TLS CONNECT. It also exercises streaming fixed callbacks. This
proves that proxy-aware clients can use the transport; it does not yet prove a
real Claude task/metering/bridge flow through a deployed Cube guest.

Native Node 22.21.0 was tested with `NODE_USE_ENV_PROXY=1`: `fetch`, default
`node:http` and `node:https` agents use the broker, including verified TLS CONNECT;
localhost `NO_PROXY` bypass still works. The runtime injects this opt-in, and enabled
Cube images require Node 22.21 or newer. This is native Node support, without an
additional Undici preload. See the [Node 22.21 release notes](https://github.com/nodejs/nodejs.org/blob/main/apps/site/pages/en/blog/release/v22.21.0.md).

Custom agents/dispatchers, Bun, arbitrary SDKs, raw database drivers, UDP and
clients that ignore proxy configuration are not transparently supported. Database
access requires a reviewed connector/proxy. Plain HTTP Upgrade is rejected;
HTTPS/WSS can use CONNECT when the client supports it. Streaming connections are
bounded by the 15 min stream lifetime. Do not claim arbitrary backend compatibility
or enable global migration from these unit tests alone.

## Local verification and remaining integration

Run from `control-plane`:

```sh
go test -race -timeout 60s ./internal/egress
```

The suite uses local HTTP/TLS fixtures behind a test-only trusted dial hook. DNS
returns a synthetic public address, while the hook asserts that exact numeric
address and connects to the fixture. Production does not configure this hook;
it never adds a loopback allow rule. Tests cover public/private/mixed DNS policy,
rebinding, curl/npm, repeated HTTP and CONNECT, concurrent opens, canonical fixed
services/capabilities/source identity, streaming/cancellation, session replacement,
capacity, duplicate/malformed frames, fixed-window backpressure, and an incomplete
upload rejected before its body arrives.

Still required before enabling this path: runtime/server integration tests,
startup/reconnect/pause/resume binding checks, packaged-template proxy environment
verification, a real authorized Claude request with metering and bridge callbacks,
representative package installs and backend client compatibility, protected-address
inventory review, and the existing separately required worker isolation acceptance.
These are not implicitly satisfied by successful localhost transport fixtures.

## Startup configuration and explicit pilot containment

`SANDBOXD_CUBE_REVERSE_EGRESS=true` enables the host manager before reconciliation.
It requires Cube enabled, the existing model relay and network acceptance flag,
a fixed HTTPS `SANDBOXD_CUBE_BRIDGE_URL` ending exactly in `/api/bridge`, nonempty
comma-separated `SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS` and
`SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS`, and the explicit pilot capability profile
`SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE=proxy-http-v1`. The manager adds its local IPv4
addresses and configured service names to the protected inventory. The guest
image must be built with `CUBE_REVERSE_EGRESS=1`; two-field instance bootstrap is
unchanged. Image-default and host-default are disabled.

Global reverse-egress startup is rejected while fleet client compatibility is
unverified; the existing pilot app allowlist remains required. The profile is an
acknowledgment of supported clients, not a bypass for security acceptance. Direct
allow-domain flags remain rejected. Supervised app/worker startup waits for the
first authenticated host channel, while the control listener stays available.

The host attaches on creation/wake/reconciliation, reconnects without waking a
stopped VM, and revokes on stop/deletion/binding or credential changes. Fixed
callbacks check the current generation again on each request. Bridge requests,
like model streams, are cancelled when their durable task scope disappears.

`python3 scripts/inventory-cube-egress-clients.py` performs a bounded, read-only
capability inventory against the local fleet database/workspaces. It emits only
allowlisted package/capability names, counts and sandbox IDs, never source,
commands, URLs or environment values. It follows no tenant symlinks and marks
limits/skipped paths incomplete. Static absence is not runtime compatibility.

## Validation on 2026-09-24

The affected Linux packages passed with `go test -race` in a disposable container
with no network, 2 CPUs and 2 GiB memory: `internal/egress`, `internal/runtime`,
`internal/api`, `cmd/runtimed`, and `cmd/sandboxd`. API tests include actual
host-manager/guest-channel fixtures for model metering headers, project-scoped
bridge authorization, task-finish revocation, credential rotation and stop/resume.
The complete API package passed in 135.214 s. Its first full run lacked the docs
and Traefik fixture mounts; those test-environment omissions were corrected.
A bridge cancellation test also caught premature connection-close semantics;
the fixed upstream transport now preserves normal HTTP cancellation behavior.

The exact native Node 22.21.0 fixture passed HTTP/HTTPS `fetch`, default agents,
verified TLS CONNECT and local bypass under the race-tested transport. The
portable package including those clients passed in 6.634 s. Three Python tests
cover the capability scanner's secret-free output, symlink exclusion and file
size bound. No tenant code was executed by the inventory.

The [fleet capability report](cube-migration/egress-clients-2026-09-24.json) covers
59 observed sandboxes, including the explicitly excluded project still being
built. Its PostgreSQL dependency belongs to that excluded project. The eligible
fleet's detected raw socket call connects to its own guest loopback. All four
initial scan limits were accounted for using reviewed data/runtime exclusions
and separate bounded scans of the actual deeper source subtrees; the original
scan limitations remain visible in the evidence. This is static evidence, not
proof of Java/custom SDK behavior or full application flows.

The feature remains opt-in and global reverse-egress admission remains disabled.
No production service, project, firewall, domain allowance, or worker policy was
changed by this implementation or its localhost tests.
