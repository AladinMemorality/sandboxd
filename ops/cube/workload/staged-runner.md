# Prepared higher-concurrency fixture

This is benchmark source, **not an executed six/eight/twelve measurement or a
production quota change**. The September 25 executable is historical: current
storage enforcement rejects guarded stages above four in an ordinary build.
Use a newly pinned Linux/CGo test binary built with `cube_workload_benchmark`. Production guard/enrollment remains
four slots. The historical four/12 results and binaries are retained unchanged.

The tag expands only compiled library/test bounds to the three explicit profiles
`cpu1-mem1024`, `cpu1-mem2048`, and `cpu2-mem2048`. Ordinary compilation retains
2CPU/2048MiB and at most four guarded reservations. Production daemon/migration
entrypoints call `RequireStorageGuard`, which still rejects other profiles and
capacity even in a tagged build. Do not build/deploy a production service with
this benchmark tag. The immutable canonical admission policy is never updated.
Fresh fixture databases retain the genuine observer, generation/epoch checks,
48GiB reserve and conservative 12GiB per-allocation storage debit, even for the
1GiB profile. Native worker start/recovery validation remains four/2CPU/2GiB.

`run-stage.py` is an operator runner with no service, quota or baseline mutation
commands. It supports exactly 4/6/8/12 slots. Preparation remains 512 MiB/app,
4 MiB chunks, 20 seconds maximum; steady load remains 45 seconds, one start per
process, and fixed 60/80-second automatic stops. The added process resource usage
reports minor/major faults. An independently cancelable monitor runs from before
provisioning until cleanup, writing phase events and timestamped JSON lines for
outer/worker memory, aggregate faults, CPU ticks/run queue, memory PSI, QEMU
memory.stat/current/lifetime peak, CPU statistics/pressure, and platform latency.
Samples target one-second spacing **plus their recorded collection latency**;
the fixed SSH metric observation can itself take longer and abort on failure.
This overhead requires a new four-slot baseline before comparison to older runs.

Memory floors are now 12 GiB on both hosts initially, 8 GiB outer/6 GiB worker
while running. Abort triggers include OOM/max changes, swap use, QEMU current
memory >=40 GiB, >1% full-memory PSI for ten seconds, three platform probe
failures or >1-second responses, lost controller/LCM hold, or metric errors.
Cancellation runs the existing independently bounded owned-guest cleanup; it
does not terminate unrelated workloads. Reactive monitoring cannot guarantee
that another workload will never cause pressure. The wrapper checks steady
HTTP/supervisor p95 <=250 ms and max <=5 seconds, preserving every sample.

## Required operator preparation

1. Finish current canonical tasks. Independently export/checkpoint the retained
   canary and preserve its latest marker/config proof. Pause it normally and
   confirm its canonical row is stopped and its sole provider state is paused.
   Do not delete, replace, wake or rewrite the canary to make a test pass.
2. Establish a genuine admission hold. The prepared runner requires the exact
   controller container **stopped**, with its immutable image pinned, plus LCM
   frozen. It does not perform those operations or restore them. All existing
   Docker guest containers remain untouched. `CUBE_ENABLED=false`, a maintenance
   page, or an operator flock alone is insufficient: existing Cube bindings can
   still wake. A future less disruptive hold needs its own reviewed enforcement.
3. Under the three outer deployment/operator locks and worker handoff, apply the
   selected reviewed native quota only: 4=10000m/10Gi, 6=14000m/14Gi,
   8=19000m/18Gi, 12=28000m/30Gi. The wrapper checks both YAML and the exact node returned by read-only
   worker-local Master `GET http://127.0.0.1:8089/internal/node?host_id=...`:
   matching advertised CPU/memory quota, zero used quota/creates, ready/healthy,
   and a metric no older than30 seconds. It refuses unavailable or incompatible
   observations. This supported interface was verified in the pinned upstream
   `CubeMaster/pkg/service/httpservice/inner/info.go` and
   `pkg/base/node/node.go`; the new preflight has not yet run on the live worker. Preserve the complete old config. Keep VM40GiB/12vCPU,
   QEMU42/44GiB high/max, zero swap and1000%CPU. The runner checks cgroup limits
   and exact dynamic quota fields; it does not apply or restore them.
4. Require zero charged/pending canonical admission, no active canonical task,
   no worker containerd tasks and no provider job outside READY/FAILED across
   the eight pinned MySQL job tables. Preserve the paused baseline and its disk
   usage in the real storage observer; require the real guard in the fixture DB.
5. Write root-owned0600 configuration and a fresh version1 drain receipt. All
   paths must be canonical below private root-owned, non-writable directories.
   Receipt fields: `outer_boot_id`, `worker_boot_id`, `controller_id`,
   `started_boottime_ns`, `controller_stopped:true`, `canonical_charged:0`,
   `canonical_active_tasks:0`, `provider_active_jobs:0`, `worker_tasks:0`,
   `native_quota:[14000,"14Gi"]` for six slots, `profile`, `template_id`,
   `worker_node_id` matching the config, and `paused_baseline` matching
   the fixture object below. An observation older than five minutes or from a
   different outer boot is refused. This receipt is operator evidence, not a
   substitute for the runner's independent live checks.

## Configuration and execution

