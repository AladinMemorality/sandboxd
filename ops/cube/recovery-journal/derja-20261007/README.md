# Derja current-disk recovery, 2026-10-07

Exact incident operator for the lost B200 native runtime. It preserves the
stable Baarcha project and sandbox, source disk, controller backup/key and original
archives. It uses the existing offline journal with the source B200 admission
partition; it cannot create a second replacement for an uncertain operation.

Before running, capture the latest current disk under the worker/operator fences,
export home inside the networkless rescue VM, validate and convert its reviewed
scope, and independently recheck no original task or open disk handle remains.
The operator requires zero task history and config revision zero for this exact
app. It imports then re-exports both archives and compares canonical digests,
applies frozen config and verifies the authenticated frontend before committing.
Private artifact/config paths are under the fixed VPS operations directory.

Build main.go inside a temporary command directory in the control-plane module.
Run as host root with only the network namespace of a retained management relay,
so native provider endpoints are reachable while the controller is fenced.
`validate` and `preflight` precede `run`; never replay a provider create after an
ambiguous acknowledgment. `continue` accepts only the exact journaled target.
A failed unfinished journal keeps the controller fenced for explicit recovery.

This is an incident-specific operator, not a generic unattended migration tool.
Production result is recorded separately after actual import and browser checks.
