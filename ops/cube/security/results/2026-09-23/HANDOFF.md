# Incomplete isolated worker trial, 2026-09-23

Work stopped at the coordinating agent's request. **No enforcement attestation,
production rollout, or sandboxd domain-egress enablement was performed.**

The disposable nested VM on the VPS was started. The upstream v0.7.1 candidate
was rebuilt using the observed resolver `169.254.254.53` instead of `10.0.2.3`.
Its SHA256 was `a2ad1c5fda91a3468c4b770f96eb48090ab89ae8314a3047033c712ce9fb30a8`.
Kernel classifier/cached-session and full collection verifier tests passed.
The actual installed worker's programs changed: `from_cube` ID25/tag
`2b8137540c4a97a8` became ID117/tag `1461a96be9cec5a1`. The original binary and
configuration were backed up before replacement; only the nested VM was changed.

## Finding and second candidate patch

The first candidate still allowed forged non-SYN traffic through gateway and
static exposed-port reply paths. The corrected packet report shows 48 of 87
crafted cases redirected toward protected destinations. Reset packets redirected
back to the guest were correctly treated as denial, not as outbound success.

`../../0002-track-inbound-replies.patch` adds a worker-pinned LRU reply-permission
map, keyed by guest interface, peer IP/port and guest port. A real ingress SYN
creates permission. Reply checks require matching guest generation and a live
timestamp; the idle limit is one hour. This separates guests despite the
upstream port-mapping key's shared inner guest IP. Fragmented guest IPv4 is
explicitly dropped before transport headers are interpreted.

The second candidate built and passed kernel verifier/classifier tests. Its
installed binary SHA256 is
`7bc2c2e7290e1ba62d0648441388ac11da9f046134683f44da2f8d20a346fa6b`.
Its attached `from_cube` program was ID177. All 87 crafted cases passed using
`BPF_PROG_TEST_RUN` against this **actually attached program and live guest maps**.
This is not raw packet transmission through a guest NIC. Cases cover private,
gateway, public-management, metadata and sibling destinations; source ports
3000/3031/49983/random; SYN/ACK/SYNACK/RST; fragments; IPv4 encapsulation/GRE;
and non-IPv4 Ethernet rejection. No map permission positive-control regression
or cross-guest tuple-collision test has yet been added.

## Ordinary guest trial remains failed

The two UID1000 guests bootstrapped and authenticated. The npm HTTPS ping
returned200. Numeric private/public-worker connection attempts failed, as did
source-port3000 initiation, an unapproved domain and raw socket creation.
Native IPv6 reported ENETUNREACH, which alone is not evidence of BPF filtering.

The rebinding trial failed because protected test-domain DNS answers were not
obtained. The original fixture used resolver `119.29.29.29`. Explicit test queries
to `169.254.254.53` timed out even after an operator trial allow-map entry was
added for that exact address; the compiled exception remains UDP53-only.
The CoreDNS systemd start script overwrites Corefile, so the harness changed to
container restart after writing trial hosts. The final pending read reported
`No such container: cube-proxy-coredns`; current service state needs inspection.
No canary log existed in that read. Listener positive controls were not completed,
so connection failures are not claimed as complete endpoint-isolation proof.

The harness stopped at its first validation failure. It did **not** reach
pause/resume, post-policy-update verification, worker restart recovery, true
external rebinding, listener packet witnesses, or broad workload performance.

Both listed disposable trial guests were subsequently deleted by the root
agent (API204). The remaining nested-worker configuration is retained pending
completion of unrelated runtime tests.

## State retained for review

- Nested VM SSH: outer VPS localhost19222, existing benchmark VM key.
- Installed worker remains the second candidate; no restoration was performed.
- Backup: `/root/cube-pilot/network-live/backup/{cubelet,config.toml,Corefile}`.
- Guest a: `8cc0ec8dd41e4f4aae2b9657853b913d`, last observed interface515,
  assigned IP192.168.1.246.
- Guest b: `a360ec1ed27d42a3a203907c8a2df560`, last observed interface516,
  assigned IP192.168.1.247.
- Guest credentials are private files in `/root/cube-pilot/network-live` and
  were deliberately excluded from this evidence archive.
- Guest a's web command was moved from3000 to3005 to free source port3000;
  its repeating pilot probe was replaced. Only disposable trial guests changed.
- A metadata sentinel address169.254.169.254/32 was added to nested VM loopback.
- A canary process was launched for ports18081/18082; it was not positively
  verified, and its current state is unknown.
- Custom trial CoreDNS hosts and exact guest network allowances may remain.
- No outer production service, user project, or platform egress gate changed.

The scripts under `../../live` are unfinished trial tooling. Do not treat their
existence or the passing attached-program report as production approval. The
parent README predates this trial and must be reconciled during final review.
