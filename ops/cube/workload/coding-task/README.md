# Real coding measurement, September25

This is a distinct performance measurement on the existing operator-owned
Node/PostgreSQL project, with its files, database and failed task history retained.
The actual task adds server-side notes search, input validation, UI states and
focused Node tests. It uses the same configured agent/model/thinking settings as
the deployed platform, with an explicit600-second runtime limit. It does not
install dependencies or perform a frontend bundle build; those are separate
workload profiles and must not be claimed from this result.

The earlier recovery fixture forced `MAX_THINKING_TOKENS=0`, whereas the platform
defaults to2000 and omits the variable when its setting is zero. Its last300-second
timeout included24 matching tool results arriving within one second. That differs
from normal task configuration and does not establish a sandbox CPU/RAM problem.
The model's receipt of every upstream request field is not directly observed.

Before running, finish the exact read-only idle baseline using the coding-profile
sampler. Start a separate bounded sampler covering idle, task execution, test
commands and cooldown; retain its process-identity pins and task timestamps.
No customer workload or resource quota is changed. Guest container accounting,
whole guest shim RSS/PSS, outer QEMU memory/cache, CPU time, disk I/O and sampled
peaks are different measurements and must be labeled separately. In particular,
guest cache is unavailable from the current Stats adapter, not established zero.

`run.py` imports only SHA-pinned existing lock helpers, holds all four outer
operator/deployment locks plus the nested acceptance lock, and runs one bounded
Node child. The private config must include current exact app/sandbox/controller,
controller image, platform revision, worker boot ID, new private stage, this task
script and prompt SHA256, and the installed reviewed lock wrapper/helper paths
and SHA256. It is intentionally tied to the current pre-upgrade controller and
three independently verified terminal tasks. Source or production changes require
fresh review. No example config supplies authorization or fabricates pins.

The Node child verifies owner103, the exact project bridge, and at least1500
millimes of actually available operator credit, including pending usage. It grants
no credit. It saves a durable submission intent before exactly one task POST.
Unknown outcomes retain that intent; known terminal failures remain failures.
There is no task replay or automatic deletion. The earlier canonical recovery
journal remains untouched. The credit allowance is not a hard spend cap.

Root must independently verify the feature, test output/checkpoint, rendered page
and phase-aligned telemetry after completion. A completed task alone is not full
application acceptance, a higher-concurrency result, or a global migration gate.

Local checks:

```sh
node --test ops/cube/workload/coding-task/test_task.mjs
python3 -m py_compile ops/cube/workload/coding-task/run.py
```

Five Node tests cover exact terminal history, normal thinking settings, durable
submission intent, lost acknowledgement and the cancellation watchdog.
