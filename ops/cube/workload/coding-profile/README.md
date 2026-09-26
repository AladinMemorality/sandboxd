# Coding workload observer

This Linux operator tool observes only sandbox `01M3D1Q0E1KM1FEM244XVHEC65` / provider `095a0076b90b4b009ab995837909e276`. It does not start tasks, execute guest commands, change quotas, pause/resume guests, acquire lifecycle locks, or read application files. A separate operator runs the coding task.

It checks the canonical SQLite binding, worker boot, QEMU/shim PID start ticks, cgroup membership, KVM ownership, filesystem UUID and exact current writable volume generation. A changed identity or unavailable metric fails the run; empty output is never treated as zero. Process environments and command lines are never read. Tool metadata is parsed privately; only selected identities/counters leave the worker. Outputs and stderr remain in a fresh root-private directory.

## Run

Run on the outer VPS using the already reviewed tools and pinned nested SSH key/known-hosts files. Choose a fresh unit and output name; one observer is sufficient. The tool accepts 10–1200 seconds. This example merely starts an observer:

```sh
systemd-run --unit=cube-coding-profile-coding-UNIQUE \
  --property=CPUQuota=25% --property=MemoryMax=128M \
  --property=MemorySwapMax=0 --property=TasksMax=32 \
  --property=RuntimeMaxSec=960 \
  python3 /opt/baarcha-bench/cube-coding-profile-tools-20260925-01/sample.py \
    --output /opt/baarcha-bench/cube-coding-profile-coding-UNIQUE \
    --seconds 900 --label coding
```

The observer uses one SSH connection and sends its fixed worker collector on stdin. It installs nothing in the guest or worker. Host/shim counters are sampled every second; supported containerd task metrics every five seconds. The source file hashes are recorded in `sampler-identity.json`. `samples.jsonl` is flushed after each sample, so a partial failed observation remains reviewable. `summary.json` reports completion separately from measurements.

Every sample includes only task IDs, states and creation/finish timestamps from the canonical runtime database. Optional `--phase-file /root/private-phases.jsonl` accepts a root-private, canonical file of at most 64KiB containing JSON lines such as:

```json
{"phase":"coding","task_id":"EXACT_TASK_ID","at_unix_ns":1790368000000000000}
```

Allowed phases are `idle`, `coding`, `build`, `verification`, and `finished`. The operator can append phase events. The sampler does not infer model/tool phases from application transcripts.

## What the counters mean

| Scope | Meaning | Limits |
| --- | --- | --- |
| Guest container | Agent-reported container cgroup memory usage and CPU time, via `ctr tasks metrics` | Includes container processes such as Node/PostgreSQL; does not measure the entire guest kernel. Assigned 2GiB is a limit, not observed use. |
| Guest VM | KVM-owning `containerd-shim` process RSS/PSS, cgroup memory, CPU and process I/O inside worker | PSS accounts for shared mappings; cgroup memory also contains cache not represented by process PSS. Do not add these overlapping measures. |
| Whole worker | Outer QEMU process and service cgroup, plus host/worker MemAvailable/cache counters | Includes Cube services, all guest VMs, caches, operator activity and observer overhead. It cannot be attributed entirely to the coding app. |
| Writable disk | Exact 10GiB backing file logical size and allocated `st_blocks × 512` | Reflink/snapshot sharing means allocated blocks are not unique physical disk consumption. Lower images and other snapshot files are separate. |

CPU percentages are fractions of **one CPU core**, calculated from monotonic elapsed time and counter deltas. 100% means one core; 200% means two. PID generation is checked before using deltas. RSS/PSS peaks are sampled peaks, not guaranteed instantaneous maxima. Raw cgroup `memory.peak`, where present, is a lifetime counter and is not relabeled as this run's peak. Process `read_bytes/write_bytes` differ from logical `rchar/wchar`; unavailable cgroup `io.stat` remains null. No complete guest block-device accounting is claimed.

The deployed source implements task Stats through `CubeShim/shim/src/service/task_srv.rs` → `sandbox/sb.rs::stats_container` → `agent/src/rpc.rs::stats_container` → the container's `stats()`. The shim's `normalize_guest_stats` copies memory usage/limit and CPU counters but does not populate `MemoryStat.cache`. The CLI's displayed cache 0 is therefore unavailable, explicitly converted to null. Guest MemAvailable/cache split is not obtained by this tool; host and worker kernel counters are recorded separately.

## Verified idle baseline,25 September 2026

