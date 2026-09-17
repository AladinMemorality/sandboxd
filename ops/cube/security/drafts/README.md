# Unexecuted live-test preparation

These scripts were syntax checked only. They have **not** run against a guest and
are not evidence that the candidate worker enforces isolation. No worker was
replaced and no trial listeners or guests were started. The parent runbook lists
the remaining proof gates and the candidate's resolver mismatch.

`live_canary.py` serves a public sentinel on TCP/18081 and UDP/18082 inside the
disposable VM. `live-network-probe.mjs` attempts ordinary connections from a
non-root guest; `live_guest_trial.py` installs that workload into a pre-created
disposable guest and repeats it after pause/resume. It requires an explicit
`--allow-unvalidated-draft` flag. Its credentials file must be owner-readable only
and must never be archived. Output intentionally excludes tokens.

Before running, the operator must back up the original worker binary/config,
install a resolver-correct candidate, prove its programs attached, create two
disposable guests from the reviewed template, and identify their actual private
addresses. Verify canary listeners work from the VM itself. Approve only the
exact public registry and rebinding test domains in the test guest policy. The
configuration file needs `targets` with `ip`, `domain`, and `port`, plus
`workerIP`, `tcpPort`, and `udpPort`. Prove every test domain actually resolves to
its expected protected address; resolver rebinding filtering is not evidence of
the worker's hard deny.

The workload binds source port 3000 for forged-source-port tests. First move the
disposable guest's web listener to a different port in its manifest, preserving
the template's static exposed port 3000. Otherwise `EADDRINUSE` invalidates that
test. Preserve the manifest's `pilot-probe` worker command so `/config` restart
executes the replacement workload. The workload also requires guest Python3 for
the expected raw-socket EPERM check.

These drafts do not establish packet fragmentation/encapsulation handling,
forged raw TCP ACK behavior, worker restart/map recovery, or public management
IP blocking. IPv6 failure might reflect missing routing rather than the BPF
drop; distinguish this using packet-level evidence. TCP canary logs record HTTP
requests, not bare TCP handshakes; use packet capture or accepted-connection
logging for a complete witness. Do not treat absent log entries alone as proof.
Always clean up only the newly created guest IDs/listeners and restore the
original worker, preserving any existing paused benchmark guests.
