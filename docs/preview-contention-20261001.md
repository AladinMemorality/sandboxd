# Source discovery blocked live previews

The page picker reads up to sixty source files. Each Cube file read previously
held the exclusive sandbox lifecycle lock through a remote supervisor probe and
file transfer. Preview activity registration needed the same lock. As a result,
small, unrelated JS/CSS requests could all wait for the page-picker queue.

On the reported SEO project, a controlled VPS browser load took 5.191 seconds
alone and 19.661 seconds with page discovery; small scripts waited about 18
seconds before their first byte. The user's screenshot showed the same clustered
wait at about 31 seconds plus page discovery at 36–48 seconds. These are separate
measurements; the reproduction establishes contention rather than identical
network timing.

Independent file, listing and log reads now hold shared lifecycle locks. Preview
activity registration also shares that fence. Writes, exports, pause, deletion,
and resume remain exclusive. A read that needs to wake a guest drops its shared
lock, acquires the exclusive lock, re-reads sandbox state and refreshes the runtime
client before proceeding. No authorization, admission or isolation check is removed.

The regression test holds two actual guest file responses open, verifies both
read requests reach the guest concurrently and preview traffic completes, then
checks a file write cannot reach the guest until both reads release their fences.
Existing owner-isolation and stopped-guest tests cover the same path.

`ops/cube/preview-contention/measure.mjs` reproduces the browser + page discovery
workload without editing application files. Run with the production environment
and an authorized sandbox ID. Its output omits credentials and query strings.
The guarded rollout preserves bindings, pending reservations and the worker-stop
controller pin, and rolls back to the exact prior image if activation fails.