Use a fresh `/opt/baarcha-bench/cube-workload-...` stage and work directory. The
configuration contains `controller_id`, `controller_image`, `worker_boot_id`,
`lcm_id`, `worker_node_id` (exact Master InstanceID), `stage`, `binary`, `binary_sha256`, `store_source_dir`, `drain_receipt`,
`marker_receipt`, `canary_config_receipt`, and `fixture`. The binary and private
inputs must have no group/other permissions; source cwd is the exact reviewed
`control-plane/internal/store` directory with its matching `../../migrations`.

`fixture` is the existing private workload configuration: fixed loopback
`api_url=http://127.0.0.1:20300`, `proxy_url=http://127.0.0.1:20080`, `api_key`,
`domain=cube.app`, reviewed immutable `template_id`, explicit `profile` from the three choices above,
`max_active`, `work_dir`,
and the exact installed `storage_guard`. The wrapper also reads
`cubemastercli tpl info --template-id EXACT --include-request --json` and
requires READY, exactly one container with the chosen CPU/memory, deny-out
networking and a10GiB writable layer. The admission client independently checks
the actual created guest against the configured template/resources; mismatch
retains the charged outcome for review. New 1CPU template IDs must be prepared
and reviewed separately; the runner does not create templates or resize the canary.
Its optional `paused_baseline` has:

```json
{
  "runtime_id": "EXACT_32_HEX_PROVIDER_ID",
  "template_id": "EXACT_EXISTING_TEMPLATE",
  "app_id": "EXACT_26_CHAR_CANARY_APP",
  "provider_json_sha256": "SHA256_OF_GO_CANONICAL_MARSHALED_FULL_PROVIDER_ROW",
  "config_sha256": "SHA256_OF_PRIVATE_CHECKPOINT_CONFIG_RECEIPT",
  "marker_receipt_sha256": "SHA256_OF_PRIVATE_LATEST_MARKER_RECEIPT"
}
```

Placeholders are unusable. The provider row must match paused state, resources,
template and `metadata.sandboxd_app_id`; its full canonical fingerprint must
match both before allocation and after cleanup. The baseline is never inserted
into the fixture's owned list or passed to any create/connect/delete operation.
Marker/config hashes bind previously verified private receipts; the runner does
not wake the baseline to re-read markers. While the controller and LCM remain
held, the operator must prohibit all other guest/snapshot/template operations.

Run read-only preflight first, then only under an explicit scheduled handoff:

```sh
python3 run-stage.py /root/private-reviewed-stage.json
systemd-run --unit=REVIEWED_UNIQUE_UNIT --property=CPUQuota=200% \
  --property=MemoryMax=2G --property=MemorySwapMax=0 --property=TasksMax=128 \
  --property=RuntimeMaxSec=700 --property=StandardOutput=append:/root/PRIVATE_LOG \
  --property=StandardError=append:/root/PRIVATE_LOG \
  python3 /root/PINNED/run-stage.py /root/private-reviewed-stage.json --execute
```

The wrapper holds the first three established flocks; the native fixture holds
the fourth workload flock and worker-side acceptance flock through cleanup.
Retain failures and verify the baseline/API/Master/tasks/charges independently.
Only the coordinating operator may then restore the prior native quota,
unfreeze LCM, and reopen the controller after verifying its correct pin. No
automatic restoration or fabricated zero-inventory result is provided here.

## Build and offline validation

On Linux with the reviewed Go toolchain and cached modules, with all live fixture
configuration variables unset, run from `control-plane`:

```sh
go test -race -p 1 ./internal/cube ./internal/store
go test -race -p 1 -tags cube_workload_benchmark ./internal/cube ./internal/store
CGO_ENABLED=1 go test -c -tags cube_workload_benchmark -o /PRIVATE/cube-workload.test ./internal/store
```

The tested Linux binary, full source archive/manifest hashes, resource bounds and
cleanup are recorded in [benchmark build evidence](benchmark-build-2026-09-26.json).
Record the entire staged source manifest and binary SHA256 before execution.
The tag does not create guests: the native fixture remains explicitly opt-in.
Offline regressions exercise all4/6/8/12 × three profile enrollments, real SQLite
capacity refusal, unchanged storage debits/staleness, immutable policies, actual
resource mismatches, and production-entrypoint refusal in both builds. Python
runner tests cover actual Master identity/quota/usage/freshness, template
resources and the existing receipt/latency/hold gates.

`staged-validation-2026-09-25.json` remains the historical source/binary record;
it is not evidence that the current guarded higher-capacity path executed.
The historical four-app pass and twelve-app failure remain unchanged.

## Next measurements and remaining limits

Finish recovery, preserve/checkpoint and pause the exact canonical canary, then
establish the documented hold. Run a new4-slot baseline with the current monitor
before6, then8, then12; stop escalation on a failure and retain its evidence.
Keep the workload/deadlines unchanged. All profiles still use the same reviewed
native quota table; allocation ceilings are not physical CPU capacity.

This fixture is512MiB page-touch plus a modest CPU duty cycle, not a coding/build
workload. The1CPU choices make resource-profile tests possible; they do not prove
1GiB is suitable for customer projects. A matched realistic comparison still
needs the existing deterministic frontend-build payload adapted to disposable
owned guests, and its resource sampler adapted from the pinned canonical guest
and2GiB limit. Run identical edit/tests/build/API/HMR with matched caches and
multiple fresh trials at1CPU/2GiB versus1CPU/1GiB before selecting a profile.
No adaptation may wake or alter the paused canonical canary. The earlier842MiB
single build sample is insufficient1GiB evidence. Global rollout also still
needs recovered lifecycle/backup proof and an explicit production policy
transition; neither is performed by this benchmark.
