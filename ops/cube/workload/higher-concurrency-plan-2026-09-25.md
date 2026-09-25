# Proposed six, eight and twelve active-app measurements

Four is the currently tested operating point, not a measured hardware maximum.
No quota, admission policy, guest or service was changed for this plan. It uses
the recorded worker identities/results and current source; do not mistake it
for a fresh live capacity sample during the controller cutover.

The strongest current comparison is the guarded four-app run
[`guarded-live-2026-09-25.json`](guarded-live-2026-09-25.json): 18.592 seconds to
fully touch 512 MiB in each guest, then 45.335 seconds steady work, HTTP p95
11.583 ms, supervisor p95 4.224 ms, no OOM/swap/throttling, and verified cleanup.
The older twelve-app run failed the unchanged 20-second first-touch bound;
it never reached the steady interval. Its roughly 17 GiB QEMU memory sample was
not a memory exhaustion result. The later freshly booted four-app peak was
7.43 GB; cache/history and phase differences prevent treating those peaks as
per-app RAM or a direct scaling curve.

## Resources and policy that must remain distinct

| Candidate active count | Per-app quota | Minimum accounted native CPU | Proposed native CPU quota | Accounted native memory, approximately | Proposed native memory quota |
| --- | --- | ---: | ---: | ---: | ---: |
| Current 4 | 2 CPU / 2 GiB | 9,200 mCPU | 10,000 mCPU | 8.44 GiB | 10 GiB |
| Test 6 | 2 CPU / 2 GiB | 13,800 mCPU | 14,000 mCPU | 12.66 GiB | 14 GiB |
| Test 8 | 2 CPU / 2 GiB | 18,400 mCPU | 19,000 mCPU | 16.88 GiB | 18 GiB |
| Test 12 | 2 CPU / 2 GiB | 27,600 mCPU | 28,000 mCPU | 25.31 GiB | 30 GiB |

These native values are scheduler accounting limits, **not dedicated CPUs**.
Pinned Cube accounting charges about 2,300 mCPU and 2,160 MiB per reviewed
2-CPU/2-GiB template. Verify actual `ResourceWithOverHead` before each test;
a different matched snapshot can alter the result. Keep `mvm_limit=128`,
`creation_concurrent_num=1`, and paused release ratio 1.0. Each proposed CPU
quota also refuses the next guest by native accounting, independent of the
fixture's N-slot ledger.

Keep the worker VM at 40 GiB and 12 vCPU, outer `MemoryHigh=42G`,
`MemoryMax=44G`, `MemorySwapMax=0`, `CPUQuota=1000%`. The host has 6 physical / 12
logical CPUs and about 62 GiB RAM. Motion Studio's separate 6 GiB / 250% CPU
limit must remain accounted for: ten worker CPU-equivalents plus 2.5 Motion
CPU-equivalents already exceed twelve logical CPUs before the platform and
other Docker work. No extra CPU is created by increasing native mCPU quota.
44 + 6 GiB leaves approximately 12 GiB for host, platform and other workloads
at their ceilings; their measured RSS alone does not reserve that headroom.
Do not raise the QEMU cgroup limits to make this test pass.

## Required preparation and execution prerequisites

1. The benchmark follow-up now extends only the **synthetic fixture** slot validator in
   `control-plane/internal/store/cube_workload_live_linux_test.go` from {4,12}
   to {4,6,8,12}, with validation tests. It preserves 512 MiB per app, 4 MiB touch
   chunks, 20-second preparation, 45-second steady work, 5-second request
   deadlines, one-shot activation, automatic 60/80-second stops and bounded
   cleanup. Record a new binary/source hash; retain the historical ccfe and
   guarded c485 binaries/results unchanged.
2. Keep real storage-observer enforcement in each fresh private admission DB.
   The library admits up to twelve, but production
   `AdmissionConfig.RequireStorageGuard()` explicitly caps four, and the
   enrollment renderer does too. Do not relax production guards for a test.
3. The fixture's private ledger is **not shared with the live controller**.
   The new `run-stage.py` refuses to execute unless the exact controller is
   actually stopped; `CUBE_ENABLED=false` does not fence existing Cube bindings.
   A new optional baseline preserves one exact paused canary, pinned by provider
   fingerprint, resources, app identity and private config/marker receipts.
   The default mode still requires empty provider inventory.
   Once the owned-app canary is connected, require a reviewed Cube-only
   admission hold and drain before this fixture; a flock by itself does not
   stop browser/controller wake requests. Preserve the canary's app identity
   and all customer Docker services. Require no running Cube guests/tasks/jobs
   and no uncertain admission before allocation; only the exact paused baseline
   may remain. If that cannot be achieved
   safely, defer the test rather than combining two independent ledgers.
