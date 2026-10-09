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
