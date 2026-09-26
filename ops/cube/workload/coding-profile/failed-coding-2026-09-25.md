# Failed coding task observation, 25 September 2026

The task timed out after 600.416 seconds and changed only `search-query.mjs` and `server.mjs`. It did not complete the requested feature. No separate build was executed. This is an actual partial-coding observation, **not a successful coding/build benchmark or a concurrency result**.

| Phase | Guest memory median / peak MiB | Guest VM PSS peak MiB | VM cgroup file peak MiB | Guest CPU seconds / average / interval peak | RW net MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| idle_before_task | 120.16 / 120.32 | 560.50 | 593.36 | 0.027s / 0.13% / 0.25% | 0.00 |
| actual_task | 271.71 / 283.09 | 575.59 | 609.15 | 11.318s / 1.90% / 8.04% | 0.47 |
| after_task | 116.29 / 120.21 | 576.68 | 610.20 | 0.900s / 0.33% / 9.79% | 0.04 |

CPU percentages refer to one core. No build interval was supplied. Nested scopes must not be summed. See JSON for actual sample coverage, RSS/cgroup/cache, I/O, pressure, OOM/swap/throttle and collection costs.

Within the task interval, guest container memory peaked at 283.09MiB against 2GiB assigned. Its CPU counter increased by 11.318 seconds across 595.002 seconds covered by native five-second samples (1.90% of one core average). The guest VM's shim peaked at 575.59MiB PSS and 924.84MiB cgroup memory, including 609.15MiB cgroup file memory. These scopes overlap and must not be added.

Guest VM and whole-worker cgroups recorded no OOM, swap use, throttling, or memory-pressure stalls. Worker MemAvailable had a measured minimum of 36.186GiB. Guest VM CPU-pressure stall time increased 0.305 seconds over roughly 600 seconds, and whole-worker CPU-pressure stalls increased 2.247 seconds. These measurements show no sustained CPU or memory saturation explaining the timeout. They do not isolate the external model, model gateway, agent decisions, or network latency.

The current writable file grew 495,616 allocated bytes during the task; its 10GiB logical size stayed fixed. Shared snapshot/reflink blocks are not unique physical storage consumption. Guest VM process I/O increased 2,498,560 read bytes and 2,228,224 write bytes. Whole-worker writes also include services/telemetry; they must not be attributed wholly to application edits.

The sampler completed normally: 901 host/shim samples over 15 minutes. The task interval contains 601 host/shim samples and 120 guest-container samples. Host/shim coverage misses approximately 0.094 seconds at the beginning and 0.322 seconds at the end; native guest counters cover 595.002 seconds. The raw private files were independently copied and SHA256-verified. Exact hashes, timestamps, detailed distributions, pressure/event/I/O deltas and limitations are in `failed-coding-2026-09-25.json`.

Warm PostgreSQL and previous agent-session/cache state were retained. The observer itself had nonzero cost: task-interval outer collection median 278.93ms/peak 417.58ms and worker collection median 10.91ms/peak 39.87ms. CPU peaks are interval averages, not instantaneous maxima. After-task samples include cleanup/ordinary activity and are not a pristine baseline. No tasks, quotas or lifecycle state were changed by this analysis.
