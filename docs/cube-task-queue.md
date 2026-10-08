# Cube coding queue

`SANDBOXD_CUBE_TASK_CONCURRENCY` controls simultaneous coding requests per
worker in the Cube controller. Zero (the default) preserves direct submission.
A positive value enables a durable queue for workers with a resource budget;
the effective limit cannot exceed that worker's runtime slots. Dedicated
builder-VM reservations do not cap coding jobs in existing app VMs.

Choose this setting from workload measurements. The VPS's successful 50-app
serving test does not establish capacity for 50 complete coding-agent sessions.
The first 50-build burst was stopped by the host memory-pressure guard before
completion. Production agent-load testing is deferred; no two-agent limit has
been enabled by this change.

Requests return HTTP 202 with `status: queued` and an events URL. They do not
wake a sandbox until claimed. Each project has at most one queued or running
request, and each worker holds at most 100 queued/in-flight dispatch records.
A request's environment is encrypted at rest using the existing secrets key.
Cancelling a queued request removes that payload and saves a terminal result;
replaying it does not contact the guest. Messages to waiting requests return
`task_queued`; cancel and resubmit to change the prompt before it starts.

Claims count toward concurrency before preparation starts. The dispatch
checkpoint precedes the guest RPC. Lost responses remain charged until guest
reconciliation establishes the outcome; the controller never blindly retries
an ambiguous submission. After a restart, only pre-dispatch claims return to
the queue, with old claim tokens invalidated. Five failed preparation attempts
save an explicit failure without fabricating a guest event log.

Task history retains dispatch timestamps and whether a result has no guest
log. Completion and encrypted-payload deletion are atomic. Stream cursors are
preserved when a waiting request starts. Offline worker maintenance and runtime
relocation treat queued requests as active work.

Configure this on the single active controller for the worker. Drain pending
requests before setting the value back to zero or rolling back to a version
without queue support. Existing running requests continue to count after a
limit reduction. Validate submission, cancellation, reconnect, restart recovery,
and the platform UI before enabling it in production.