`idle-evidence.json` contains sanitized values and hashes of the retained private raw files. The 60s run completed with 61 host/shim samples and 13 guest-container samples. PostgreSQL was running and three prior AI tasks remained failed; this was not a pristine VM. No task was submitted by the observer.

- Guest container: 118.57MiB sampled peak against a 2GiB limit; 0.115% of one CPU core average.
- Guest VM: 560.47MiB sampled PSS peak; 562.75MiB RSS; 894.33MiB cgroup memory; 0.583% CPU average.
- Writable file: 10GiB logical, 45.59MiB allocated; no allocated growth during the run.
- Whole worker: 4.56GiB PSS and 5.24GiB cgroup sampled peaks; 52% CPU average. Ordinary services and read-only operator source inspection were active, so this is not app-only idle CPU.

Observer cost is not zero: maximum outer collection 340.39ms, worker collection 35.10ms and guest metrics 24.57ms. The outer sampler's 25% CPU limit and smaps scans can affect measured timing. The idle run did not establish coding-task consumption or higher-concurrency capacity. Compare task intervals in a subsequent run, preserve this caveat, and report sampled peaks/counter deltas rather than subtracting whole-worker memory as if caches and other activity were constant.

The current matched snapshot records 2 CPU / 2GiB host allocation. A read-only Master sample reported 2,000mCPU / 2,048MiB used against 10,000mCPU / 10,240MiB quota. Five such allocations fit that arithmetic; the independent durable platform admission cap remains four. Historical 2.3 CPU / 2,160MiB estimates must not be presented as this guest's actual charge. No five-guest or coding capacity test was run here.

## Validation

```sh
python3 -m unittest discover -s ops/cube/workload/coding-profile -p test_profile.py -v
```

Six focused tests passed locally and on the outer Linux host before the idle observation. They cover PID-stat parsing, generation-aware CPU deltas, unavailable/mismatched metrics, framed bounded streaming, duration limits and the exact read-only task-metrics command. The idle unit completed with exit 0 and its worker SSH collector ended normally. Raw output remains private; repository evidence contains no prompts, transcript text, credentials or process environment.

## Analyze explicit task/build phases locally

`analyze.py` reads retained files only; it has no remote access or lifecycle calls. Supply actual task start/finish timestamps, including their UTC offset. Do not substitute a timeout deadline for an observed finish. Build boundaries are optional and must come from actual events; a build is reported as a subset of the task, not an additional independent workload.

```sh
python3 ops/cube/workload/coding-profile/analyze.py /private/samples.jsonl \
  --identity /private/sampler-identity.json \
  --task-id EXACT_TASK_ID \
  --task-start ACTUAL_START_ISO8601 --task-end ACTUAL_END_ISO8601 \
  --output /private/new-analysis-directory
```

Optional `--build-start ACTUAL_START_ISO8601 --build-end ACTUAL_END_ISO8601` adds the build row. Output is a fresh 0700 directory containing 0600 `analysis.json` and `table.md`, with exact input hashes. The JSON retains detailed measurements; the table is a compact comparison. It requires the task to appear in the scoped samples and rejects partial/oversized JSONL, reordered samples, changed PID/disk generations, clock rollback and regressing counters.

Only samples within each requested interval are used. CPU comes from consecutive counters within that phase, preserving the guest metric's native roughly five-second cadence. The output reports actual covered duration, missing boundary time, interval lengths, CPU seconds/average/interval peak, memory median/peak and net disk growth. Short phases with no guest samples report unavailable, not zero or interpolated estimates. A five-second interval peak cannot establish a shorter CPU burst's peak.

The JSON also includes guest shim RSS/PSS and cgroup anon/file/shmem/kernel components; process and per-device block I/O; memory-event/OOM and CPU-throttle counter changes; swap first/last/peak; kernel MemAvailable/cache; and measured cgroup CPU/memory PSI. System-wide worker `/proc/pressure` was not sampled and is not inferred. Cgroup `file` includes file cache and shmem; it overlaps other fields. Per-device I/O is not summed because stacked devices can count the same write. Memory/swap gauges can decrease, while cumulative event counters must remain monotonic.

Warm caches, retained PostgreSQL processes and observer overhead remain part of the measurements. The analyzer neither extrapolates fleet capacity nor labels all whole-worker changes as the coding app's consumption. Seven analysis regressions plus the original six sampler tests pass locally; the analysis was also applied successfully to all 61 real idle samples, reproducing the original CPU/PSS/disk result. No live coding conclusion is included without the completed task's actual timestamps.