4. Under the reviewed operator handoff, apply only the selected native quota
   at an empty/drained worker, preserving the exact config and prior quota.
   Verify the worker reports that quota before allocating. Restore the current
   four-slot quota after each test unless a separately reviewed next stage is
   ready. Never rewrite/delete durable admission rows to raise a limit.

## Sequence and acceptance

Run 4 as a phase-instrumented baseline, then 6, then 8; only attempt 12 after
the preceding stages pass. The historical twelve-app failure remains a failure.
For each stage, record all attempts and perform two runs: the ordinary initial
run and a repeat after verified cleanup, without dropping host caches or
rebooting production to manufacture a favorable baseline. Tag the cache/boot
conditions explicitly. No concurrent builds, screenshots, migrations, template
builds or other synthetic load should overlap. Motion and normal platform
services stay under their current limits and are observed, not stopped.

The primary gate keeps the existing workload exactly comparable:

- Every selected app fully resident and checksum-verified by 20 seconds.
- All guests remain the same processes and make progress for 45 seconds.
- No HTTP/supervisor timeout/error; retain each sample. Proposed additional
  usability gate: both steady p95 values <=250 ms, maximum <=5 seconds.
- N+1 refused by the durable guard before an upstream create, with no extra
  provider guest or leaked debit. Do not label a disk-reserve refusal as an
  active-slot ceiling result.
- Zero OOM/max events, no swap use, and cleanup verified independently through
  API, complete Master inventory, worker task list, and charged reservations.

Then run separate representative 2-CPU/2-GiB workloads at the largest passing
stage: frontend cold dependency/reload, PostgreSQL writes plus requests, and
parallel ordinary backend requests. Measure wake/connect-to-ready, HMR/reload,
SQL/read correctness and end-to-end latency. The 512 MiB / 5%-CPU synthetic
profile cannot certify simultaneous package builds or maximum 2-GiB app demand.
A concurrency setting is accepted only for the workloads actually tested.

Keep preparation and steady samples separate. Capture one-second outer and
worker CPU/memory/pressure samples plus phase timestamps; include QEMU and
owned VMM/Node process minor/major faults and user/system CPU when available.
Report CPU throttled periods/time, host CPU run queue, inner/outer page faults,
`memory.stat` anon/file components, available RAM, and Motion/platform latency.
This can distinguish deferred snapshot/COW page faults from CPU contention or
memory pressure; the older evidence did not isolate those causes. If six fails
first-touch but warm requests remain fast, report that distinction and diagnose
it before increasing preparation deadlines. A separate staggered-start
experiment is useful but must not replace the failed simultaneous result.

## Abort bounds and disk budget

Keep the runner <=2 CPU / 2 GiB / 128 tasks with its finite run and cleanup
contexts. Before each stage require at least 12 GiB outer and 12 GiB worker
`MemAvailable`; also inspect headroom for the **remaining** possible QEMU and
Motion growth, not only current RSS. Proposed stronger runtime aborts than the
old fixture: outer available <8 GiB or worker available <6 GiB, any OOM/max
increment, any swap use, or sustained memory-full PSI >1% for ten seconds.
Abort load and clean only recorded synthetic guests. At >=40 GiB QEMU
`memory.current` stop the stress attempt before the 42 GiB high watermark;
retain the exact sample/counter history. Repeated public-platform health
failure or its controlled probe latency exceeding one second for three
consecutive samples also ends the attempt. These monitors are reactive bounds,
not a guarantee that unrelated workloads cannot cause host pressure.

The physical data filesystem is now 448 GiB after the recorded host cycle;
use actual `statfs` free space and the installed observer for every run. The
existing guard's arithmetic remains conservative for a higher **test** count:
12 GiB per charge (10 GiB disk + 2 GiB RAM snapshot), no epoch refunds, 48 GiB
reserve, and 96 GiB baseline on both inner and outer filesystems. From an empty
ledger, admitting all N within an epoch needs at least:

| N | Minimum observed free for full N |
| --- | ---: |
| 4 | 96 GiB |
| 6 | 120 GiB |
| 8 | 144 GiB |
| 12 | 192 GiB |

Recent released grants and prior in-flight charges may increase the requirement;
a fresh observation does not erase work still capable of writing. Keep the
65% native filesystem filter as a second independent gate. Full fleet paused
snapshots, originals and backup staging consume disk even when active count is
small. Do not remove historical orphan/recovery artifacts to improve a result.

## Converting a successful measurement into a higher production limit

A pass does not change production. `Store.AdmissionPolicy()` pins its installed
maximum/profile and rejects a different process-local configuration. Raising
production capacity needs an explicit transactional operator transition with
charged/pending inventory checks, reviewed production guard/enrollment changes,
matching stop/start coordinator config, matching native quota, rollback to a
safe lower limit after drain, and deployment tests. That transition is not
implemented by this plan. Select the largest stage with repeated representative
passes and meaningful headroom; do not extrapolate that all fleet projects can
run at once or that four is the host's permanent maximum.
