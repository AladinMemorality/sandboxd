# Experimental pristine capture VM

This template is separate from application sandboxes. Build the reviewed Node /
Playwright-only worker image from the platform first, then this Dockerfile from
the sandboxd repository root with `CAPTURE_WORKER_IMAGE` pinned to its image digest.
Use one CPU and 768 MiB guest memory; the host factory checks both before admission.
Kernel/supervisor/browser memory must fit that guest bound; the image still needs
a real Cube restore/load pilot. This is not a production-enabled backend.

`capture-init` drops to UID/GID 1000, disables privilege gain and protects its
process memory. It starts Node with a fixed credential-free environment, listens
on a 0600 guest-local Unix socket and waits for Chromium's blank-context/font/
screenshot warmup readiness. Only then can port 49983 `/health` succeed. The snapshot
contains the blank browser and internal Go↔Node socket, never a tenant page,
control token or external broker session. Node has no inbound TCP listener.

Port 49983 remains restricted to trusted Cube bootstrap traffic. Cubelet `/init`
accepts exactly `RUNTIMED_HTTP_ADDR=:3031` and a fresh random
`RUNTIMED_HTTP_TOKEN`; retries with the same token are idempotent and any token
replacement is refused. The token hash stays in protected Go memory. It is not
written to disk or inherited by Node. `/health` remains usable after init for
Cubelet compatibility, while `/snapshot-ready` returns 503 after initialization.
The template builder must record `/snapshot-ready` 200 and `initialized:false`
before freezing. The factory must reference only this reviewed snapshot. Never
create a capture template from a restored or used instance.

Port 3031 provides only authenticated `GET /health` and single-use
`GET /capture/channel` WebSocket. Browser Origin headers are rejected. Control
requests contain the per-instance bearer plus the independent private CubeProxy
ingress token. The authenticated host establishes this connection only when a
capture is leased. The guest starts no outbound network connection. WebSocket
messages carry binary chunks of at most 64 KiB; existing newline JSON protocol
frames remain bounded at 20 MiB and the job connection at 45 seconds. The platform
broker and worker retain their stricter HTTP/WebSocket job budgets.

Fresh instances must have private ingress and deny-all guest egress. The control
plane's selected policy is not a proof of enforcement; verify the capture VM's
actual policy separately before enabling a pilot. Guest data still flows only
through the authorized platform broker. Successful or failed jobs permanently
consume the VM; the factory requires confirmed deletion before releasing its
resource reservation. No used browser, VM, snapshot or job connection is reused.

The host manager's Cube factory is opt-in and requires a durable private lease
journal. Create intent is fsynced before the API mutation. An ambiguous creation
without a response ID blocks restart pending explicit reconciliation; it cannot
be declared absent by an immediate lookup. Confirmed VM identities are checked
against instance, lease and reviewed snapshot before startup recovery deletes
them. API credentials and per-instance tokens never enter the journal or browser.

## Bootstrap fixture

`check-bootstrap.cjs` runs inside the dedicated image via `docker exec -i`, with
network disabled, a read-only root, all capabilities dropped, no-new-privileges,
256-process cgroup, 768 MiB memory, one CPU, a 256 MiB `/tmp`, and 128 MiB shared
memory. It verifies pristine health before init, protected supervisor memory,
authentication, absent filesystem APIs, and actual Chromium rendering through
loopback-only broker fixtures. It does not contact external sites.

The 2026-09-24 fixture passed with the reviewed worker base
`sha256:92e589bb3f99762b51bd04194a9c71cb83fc807984f38a441d2cc36c4c4b00ac`.
The consumed supervisor remained alive until explicit container deletion; the
renderer was not reused. The Go unit tests additionally verify repeat-use refusal
and deadline/chunk/frame bounds. The outer fixture container was removed.

Do not substitute `RLIMIT_NPROC` for a guest process cgroup in shared-host tests: it
counts UID 1000 threads across Docker containers, and an attempted limit of 256
prevented Go itself from starting. The fixture retains its actual cgroup process
limit. Actual Cube guest process enforcement must be established during the pilot,
along with pristine snapshot restore, deny-all egress and VM overhead.

## Current prepared-page pilot

The worker now prepares empty pages for DPR 1 and DPR 2 before pristine readiness.
Their HTTP/WebSocket callbacks deny requests until the sole job is selected;
the unused context is closed and the chosen page brought to the foreground before
navigation. No tenant URL, credential or host broker connection exists in these
prepared contexts. The consumed worker remains ineligible for reuse.

The exact current-source image tested on 2026-09-24 is
`sha256:b749065c8731d005f95fb8f795e35bafe6968e9380dda70e7a3b2bc3c7f776d6`,
template `tpl-b59bceac5b8a48708a6e219c`. All 18 real browser cases passed on both
Docker and restored Cube guests. The platform repository retains the scripts,
source hashes and every final timing sample under
`landing/services/capture/benchmarks/2026-09-24/cube/`.

Two-worker/eight-job comparison at 1 CPU/768 MiB per worker: Docker 6.53 s, Cube 10.48 s.
Cube pool preparation was 204 ms versus Docker 1770 ms, but median screenshot execution
was 2010 ms versus 323 ms. Cube adds a KVM layer inside this disposable benchmark VM;
this is not bare-metal production timing. **Keep the Docker capture backend as
default.** Successful restoration and browser compatibility do not establish a
capture throughput improvement or complete network-isolation enforcement.
