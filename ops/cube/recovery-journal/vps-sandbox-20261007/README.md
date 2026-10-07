# Brandish Creative Lab VPS recovery, 2026-10-07

Incident-specific companion to the Derja operator. It preserves sandbox
`01M3HH7PZJRPKHZ9GE3NRC37F7` and its existing app, owner, configuration and completed
task. The source worker is `vps`; this is not a relocation to B200.

The native runtime disappeared on an unchanged worker boot. Capture uses the
individual-runtime fence with the exact source inode, no native task or open disk
handles, and controller lifecycle mutations disabled. Original storage remains
retained. The isolated rescue exporter and bounded archive validators precede
conversion; guest files are never extracted onto the controller host.

Canonical task history contains only the existing owned task's `events.jsonl`
and terminal `result.json`. The Go importer validates its ID and content contract,
then re-exports and compares each file's digest before the journal can commit.
Old supervisor credentials remain in the private recovery archive; the replacement
uses fresh credentials. Workspace and reviewed home files are independently
imported, re-exported and compared as well.

Private inputs and receipts live under
`/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007`. The operator requires
native host root, the retained management relay's network namespace, exclusive
deployment and worker locks, and the source worker's complete admission policy.
Run `validate` and `preflight` before `run`. An unfinished journal keeps the
controller fenced; never repeat an ambiguous provider Create. `continue` accepts
only the exact acknowledged replacement already recorded by that journal.

Production acceptance is recorded separately after live preview checks.

Worker SSH runs explicitly in the native host network namespace (`nsenter -t 1 -n`).
Stopping the controller removes its veth route even while the relay namespace
survives. The retained loopback relays continue to reach their fixed host Unix
sockets; only provider calls use that namespace. SSH lease cleanup is bounded.
