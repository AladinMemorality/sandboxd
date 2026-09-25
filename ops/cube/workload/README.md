# Bounded four-or-twelve-app workload fixture

**Twelve-app acceptance failed on2026-09-25.** The corrected asynchronous run
exceeded the20-second resident-memory deadline; it did not reach steady work.
See [recorded results](live-validation-2026-09-25.json) and
[analysis](analysis-2026-09-25.md). The four-app variant passed unchanged bounds; see
[four-app evidence](four-app-validation-2026-09-25.json). Building or running the ordinary unit suite creates no Cube guest.
The live test requires a separate explicit coordinator handoff after all other
worker fixtures and template jobs have finished. It is not production capacity
acceptance, a migration, a crash test, or a request to change resource quotas.

`control-plane/internal/store/cube_workload_live_linux_test.go` uses the actual
Cube client and durable SQLite admission adapter. It creates exactly four or twelve
synthetic guests from the reviewed React Pro template, each verified by admission
as **2 CPU / 2048 MiB**. It imports a dependency-free Node HTTP app into each new
fixture workspace through the authenticated private-workspace transport. No
customer app, source, provider credential, model call, or outbound HTTP is used.
The only host reverse-channel policy denies all IPv4 and IPv6 destinations; the
synthetic program contains no outbound networking code.

The workload starts only after all selected HTTP apps are ready. Each allocates
exactly512 MiB once, acknowledges immediately, and touches one byte per4096-byte
page in4 MiB chunks scheduled on separate event-loop turns. All apps must reach
full resident memory with verified page checksums within20 seconds. Each then holds that allocation
while executing a 5 ms CPU loop every 100 ms. This is approximately 5% of one CPU
per app, not 24 CPU of saturation. Each response reports resident memory, the
page checksum, process identity, CPU usage and completed compute cycles. The
coordinator attempts to verify all selected apps remain resident and make progress throughout a
45-second interval, concurrently reads HTTP and supervisor health with 5-second
request deadlines, and verifies the next over-budget allocation is promptly refused (fifth or
thirteenth). A future pass would support only this modest workload at the
selected concurrency, not maximum tenant CPU or memory demand.

The app stops CPU work and requests garbage collection60 seconds after all
pages are touched, with an independent absolute80-second deadline from the
initial activation, even if the coordinator or channel disappears. Pending
chunks cannot resume allocation after that deadline. It also has an eight-minute process
lifetime limit. Only the first `/start` request can allocate; retries are refused.
No user-adjustable memory amount, duration or compute intensity is accepted.
Guest deletion remains the authoritative resource cleanup. Failed/ambiguous
operations retain the private admission database and exact ownership record for
operator recovery; never clear a pending reservation based on elapsed time.

## Preconditions and invocation (inert example)

Run natively as root on the outer VPS, not in the worker or a Docker build
container. The binary needs its working directory at the checked source's
`control-plane/internal/store` so `../../migrations` resolves correctly.

Use a root-owned0600 JSON configuration with the same schema as the existing
admission fixture: `api_url`, `api_key`, `proxy_url`, `domain`, `template_id`,
`work_dir`, `max_active`. Fixed endpoints are `http://127.0.0.1:20300` and
`http://127.0.0.1:20080`, domain `cube.app`, and `max_active` must be exactly4 or12. No other scale is accepted. The work
path must be a fresh absolute directory beginning
`/opt/baarcha-bench/cube-workload-`; the test creates it0700 and refuses reuse.
Supply the API key using the existing private operator credential file; never
put it in shell history, tool output, this repository, or a public report.

Before assigning the handoff, the coordinator must verify no template job or
unrelated worker operation is in flight. The test independently refuses a
nonempty authenticated API inventory and a nonzero all-state CLI inventory.
It holds `/run/lock/cube-operator-acceptance.lock` inside the worker for its entire
create/load/cleanup interval, the same lock as PG/reload/crash acceptance, plus
an outer workload coordinator lock. Losing the operator process leaves the
fixed app deadline in place; inspect retained ownership evidence before another
fixture. No lock can make an operator bypassing this protocol safe.

The worker identity and SSH key/known-hosts paths are fixed to the reviewed
worker-01 endpoint. Read-only `/proc/meminfo` and `/proc/vmstat` from the worker
and outer host are captured before allocation, during steady work (if reached), and after cleanup.
The external operator wrapper also sampled the outer QEMU cgroup every second
throughout provisioning/preparation/cleanup; those peaks are measured over the
fixture interval, whereas kernel memory.peak is explicitly a lifetime counter. The fixture
requires at least12 GiB worker /4 GiB outer `MemAvailable` initially and aborts
below4 GiB worker /2 GiB outer during the run. Any global `oom_kill` increase
fails the test, even if caused by unrelated host work. Memory figures are
available memory, not a guarantee against all possible memory pressure.

After coordinator authorization only:

```sh
cd /opt/baarcha-bench/cube-workload-build-20260925/source/control-plane/internal/store
CUBE_WORKLOAD_LIVE_CONFIG=/private/operator/workload-config.json \
  /opt/baarcha-bench/cube-workload-build-20260925/bin/workload-4or12.test \
  -test.run '^TestLiveCubeAppWorkload$' -test.v -test.timeout 10m15s
```

The test allows7.5 minutes for preflight/provisioning/load and a separate2-minute
owned-ID cleanup window. The live test should complete well before those upper
bounds. On timeout or cleanup failure, retain the private stage; review all
charged/pending admission rows and exact provider identities. Delete only owned
fixture IDs through the guarded client. Never use a global guest deletion or
blindly retry an ambiguous create.

`result.json` records individual app response/status latency samples, resident
memory/CPU evidence, host metrics, capacity refusal, and cleanup status.
`owned-private.json` contains fixture runtime tokens and must remain0600/private.
The admission database also remains private. Successful cleanup requires zero
charged reservations plus an empty final provider inventory; fixture data may
then be removed only by its exact reviewed stage path.

## Validation without guest execution

```sh
node ops/cube/workload/test_script.cjs
go test -race -run 'TestWorkload|TestLiveCubeAppWorkload' ./internal/store
```

Run the Go command from `control-plane` on Linux without
`CUBE_WORKLOAD_LIVE_CONFIG`. Unit tests verify the real script's bounded
allocation request/page touches,4 MiB chunks, immediate acknowledgment, one-shot
activation,60/80-second deadlines, CPU duty and timers using
mock memory/clock primitives (no512 MiB allocation or socket). Go tests validate
the import archive and reject missing metrics, OOM increments, low memory,
unresident/untouched allocations and no-progress/restarted processes. These
checks and compiling the live binary are preparation, not a live workload pass.

The fixture only changes its private admission SQLite budget when selecting4
slots. It does not change production admission settings, worker quotas, template
resources, memory size, or deadlines. The original executed12-slot binary and
source copy remain preserved separately; `workload-4or12.test` is the new candidate.
