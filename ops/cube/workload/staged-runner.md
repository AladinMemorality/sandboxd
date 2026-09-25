# Prepared higher-concurrency fixture

This is tested source and a bounded executable, **not an executed six/eight/twelve
measurement or a production quota change**. Production guard/enrollment remains
four slots. The historical four/12 results and binaries are retained unchanged.

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
   8=19000m/18Gi, 12=28000m/30Gi. Verify the actual worker node reports it, not
   only its config file. Preserve the complete old config. Keep VM40GiB/12vCPU,
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
   `native_quota:[14000,"14Gi"]` for six slots, and `paused_baseline` matching
   the fixture object below. An observation older than five minutes or from a
   different outer boot is refused. This receipt is operator evidence, not a
   substitute for the runner's independent live checks.

## Configuration and execution

Use a fresh `/opt/baarcha-bench/cube-workload-...` stage and work directory. The
configuration contains `controller_id`, `controller_image`, `worker_boot_id`,
`lcm_id`, `stage`, `binary`, `binary_sha256`, `store_source_dir`, `drain_receipt`,
`marker_receipt`, `canary_config_receipt`, and `fixture`. The binary and private
inputs must have no group/other permissions; source cwd is the exact reviewed
`control-plane/internal/store` directory with its matching `../../migrations`.

`fixture` is the existing private workload configuration: fixed loopback
`api_url=http://127.0.0.1:20300`, `proxy_url=http://127.0.0.1:20080`, `api_key`,
`domain=cube.app`, reviewed2CPU/2048MiB `template_id`, `max_active`, `work_dir`,
and the exact installed `storage_guard`. Its optional `paused_baseline` has:

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

The prepared native executable and its exact source cwd/hash are recorded in
`staged-validation-2026-09-25.json`. The binary contains the real SQLite/CGo
store implementation. It was compiled after passing native race tests; no live
workload configuration was supplied, so no six/eight/twelve guests were run.
