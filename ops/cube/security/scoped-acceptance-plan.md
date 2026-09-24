# Proposed bounded isolation acceptance — not executed

Status: preparation only, 2026-09-24. No acceptance attestation or deployment
permission is conveyed by this document. An earlier automatic review rejected
extended/raw-packet isolation trials; the original rejection text and rationale
are unavailable. This proposal does **not** authorize retrying those scripts or
bypassing that rejection. The coordinating operator must submit the concrete
stages below for a new review before execution. Any separately rejected stage
stays unexecuted; passing another stage does not substitute for it.

## Intended production boundary

Keep guest NIC egress denied. Public HTTP(S) and fixed model/bridge calls use the
authenticated, host-initiated reverse channel; never add direct domain
allowances or an opaque management-host CONNECT exception. The acceptance scope
is the exact reviewed worker, kernel, template, control-plane broker and protected
address inventory. Screenshot workers are a separate service and are excluded.

The September23 [handoff](results/2026-09-23/HANDOFF.md) records a real bypass in
the first candidate: gateway/static exposed-port reply branches admitted forged
non-SYN traffic. Patch0002 adds tuple/generation-bound permissions established by
ingress SYN. Its 87 attached-program cases passed, but ordinary endpoint trials
failed at DNS setup without listener positive controls. That is incomplete
evidence, not production isolation approval.

Read-only inspection on September24 found the running executable at
`/usr/local/services/cubetoolbox/Cubelet/bin/cubelet`, SHA256
`7bc2c2e7290e1ba62d0648441388ac11da9f046134683f44da2f8d20a346fa6b`, matching the
second candidate. The current `from_cube` program was ID22/tag
`79efd5f73727f0f7`. IDs can change after reboot, and program tags alone do not
establish complete attachment/map ownership. Current attachment provenance
must be freshly recorded. The nested VM also has active CoreDNS and inherited
cube-egress/TPROXY services. The previous resolver failure is historical, not a
claim that CoreDNS is currently missing.

## Findings to resolve before execution

1. Pin both patches, generated policy header, executable, template, kernel and
   actual attached programs/maps as one reviewed artifact set. An executable
   hash alone does not prove what is attached to every guest interface.
2. Confirm the candidate's three program paths share the same reply map. The
   patch tracks ingress in localgw/nodenic and checks mvmtap; an anonymous-map
   unit test cannot establish that runtime wiring.
3. Prove legitimate ingress replies work before interpreting denied connections.
   Prove each synthetic listener accepts an authorized control connection both
   before and after the negative batch. Record accepted connections, not only
   completed HTTP requests; a failed HTTP response is not necessarily a denied
   TCP connection.
4. Cover same peer/source/guest port on two different guest interfaces,
   generation invalidation, stale tuple reuse, missing map state and expiry.
   Patch0002 is a reply permission cache, not complete TCP sequence tracking:
   FIN/RST do not remove entries, and accepted outbound replies refresh its
   one-hour idle timestamp. Do not claim a hard one-hour lifetime or established
   TCP-state validation. Whether this is acceptable requires explicit review
   of both ingress attribution and the host TCP boundary.
5. Do not inherit template network allowances or CubeEgress L7 rules. The
   reverse broker is the sole approved application egress path. Inventory the
   active transparent-proxy services; presence alone neither proves use nor
   proves a bypass. Any necessary service/routing changes require their own
   reviewed disposable-environment setup, never changes on production.
6. Ordinary IPv6 `ENETUNREACH`, a DNS timeout, or an absent canary log cannot
   establish a classifier rejection. Keep those verdicts inconclusive without
   a corresponding valid positive control and enforcement evidence.

## Exact resource and execution envelope for review

- Use a fresh dedicated disposable worker VM, or a restored clean reviewed
  snapshot whose complete topology is recorded. No production worker/project,
  existing trial guest, or unrelated paused guest may be changed.
- Before execution, produce `scope.json` containing exact immutable VM identity,
  image/template/binary/header/patch hashes, kernel release, **two new** tagged
  guest IDs and assigned interfaces/IPs, and listener bind addresses. An empty
  or unresolved address is an abort, not an invitation to scan a subnet.
- Two guests maximum, each one vCPU/1 GiB RAM, UID1000, no added capabilities.
  One host sentinel service, ports18081 and18082, plus guest B's synthetic HTTP
  listener on3005. All listeners return only a random run marker, accept no
  writes, cap requests at1 KiB, and store only run ID/monotonic time/accepted
  connection counts. Do not mount project directories or secrets.
- Host sentinel addresses must already belong to this dedicated VM's reviewed
  topology. A metadata/link-local alias or extra namespace/listener is a setup
  mutation and must be explicitly listed and reviewed before creation. Never
  bind metadata aliases, listeners or firewall rules on the outer VPS.
- Test destinations are those exact synthetic listeners only: dedicated
  worker private address, dedicated worker management address if distinct,
  reviewed synthetic metadata alias if present, and guest B. Never probe real
  cloud metadata, production host/API/database ports, other tenants, Internet
  address ranges, or unrelated services. Port/path changes are not authorized.
- At most256 ordinary connection attempts for the entire run, at most two in
  flight, at most four starts/second, three-second connect deadline and
  five-second total request deadline. Overall deadline20 minutes including
  cleanup; reserve the last two minutes for cleanup. No automatic retries
  beyond the explicit repeat phases below. Stop on unexpected acceptance,
  invalid controls, scope mismatch, lost cleanup ownership or resource limits.
