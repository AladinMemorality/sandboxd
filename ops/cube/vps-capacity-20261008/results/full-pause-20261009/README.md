# VPS memory snapshot candidate and third source recovery

Two independent paused app restores panicked in guest kernel list handling.
Their previous pauses used incremental anonymous-page capture. A third app's
main process exited immediately after restore. No worker or outer-host OOM was
observed. This establishes failed restore behavior; it does not establish the
underlying cause of each failure.

The latest affected app, `01M2AR44CA9AW2FEFA44KVEAGQ`, was recovered from a
fresh, verified source archive with seven historical tasks. Its original disk,
snapshot and failed operation journal remain retained. The replacement passed
module checks and three stop/wake cycles. A separate receipt resolves the failed
density test's cleanup without overwriting its failure record.

Patch `0014-full-pause-memory.patch` uses the VMM's existing full-memory dump
path for pauses, regardless of the suggested incremental strategy. Commit
snapshots are unchanged. The candidate is built from the exact source manifest
of installed Cubelet `de3bd4c1...`, preserving its durable metadata, storage
retention and network patches. Only the pause configuration and its test differ.
The protected embedded BPF objects retain their exact bytes.

The candidate built and passed race-enabled cubebox, metadata, durability,
recovery and focused storage suites. These are pre-deployment results. They do
not prove live recovery or the 50-app capacity target.

The prepared deployment drains mutations, preserves the controller database,
backs up native metadata after a graceful Cubelet stop, updates the binary pin,
and verifies surviving guest process identities. It does not restart the worker
VM or guest VMs. A live user task gates deployment. The prepared canary requires
three restores from new full checkpoints and actual preview module responses.
Old incremental checkpoints can still fail on their first resume.

All work here is VPS-only. No model calls or coding-agent load test were made.

## Deployment follow-up

The first StopUnit attempt queued stops for Cubelet's two Requires dependents.
Their retained-stop hooks blocked the cascade; the original Cubelet and guest
processes remained running. The operation was aborted and production restored.
The egress container was restarted during recovery, but no guest VM was.
An existing network-start check expected `local 0.0.0.0/0`; Linux printed
`local default`, causing a duplicate-route error. Patch 0015 accepts both exact
forms, and the network service passed its real startup check.

A transient systemd witness verified that graceful main-process exit preserves
Requires dependents without BindsTo. The second rollout uses a pidfd and pinned
main binary, keeps the normal stop hook after maintenance, and checks guest and
egress process identities. It deployed full-snapshot Cubelet `43d8c020...`,
preserving all 136 app bindings and the one remaining active guest. Native
metadata and the previous binary are retained. Controller management proxies
are explicitly reattached after its container starts.

The live canary passed: three restores from new full checkpoints each served
23 modules without OOM, in 1.11–1.23 seconds per wake. Cubelet Info messages were
not present in its configured file logs, so the initial log assertion failed.
The original failure remains retained. Timestamped Shim records and VMM Full
capture/dump records independently verified all four checkpoints and linked the
three new restores to them; no test cycles were repeated to produce this proof.
The 50-app acceptance remains separate required evidence.
