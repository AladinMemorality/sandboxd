# Full backup aborted; production restored

The full-home archive was too slow on the source disk. The scoped maintenance
window lasted 1008.03 seconds and ended at 17:03:14 UTC. The incomplete archive
was retained. No Cube worker stop, cold disk pair, accepted full backup or
customer migration occurred.

The recovery continuation duplicated the original parent's four exclusive lock
descriptions before stopping its exact archive subprocess. It restored the
unused STOP configuration by comparison with the journaled original, started
Motion, restarted the same controller and its management relays, and restored
the original Docker states, restart policies, timers and complete routes. Only
after readiness passed was the idle failed operator parent terminated. The
worker and customer processes were not forcibly stopped and source data was
not rolled back.

`restoration-result.json` independently verifies service restoration, both
authenticated Motion listeners, unchanged source/configuration/limits, actual
native readiness and release of all operation locks. `canary-after-reopen.json`
records the authenticated owned fixture's successful file/home/metadata, SQL,
task-history, preview and private-capture checks at 17:05:56 UTC.

The fleet remains 1 Cube fixture and 72 Docker projects. No paid task or credit
grant occurred. The next backup needs an online bulk pre-copy with a verified
closed-source delta; neither a live cache nor these restoration checks qualify
as full recovery acceptance.
