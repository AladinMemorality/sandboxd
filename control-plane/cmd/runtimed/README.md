# runtimed — in-sandbox supervisor

`runtimed` runs **inside every sandbox container** as its main process (under
`tini`). It supervises the app's dev server and executes coding tasks, so the
preview survives idle→wake and task execution has a stable owner. The control plane
(`sandboxd`) talks to it over a Unix domain socket on the workspace mount;
`runtimed` is never network-reachable.

## What it does

**Dev-server supervision.** Starts the app's dev server (`RUNTIMED_DEV_CMD`, default
`pnpm dev`) in its own process group, restarts it with exponential backoff on
unexpected exit, and after repeated fast failures stops restarting and reports the
preview `down` rather than crash-looping. It reads the app's `sandbox.yaml`
[manifest](../../../docs/sandbox-manifest.md) for the dev command + preview port.

**Health probing.** Polls the dev-server port and derives `preview.status`
(`down` / `starting` / `ready`).

**Coding tasks.** `POST /tasks`, `GET /tasks/{id}/events`, `POST /tasks/{id}/cancel`
— **one active task at a time** (a concurrent submit gets `409 task_in_progress`).
Per task: a pre-task git checkpoint (the app dir is `git init`-ed on first use) →
run the agent → authoritative `files_changed` from `git diff` against the checkpoint
→ a build check → the canonical `runtime.TaskResult`.

**Agents.** Adapters for **opencode** (default), **claude-code**, and **codex** —
each drives the CLI with `--dangerously-skip-permissions` and parses its stream into
canonical `message` events. Provider credentials never enter the sandbox; the
control-plane proxy injects them on the wire (see [agent-auth](../../../docs/agent-auth.md)).

**Events.** A monotonic stream — `status` / `message` / `build` / `done` — appended
to `.runtimed/tasks/<id>/events.jsonl` and streamed live (NDJSON, resumable via
`?since=`). `done` is the single terminal event and carries the result.

**Persistence + recovery.** Each task keeps `.runtimed/tasks/<id>/` with
`events.jsonl`, `result.json`, `agent.log` (the agent CLI's stderr), and
`stream.jsonl` — the agent CLI's raw stdout, one record per line with the line's
arrival timestamp (`{"ts":…,"ev":<verbatim JSON line>}`). `events.jsonl` is the
mapped, truncated view; `stream.jsonl` is the exact transcript for debugging what
the agent did and when. On boot, a task with an event log but no
`result.json` (interrupted by a stop/crash) is finalized `failed` — never resumed.

**Cancellation & timeout.** Cancel kills the agent's process group and finalizes
`cancelled`; a timeout is a runtimed-initiated cancellation (`failed` /
`agent_timeout`).

## Control surface

`GET /status` (→ `runtime.Status`) and the `/tasks` endpoints are served over
HTTP/1.1 on a Unix socket at `/home/sandbox/.runtimed/sock`. The socket lives on the
durable workspace, so `sandboxd` reaches the same inode on the host. No network
port; no cross-tenant reachability. The shared protocol types and the sandboxd-side
`runtime.Client` live in `control-plane/internal/runtime`.

## Configuration (environment)

| Variable | Default |
|---|---|
| `RUNTIMED_APP_DIR` | `/home/sandbox/workspace/app` |
| `RUNTIMED_DIR` | `/home/sandbox/.runtimed` |
| `RUNTIMED_SOCKET` | `<RUNTIMED_DIR>/sock` |
| `RUNTIMED_DEV_CMD` | `pnpm dev` |
| `RUNTIMED_PREVIEW_PORT` | `3000` |
| `RUNTIMED_PROBE_INTERVAL_SECONDS` | `3` |

The control plane also passes the selected runtime preset and the agent-proxy URL
via env at container create.

## Build

```sh
CGO_ENABLED=0 go build -o runtimed ./cmd/runtimed
```

Pure Go, statically linked. It's compiled into the sandbox base image by
`image/build.sh` as the container's `CMD` under `tini`, so every sandbox boots it
automatically — there is no `docker exec`-started dev server.

## Not implemented (by design)

- **Full task event-log retention past destroy** — only the canonical *result* is
  kept (in the control plane's SQLite); the event *log* lives with the workspace and
  is gone once the sandbox is destroyed.
- **Provider-derived `tool` / `file_change` events** — only `message` events are
  surfaced; `files_changed` is always computed from git.
- **Dev-server restart on dependency changes** — a task that edits `package.json`
  does not yet trigger a dev-server restart.

## Optional authenticated guest HTTP transport

The default remains the Unix socket (`RUNTIMED_SOCKET`). To supervise a Cube
microVM without a shared host filesystem, set `RUNTIMED_HTTP_ADDR=:3031` and
`RUNTIMED_HTTP_TOKEN` to a unique cryptographically random token for that sandbox.
Generate at least 32 random bytes and encode as hex or unpadded base64url. The
process fails at startup if HTTP is enabled with a missing or malformed token.
Unix and HTTP listeners then serve the same status/task/event/cancel/message/
revert protocol. All HTTP routes, including unknown paths, require
`Authorization: Bearer <token>` before routing. No token is logged.

Keep the management port on private, restricted ingress; it must not be listed
among public application preview ports. For a local CubeProxy route, the control
plane can use a private proxy origin with HTTP Host `3031-<cube-id>.cube.app`.
If the proxy itself has public ingress, block management-port hostnames there.
Use Cube private ingress and its per-sandbox `cube-traffic-access-token` header
when available; this is a different credential from the Cube management API key.
A Host header is routing, not access control. Across an untrusted network, use
HTTPS (or a private encrypted transport); a bearer token over plain HTTP alone
is insufficient.

The control-plane constructor is:

```go
client, err := runtime.NewRemoteClient(runtime.RemoteConfig{
    BaseURL: "http://127.0.0.1:80", // trusted/private proxy origin
    Host:    "3031-<cube-id>.cube.app",
    Token:   token,
    TrafficAccessToken: trafficToken, // private ingress token returned at creation
})
```

Redirects and environment proxy discovery are disabled for remote clients.
Ordinary RPCs retain their five-second timeout. Task events stream immediately,
with no overall client/server write timeout; cancellation or closing the response
body closes the stream. This protocol is newline-delimited JSON, not SSE.

The supervisor removes its HTTP token from the inherited environment before
launching web/worker processes; agent environment filtering also excludes all
`RUNTIMED_*` variables. This is not an isolation boundary from other code running
under the same guest UID. Tokens grant access only to their own sandbox and must
never authorize a host operation or another tenant. Never bake a live token into
a reusable image/template. Publishing/remixing a guest memory snapshot requires
credential sanitization and a new token binding; changing boot environment alone
does not rotate a token already retained in a restored process's memory.

This transport does not add host filesystem mounts or generic command execution.
The existing protocol does not provide workspace file read/write/export or
supervised-process logs; these need separately reviewed scoped guest APIs before
those Docker-dependent operations can be migrated.
