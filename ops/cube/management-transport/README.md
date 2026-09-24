# Private Cube management transport

This transport connects the trusted sandboxd controller namespace to the Cube
VM's fixed outer-host loopback forwards. It adds no outer-host TCP listener.
It is separate from guest egress and does not grant isolation acceptance or
production rollout permission.

```
sandboxd network namespace (synthetic controller during staging)
  127.0.0.1:20300 -> socat sidecar -> /run/cube-management/api/channel.sock
  127.0.0.1:20080 -> socat sidecar -> /run/cube-management/proxy/channel.sock
                                      | persistent unprivileged socat
                                      v
                           fixed host127.0.0.1:20300 / :20080
                                      | reviewed VM forwards
                                      v
                                   Cube VM
```

HTTP bytes, Cube API keys, Host routing and WebSocket upgrades pass unchanged.
The relays neither parse credentials nor select an upstream from request data.
Only two trusted sidecars mount this dedicated socket directory, read-only; the
controller itself needs no socket mount. Guests, Traefik, applications and capture
workers receive none. The controller already has Docker authority: this does
not protect against a compromised controller. It prevents network-only access
from sibling containers. VM forwards must bind exactly to outer127.0.0.1.

## Permissions and process boundary

The host parent directory is root:root0755. Per-endpoint directories are created
by systemd `RuntimeDirectory` as `cube-management-relay:cube-management`0750;
sockets are owned by the same unprivileged service UID/group with mode0660.
The sidecars run UID/GID65532 plus the numeric dedicated socket group. They may
traverse and connect, but cannot create, unlink or replace sockets because they
lack directory write permission and their mounts are read-only. No root listener
or privilege-dropping relay is required.

Use a dedicated static system account `cube-management-relay`, no login shell,
no home, primary group `cube-management`; no tenant/human is added to the group.
Host services have an empty capability set, no-new-privileges, read-only system
filesystem, protected homes and localhost-only IP policy. Their writable runtime
directories are the explicit exception created by systemd. Each service owns one
socket path. Systemd stops the complete process group before restart, and its
per-service runtime directory is cleaned on stop. `unlink-early` only removes the
exact socket path inside that service-owned directory; never run a second manual
listener against that path.

