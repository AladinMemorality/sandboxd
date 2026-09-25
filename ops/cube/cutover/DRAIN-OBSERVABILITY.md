# Read-only drain observation

`drain_observe.py` is an observation tool, not a receipt generator. Run as native
host root with the exact full controller ID. It only reads service/process/socket
metadata, SQLite in read-only mode, and Caddy's loopback configuration. It writes
one new root0600 report in an existing root-private directory. No headers,
request bodies, environment values, remote addresses or process command lines
are retained. It does not stop any service or wake any guest.

```
python3 /opt/baarcha-bench/cube-host-lifecycle-enrollment-20260925/drain_observe.py \
  --controller-id 5fb591f9c2705b5611efee74d0fff7c3169897afc7719977c7f2a0d52f25fe67 \
  --output /opt/baarcha-bench/cube-host-lifecycle-enrollment-20260925/drain-before.json
```

Use a new output filename on every invocation. After the separately authorized
controller stop, supply `--shutdown-since` with the recorded UTC time immediately
before sending its stop request. This must be within the last 30 minutes.
The report checks the same container has exited normally after that time,
without OOM, with exactly one shutdown signal log, no HTTP shutdown timeout and
no store-close failure. This supports **regular HTTP shutdown** only; Go's
`http.Server.Shutdown` does not wait for hijacked WebSocket connections.
Missing evidence yields false or unknown, never inferred success.

## Scope of runtime writers

Source review on 2026-09-25:

- `/api/tools/call`, `/api/bridge`, `/api/projects/*` and project publish/remix/
  preview paths can contact or mutate the runtime. Their new requests are
  covered by the prepared offline Caddy fence. Existing requests still require
  observation; a successful controller shutdown does not prove every caller
  received a successful response.
- Chat tool execution happens in a subsequent browser request to
  `/api/tools/call`, not inside the model's SSE stream. `/api/chat` can read
  project context and write platform chat/memory records; its returned tool
  arguments cannot bypass the separate fenced tool endpoint.
- `/api/v1/chat/completions` is inference plus platform usage accounting.
  `/api/live/transcribe` relays microphone transcription and usage accounting;
  it does not dispatch sandbox operations. They are not extra stop prerequisites
  for an empty Cube worker. Stopping live-ear would terminate microphone
  sessions, so it must not be used as a harmless drain probe.
- Automatic thumbnail capture can continue in an `after()` callback after the
  project HTTP response ends. The durable `project_thumbnail_capture` lease
  is separate evidence; no live HTTP socket is not sufficient proof of no
  thumbnail work.
- The env-apply timer can recreate a sandbox. Observe it inactive AND its
  oneshot service inactive after fencing its timer. The Classroom and Fennec
  egress timers are also explicitly recorded; stop only the reviewed writers,
  preserving their original active state for restoration. Pending env rows may
  remain queued safely while that writer is fenced; they are not running work.

For an authorized read-only PostgreSQL session, collect counts only:

```sql
BEGIN READ ONLY;
SELECT count(*) AS capturing_thumbnail_rows,
       count(*) FILTER (WHERE lease_until > clock_timestamp()) AS live_thumbnail_leases
  FROM project_thumbnail_capture WHERE state = 'capturing';
SELECT count(*) AS queued_env_updates FROM project_env_apply WHERE pending;
COMMIT;
```

A missing table is unknown, not zero. An expired lease alone does not prove the
old worker has exited. Resolve remaining capture work using its actual terminal
lease state and capture service job observations, not by waiting out the lease.

## What current instrumentation cannot prove

Controller Prometheus API counters/histograms record **completed** requests;
there is no authoritative active-runtime-request gauge in the deployed code.
Next's socket table cannot distinguish inference, project tools and idle pools.
An established socket is not necessarily work; an absent socket is not proof
against a delayed handler or after-response callback. The collector reports
both limitations, rechecks process generation/socket ownership during its
sample, and never outputs a request count or a ready/drained authorization.

Direct operator API/Docker/SQLite users must be excluded by the actual operator
maintenance handoff. The fixed coordinator then independently checks the
stopped controller, exclusive maintenance lock and native DB descriptor users.
Provider jobs and all-state provider/containerd inventories are separate checks;
this outer-host collector performs no worker operations.

For the **first empty-worker enrollment**, all 66 canonical bindings are Docker
and the provider guest inventory is empty. Existing Docker application/voice/
inference traffic is not evidence of a Cube writer. The required mechanical
boundary is the fenced canonical controller and writers, genuine exclusive DB
ownership, and actual empty provider state. This is narrower than claiming every
platform request has completed. If a receipt needs a stronger condition than
these observations establish, leave its field unproven; do not turn socket
counts or elapsed time into an attestation.
