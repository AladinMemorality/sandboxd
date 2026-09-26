# Normal nonempty worker cycle accepted

At 15:36 UTC on September 26, the planned caller completed an ordinary
supervisor-controlled stop and restart of the production worker with its one
owned Cube binding. The scoped maintenance window was 156.38 seconds.

The supervisor produced its own `stopped-clean` receipt after native pause,
retained management shutdown, graceful powerdown and successful QEMU waitpid.
No external recovery authorization, status rewrite or forced power action was
used. Startup waited for retained management readiness, reconciled without
waking guests, recreated the controller/relays, and restored the complete
original live Caddy configuration and original writer states. The parent exited
zero; all four operation locks were independently reacquired and released.

- `cycle-result.json`: phases, current boot/controller, routing and fleet checks.
- `canary-after-cycle.json`: authenticated owned-project app/home bytes, modes,
  links, UID, committed SQL row, four failed task results/events, private capture
  access controls, and fresh screenshot all passed. No AI task was submitted.
- `tests.txt`, `result.json`, `source-manifest.json`: all 116 native-root Linux
  lifecycle tests passed, with no skips; exact tested source/archive hashes.
- `installed.json`: exact reviewed helper installation and previous hashes.

Private VPS inputs and journals are retained under
`/opt/baarcha-bench/cube-planned-review-20260926-01`,
`/opt/baarcha-bench/cube-planned-plan-20260926-01`,
`/opt/baarcha-cube/worker-01/maintenance/planned-20260926-01`, and
`/opt/baarcha-cube/worker-01/boot-transitions/planned-20260926-01`.
The completed journal's `locks_held:true` describes its final event while the
parent still held locks; the independent result records their actual release.

Fleet remains 73 apps: one owned Cube fixture and 72 Docker projects. No customer
migration, full paired backup, successful paid coding task, Motion UDS release,
controller replacement deployment or unattended host-boot acceptance is claimed.
The new boot/controller invalidate old execution plans; prepare fresh plans.