- Credentials remain in owner-readable transient files; reports contain no
  bearer tokens, signed URLs, environment dumps or packet payloads.

## Proposed stages and acceptance criteria

### A. Offline deterministic coverage — preparation is permitted

Run `python3 ops/cube/security/test_hard_deny.py` and
`python3 ops/cube/security/test_reply_permissions.py`. The second test extracts
the actual header from patch0002 and uses fake maps/time: legitimate SYN/reply,
missing permissions, SYNACK without grant, cross-guest/peer/port mismatch,
generation change, idle expiry, absent guest metadata and failed map update.
It additionally documents outbound refresh behavior. These tests transmit no
traffic, load no BPF, and do not attest the deployed worker.

The separate [22-case manifest](scoped-kernel-cases.json) proposes at most88
program-test executions over120 seconds for review:
fresh anonymous maps only; no attaching, existing map writes, raw sockets or
packet transmission. Required cases are the reply positives/negatives above,
hard-deny before cached allow state, fragmentation/non-IPv4 rejection, and
gateway/exposed-port branches. **Do not execute this kernel stage under the
current permission** or invoke the old extended/attached packet scripts.

### B. Ordinary synthetic endpoint controls — proposed, not run

1. Operator control connects to both host sentinel listeners and guest B's
   endpoint. Record successful accept+marker; demonstrate authenticated
   host-initiated guest health/preview and reverse-channel responses.
2. From guest A, use ordinary unprivileged sockets (no crafted flags) to each
   exact protected sentinel address. Bind source ports3000,3031,49983 and an
   ephemeral port where those ports are free. Move only the test manifest's
   listener if needed; `EADDRINUSE` is invalid evidence. This is at most16
   direct TCP attempts per lifecycle phase. Require connection rejection and
   zero attributable sentinel accepts, with positive controls before/after.
3. Verify authenticated reverse-channel HTTP and fixed callback responses using
   the synthetic host sentinel as an explicitly supplied **test callback**;
   this does not add a public proxy/private-IP exception. Verify missing/wrong
   guest capability fails. No model/gateway request or billable task is needed.
4. Test broker DNS policy using ordinary local test fixtures and injected
   resolver results, never by changing system DNS: public→protected answer,
   mixed public/protected answers, new-stream re-resolution, numeric protected
   hosts and protected-name aliases. Assert no forbidden dial and cancellation
   closes streams. This proves broker logic, not DNS learning in the NIC path.

Direct DNS learning/rebinding need not be enabled for a deny-all-NIC deployment.
It must remain a separate unaccepted gate if direct domain egress is proposed
later. A registry/public-network success control is outside this synthetic-only
proposal; the existing actual client acceptance record must be reviewed
separately, or an exact public destination must receive separate approval.

### C. Lifecycle continuity — proposed, separately review worker restart

Repeat B's endpoint controls after one guest pause/resume, after deleting and
replacing guest A, and after one dedicated worker restart. No production service
restart is included. Check reconnect, fresh credentials/generation, old channel
closure and sentinel counts. Do not force interface reuse or manually alter
worker maps: that belongs to the deterministic case manifest. At most four
ordinary phases and64 negative TCP attempts; remaining256-attempt budget covers
positive controls and bounded channel cases. Fresh-map boot/restart should fail
closed until genuine host ingress re-establishes reply permissions.

### D. Required packet-path review — remains unexecuted

Ordinary socket success/failure cannot rule out the previously observed forged
ACK path. The final reviewer must decide whether the finite non-transmitting
kernel cases plus complete attachment provenance and ordinary controls provide
sufficient evidence, or approve a separate narrowly scoped dataplane test.
Do not silently rename/re-run the rejected extended/raw-packet trial. If the
reviewer requires a raw/transmitted case, write its exact count, destination,
interface, expected effect and cleanup as a new proposal; no such action is
authorized here. Until this review is satisfied, production isolation remains
unaccepted regardless of functional app benchmarks.

## Cleanup, evidence and production decision

Cleanup owns only the recorded run IDs: cancel broker/test processes, delete
exact new guest IDs and confirm GET404, stop exact sentinel PIDs, remove only
owned aliases/namespaces if their setup was approved, and verify no matching
containers/listeners remain. Use a fresh bounded cleanup context after a test
timeout. Any ownership uncertainty or cleanup error fails the run; preserve
unrelated resources. Save sanitized before/after inventory and test counters.

The result must list pass/fail/inconclusive for every case, positive-control
outcomes, attachment/map provenance and hashes, request count/time bounds,
cleanup verification, and all exclusions. Passing the disposable stage does not
automatically enable production: the final deployment must match reviewed
artifacts/topology/protected addresses and receive an explicit cutover decision.
No script in this plan sets `NETWORK_VERIFIED`, changes global admission, or
claims that a document hash is an enforcement attestation.

## Concrete next review request

Review the populated resource manifest and approve or reject **only** stages B
and C on the dedicated disposable worker with the bounds above, plus the
separately enumerated 22-case/88-execution maximum anonymous-map,
non-transmitting kernel manifest in A.
The original rejection reason is unknown; approval cannot be inferred from
elapsed time or from this written proposal. Production enablement, raw packet
transmission, direct domain allowances and production network mutations are
expressly outside this request.