The sidecar image pins Debian's maintained socat1.7.4.4-2 package and requires a
reviewed immutable Debian bookworm-slim digest. The host uses the maintained
Ubuntu security package socat1.8.0.0-4ubuntu0.1, installed without upgrading or
removing other packages. Review future security updates explicitly. References:
[Debian package](https://packages.debian.org/bookworm/socat),
[socat manual](https://manpages.debian.org/bookworm/socat/socat.1.en.html).
No `-v`/`-x` payload logging, shell address evaluation or automatic proxy/DNS
selection is enabled.

## Bounds, failures and readiness

Each persistent host relay and sidecar admits at most256 connections, backlog32.
Each socat child transfers bounded8 KiB blocks with socket backpressure. Per
sidecar:192 MiB,0.5 CPU,270 PIDs. Per host service:128 MiB,0.5 CPU,270 tasks,
1100 descriptors. The host aggregate slice caps256 MiB,1 CPU and550 tasks.
Long-lived reverse channels consume slots. These are ceilings, not measured
fleet capacity; saturation may queue/fail and must not trigger an automatic
restart of healthy busy relays.

Connect timeout5 seconds, inactivity timeout300 seconds, half-close drain30
seconds. Heartbeats keep active reverse channels alive. FIN on one side preserves
the opposite direction while draining; normal HTTP request framing does not
half-close TCP. Requests deliberately closing their TCP write side then waiting
more than30 seconds need separate review. A broken connection is never replayed;
only a new connection dials the current fixed socket/upstream. Caller idempotency
and reconnect logic remain authoritative.

The sidecar healthcheck makes a tiny unauthenticated `/healthz` request through
both relays, bounds captured output to1024 bytes and logs no body or credential.
It proves HTTP transport only; follow it with the controller's authenticated
Cube API and private-preview readiness checks. A listening Unix socket or a
transport404 is not application readiness.

## Installation and controller wiring

All deployment steps require the coordinating operator's reviewed cutover:

1. Verify VM identity and loopback forwards20300/20080. This directory does not
   create VM forwards, public listeners, firewall rules or guest allowances.
2. Refuse unexpected existing group/account/unit/socket-directory state. Install
   the dedicated account/group, tmpfiles fragment to
   `/etc/tmpfiles.d/cube-management.conf`, and the two service units plus slice
   to `/etc/systemd/system/`. Verify with `systemd-analyze verify`; apply tmpfiles
   and daemon-reload. Keep the production services disabled/stopped until cutover.
   Before replacing older candidate artifacts, verify their exact owned bytes
   and inactive state; never overwrite an unrelated unit.
3. Build with `DEBIAN_IMAGE=debian:bookworm-slim@sha256:<reviewed64hex>`, record the
   resulting content hash and package version, and configure an immutable relay
   image reference. Set `CUBE_MANAGEMENT_GID` to the real dedicated host GID.
   Root-owned parent0755 and service-owned child0750 must be real directories,
   not symlinks. Container bind uses `create_host_path:false`.
4. Merge `compose.override.yml` after repository `docker-compose.yml`. Only Cube
   API/proxy origins change; API keys, domain, template mappings and security
   gates remain independently configured. No credential belongs in sidecar
   environment, arguments, image, healthcheck or logs. Host userns is intentional
   so the supplementary GID matches the host socket group.
5. Recreate both sidecars whenever sandboxd is recreated. They join its namespace
   at creation and cannot follow a replacement container. Use one Compose
   operation with `--force-recreate sandboxd cube-management-api
   cube-management-proxy`; `depends_on.restart:true` assists Compose updates but
   does not cover unrelated deployment automation. Check all three PIDs share
   `/proc/PID/ns/net`, then repeat authenticated readiness. Never publish the
   sidecar ports or give the controller host networking.
6. To stop management transport, stop both host services and both sidecars,
   confirming all child processes and sockets are gone. Rollback must not silently
   substitute a public management endpoint. Missing socket, wrong group and
   upstream outage fail closed.

## Tests and rejected alternatives

`SOCAT_BINARY=/path/to/socat python3 test_transport.py -v` uses temporary Unix
sockets and ephemeral127.0.0.1 listeners only. It covers preserved Host/auth bytes,
upgrade duplex, delayed half-close, upstream replacement/reconnect, fixed wiring
and actual Linux nonroot/group access. It creates no Cube guests or network
policy. The standalone `stage_client.go` verifies both endpoints inside a
synthetic controller; its bounded benchmark makes50 serial fresh connections at
no more than four starts/second, paired with50 direct host requests.

The initial `systemd-socket-proxyd` option failed a real systemd255 half-close
fixture: a delayed response was discarded after the client shut down writing.
Its rejection is retained in `results/proxyd-rejected-test-output.txt`.
An `Accept=yes` per-connection systemd+socat alternative passed stream checks but
added median326.733ms/p95 342.456ms versus direct1.062ms/1.172ms, so it was also
rejected. Final artifacts use persistent unprivileged host processes instead.

An initial recreation assertion assumed a freed network-namespace inode could
never be reused; both before/after functional smokes passed but that assertion
failed. The corrected staging fixture retains an FD to the old namespace while
creating the new one, and verifies all new sidecars share the distinct namespace.
This is a fixture correction, not a transport failure.

The final persistent stage passed actual service sandboxing, socket DAC,
read-only sidecar mounts, both endpoint protocols and controller/sidecar
namespace recreation. Fifty direct requests measured median1.061ms/p951.149ms;
fifty two-leg requests measured2.304ms/2.458ms, an added median1.243ms.
All100 requests used fresh connections, serial at at most four starts/second.
See `results/persistent-staging-result.json`. Owned fixture containers,
listeners and units were removed; final production services are installed but
inactive and disabled. The image content ID is
`sha256:f3858abc38b80bc028ab947b3812bd76b584e08663521e41978e03a8d01ad092`;
it is a local immutable image ID, **not** a registry manifest RepoDigest.
Use that exact local ID with pull disabled, or a separately published and
verified registry digest. The host service UID is987 and socket group GID982.

The actual shutdown showed socat exits143 on SIGTERM. Final units accept that
normal exit status, preventing a clean stop from being marked failed. This
diagnostic-only unit setting followed the successful protocol/timing run;
the fixture's retained failed-state entries were explicitly cleared.

Staging must retain exact image/package/unit hashes, socket owner/group/modes,
namespace/recreation results, latency samples and cleanup counts. Synthetic
transport latency excludes real Cube application startup and does not prove
production isolation or fleet concurrency. No packet-isolation test is included.

## Fresh-worker boundary acceptance

The real fresh-worker API exposed a limit absent from the synthetic transport
fixture: QEMU user networking drops the response when a client sends an early
TCP write half-close. A direct socket to the host forward reproduced this
without either relay: an ordinary request returned401; the same request followed
by `SHUT_WR` returned no HTTP response. The relay half-close test remains valid
for the relay itself, not for the complete QEMU path. Ordinary HTTP clients read
the response before closing and are unaffected.

The health probe now uses socat `STDIO,ignoreeof` to keep its request side open,
with the same finite inactivity limit. The corrected immutable relay image is
`sha256:0b0c246afeb8d343d25890bfe62d5f8af8aee528cb034a596733c3b338408327`.
This changes only healthcheck behavior; the relay command, package and unit
limits are unchanged. `accept-fresh-api.py` uses a separate controller namespace
with no network interfaces beyond loopback, the real private sockets, and the
actual fresh Cube API. Credentials travel through stdin only; response bodies
are discarded. It verifies anonymous rejection, authenticated200, both actual
sidecar health checks, no published ports and exact namespace sharing, then
removes its containers and restores prior service activation. It never creates
or changes a Cube guest or the production controller.

### Actual Cube endpoint half-close limitation

A later direct check of the installed worker endpoint found that QEMU SLIRP
returns EOF without the HTTP response if the client sends the request and then
immediately calls `SHUT_WR`; the same ordinary request without an early
half-close returns the expected HTTP401. The synthetic fixture proves the two
socat legs preserve half-close behavior against its owned listener. It does
**not** establish half-close correctness through QEMU SLIRP or the full Cube API
path. Production clients use ordinary HTTP. The health probe must retain its
write side until the response completes, with a finite timeout; root is updating
and testing that probe against the actual endpoint. No broader full-path
half-close claim should be made from the synthetic transport results.
