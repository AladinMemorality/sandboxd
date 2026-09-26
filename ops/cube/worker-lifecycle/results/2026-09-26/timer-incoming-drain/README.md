# Timer semantics and incoming-request drain

The second actual maintenance attempt failed before stopping the proxy,
controller or worker: systemd timers do not expose `MainPID`. The caller now
requires loaded/inactive timers and separately requires explicit `MainPID=0`
for loaded/inactive services. `KeyError` is no longer retried as a transient
condition, so a missing field fails immediately into the private pending journal.

The old controller condition also counted idle outbound Cube API keepalive
connections as undrained requests. `control-plane/cmd/sandboxd/main.go` routes
API and previews through the same HTTP server (around lines 637–653), then
cancels its background context and calls HTTP Shutdown (around lines 898–903).
HTTP Shutdown excludes hijacked WebSockets. Before shutdown the corrected
caller therefore requires a stable exact-process socket sample, an existing
listener and zero non-listening connections whose local port matches a current
listener. This includes accepted HTTP and hijacked preview sockets. It does not
require the outbound provider connection pool to disappear first. The existing
observer retains outbound socket counts in evidence rather than calling them
requests or dropping their observation.

Task and provider-job checks still precede normal shutdown. Actual stopped
PID0/exit0/no-OOM and logged normal HTTP shutdown remain required. Afterward the
caller again verifies current task/thumbnail/env state, exact terminal provider
counts and canonical bindings, saving the result before proceeding. This is
not a claim that a socket observation alone proves quiescence.

Native root Linux unit `cube-boot-python-20260926-12` passed 105 tests in 1.939s,
zero skips, under 2 CPU / 256 MiB / 32 tasks / 120 seconds. Added tests cover real
timer field shape, strict service PID, immediate KeyError, idle outbound versus
incoming/WebSocket connections, missing/changed listeners, and post-shutdown
provider/binding check order. No live worker, controller or routing operations
were performed. Installation and actual recovery remain separate parent actions.

- Maintenance SHA: `96b4313fd2d3bba645221b919e0861becb91d010efe70cd8c37ce6002f977a5a`
- Archive SHA: `37c456f3757bb19d4ce932cb446f2cf8eb694162adba0ae48a3d98f34f607ecb`
- Tests SHA: `9e47e616fc26ad550c8d77076a8304d3a0f88649546080cc88c03957c17b250f`

Exact source and outputs: `/opt/baarcha-bench/cube-boot-python-20260926-12/`.
