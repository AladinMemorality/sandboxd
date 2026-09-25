# Owned ordinary-socket acceptance

The fresh worker passed all four phases on2026-09-25: fresh guests, same-ID
pause/resume, replacement, and orderly worker OS shutdown/restart with both
guests paused. The report and exact tested binary identities are in `results/`.
All owned guests were deleted, with individual API404 and zero worker inventory
verified before the next fixture family started.

Each phase verifies app UID/GID1000, no effective/inheritable/ambient capabilities,
no-new-privileges, absent known host secret paths/environment variables, wrong
and cross-guest supervisor credentials, fixed-service broker authentication and
zero forbidden broker dials. Four explicitly owned destinations receive bounded
ordinary TCP attempts. Positive listener controls bracket each negative batch;
both host and sibling listener counts must remain unchanged. The sibling target
comes from its current immutable-ID binding immediately before the batch, and
the guest echoes the selected address. Reboots may legitimately change its IP.

There were eight denied socket attempts per phase and no inconclusive
unreachable results. Eight additional attempts using already occupied source
ports returned EADDRINUSE; those are explicitly inconclusive, not security proof.
This fixture performs no raw-packet transmission, BPF attachment/map mutation,
interface/address/route/firewall change, or probe of unrelated workloads.

Build `main.go` in a temporary command directory inside the control-plane
module. The operator provisions the scoped unprivileged listener and private
scope/credential files, confirms an otherwise empty worker, and invokes the
binary with the reviewed template and exact stage paths. On its restart marker,
the coordinator verifies both owned IDs paused, powers off the worker OS, starts
the outer VM unit, and restores only that run's synthetic listener. The fixture
waits for API readiness before sending any resume mutation. Other fixture
families and customer workloads must not overlap.

Earlier failures are retained in the private operator stage: a coordinator
timeout without restart, premature API use during boot, and rejection of an IP
that changed after resume. The final complete run passed after fixing those
fixture assumptions and the separately tested Master paused-detail bug. It did
not restore an earlier report or skip a failed security assertion.

This is one part of acceptance alongside the anonymous classifier tests. A
planned paused-worker restart does not establish recovery after loss of a running
worker or durability of later PostgreSQL writes. No production switch is implied.
