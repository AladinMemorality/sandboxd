# Cube v0.7.1 worker hard-deny candidate

This is a candidate upstream dataplane patch, not authorization to enable public
egress or replace a running worker. sandboxd still rejects nonempty
`SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS` until the patched worker has passed packet
tests and an enforcement attestation has been integrated. The current safe
default is no outbound access.

## Latest isolated trial, 2026-09-23

The second candidate was attached only inside the disposable nested VM. Its
87 crafted attached-program checks passed, but full guest-network acceptance
failed at DNS setup and remains incomplete. Domain egress stays disabled. See
[the exact handoff and limitations](results/2026-09-23/HANDOFF.md). The older
status below describes the September17 build, not the current trial state.

## Validation status, 2026-09-17

**The candidate has not replaced or attached to a live worker. Domain egress
remains disabled. The live packet-isolation gate is unsatisfied.** Native CGO
Cubelet built successfully on the disposable Ubuntu24/Linux6.8 AMD64 VM.
Kernel-backed classifier/cached-session regressions passed with no skips, and
the full `mvmtap`, `nodenic`, and `localgw` collections passed the real verifier
using fresh anonymous maps. Sanitized output is in [results](results/).
These checks establish buildability and the tested classifier behavior, not
isolation of a running guest or safe production deployment.

The archived candidate hash is
`814899a753d06e559e5973423a6dbf1f5499a32aa7b7de6ea02091c30828de7d`.
It protects management IPv4 `65.108.225.153/32` and was built with the private
DNS exception `10.0.2.3`. Read-only service inspection subsequently found the
actual Cube DNS listener at **169.254.254.53:53**. That mismatch must be corrected
before a public-registry positive control can be attempted. The existing binary
is a build/test artifact, **not a deployable policy for that VM**.

No worker replacement, trial listener startup, or new live-test guest creation
was performed. Existing benchmark guests were not changed. The scripts in
[drafts](drafts/README.md) are unexecuted preparation, syntax checked only.
DNS rebinding, host/sibling/metadata connection denial, forged source ports/raw
TCP, native IPv6, fragments/encapsulation, and pause/resume/restart packet paths
remain unproven in an attached-worker test. No platform attestation bypass has
been added.

To rebuild the already-patched isolated checkout with its observed resolver,
run the following **inside that disposable VM only**, then rerun all live gates
below before considering replacement. These commands build a separate artifact
and do not install it or change systemd:

```sh
cd /root/cube-pilot/CubeSandbox
python3 /root/cube-pilot/security/render_hard_deny.py \
  --management-cidr 65.108.225.153/32 \
  --dns-resolver 169.254.254.53 \
  --output CubeNet/src/baarcha_hard_deny.h
export PATH="/root/cube-pilot/toolchain/go/bin:$PATH"
cd CubeNet/cubevs
GOMAXPROCS=2 GOARCH=amd64 go generate ./...
CUBE_HARD_DENY_TEST_MANAGEMENT_IP=65.108.225.153 \
  GOMAXPROCS=2 go test -v -count=1 \
  -run 'TestBaarcha|TestClassifyEgressFlow|TestSessionPolicy' ./...
cd ../../Cubelet
GOMAXPROCS=2 go build -o /root/cube-pilot/cubelet-hard-deny-resolver-corrected ./cmd/cubelet
sha256sum /root/cube-pilot/cubelet-hard-deny-resolver-corrected
```

This reuses the already-built native `cubecow` library. It has not been executed
with the corrected resolver. Verify the resolver address again if the VM changes;
enumerate all management public addresses for each deployment independently.

Pinned upstream: `31d911e430fdf8a8879bd062b8e78066c1a8e89d` (`v0.7.1`).
`upstream.json` records original source hashes. Apply the patch only to this
revision, in an isolated build checkout. The generated policy header is owned
by the worker operator, never supplied by a tenant or embedded in a guest.

## Why the API policy alone is insufficient

The exact upstream [session classifier](https://github.com/TencentCloud/CubeSandbox/blob/31d911e430fdf8a8879bd062b8e78066c1a8e89d/CubeNet/src/session.h#L164)
checks allow rules before deny rules. An exact domain allowance learns its DNS
A answers into that allow map, without screening private addresses in
[DNS learning](https://github.com/TencentCloud/CubeSandbox/blob/31d911e430fdf8a8879bd062b8e78066c1a8e89d/CubeNet/src/dns_response.h#L122).
A rebinding or incorrect DNS answer can therefore override the private/metadata
deny. Also `allow_internet_access:false` replaces custom deny ranges with
`0.0.0.0/0` in [policy construction](https://github.com/TencentCloud/CubeSandbox/blob/31d911e430fdf8a8879bd062b8e78066c1a8e89d/CubeNet/cubevs/netpolicy.go#L699).
Adding more `denyOut` CIDRs cannot fix this ordering.

The patch adds non-overridable worker destination checks before ordinary allow
rules and before the cached-session policy-version shortcut. DNS learning drops
protected answers. The mandatory ranges cover private, loopback, link-local,
carrier-grade NAT, zero-net, multicast, reserved and benchmarking destinations.
The operator must additionally enumerate every worker/control-plane public
address or range, including API/proxy/metadata/storage services on public IPs.

Only explicitly configured resolver IPv4 addresses can be exempted, on UDP/53
alone. A management address cannot be a resolver exception. Existing Cube DNS
question filtering still limits names. TCP DNS is intentionally unavailable;
choose a resolver whose UDP responses work for the approved registries.

Native IPv6 is rejected before IP policy by the upstream
[TAP Ethernet gate](https://github.com/TencentCloud/CubeSandbox/blob/31d911e430fdf8a8879bd062b8e78066c1a8e89d/CubeNet/src/mvmtap.bpf.c#L975).
Do not describe IPv6 strings in `denyOut` as enforcement: that trie is IPv4.
The existing protocol dispatcher drops protocols other than TCP/UDP/ICMP,
including raw IPv4-in-IPv4 and GRE; verify fragments and encapsulation on the
actual worker. An allowed external service may still act as an application-level
proxy; allowlists are not data-loss prevention.

## Build and validate without replacing services

On a disposable Linux AMD64 worker VM, use Go **1.25.7 or newer**, Rust **1.89.0**,
clang/LLVM, libbpf development headers, make and a C toolchain. Ubuntu24 packages are
`clang llvm libbpf-dev build-essential git`. BPF verification/program-test-run
requires root or the corresponding BPF/network capabilities. Run without
mounting or modifying production workers.

```sh
git clone https://github.com/TencentCloud/CubeSandbox.git CubeSandbox
cd CubeSandbox
git checkout --detach 31d911e430fdf8a8879bd062b8e78066c1a8e89d
git apply --check /path/to/0001-worker-hard-deny.patch
git apply /path/to/0001-worker-hard-deny.patch
python3 /path/to/render_hard_deny.py \
  --management-cidr 203.0.113.7/32 \
  --dns-resolver 10.0.0.53 \
  --output CubeNet/src/baarcha_hard_deny.h
cd CubeNet/cubevs
GOMAXPROCS=2 GOARCH=amd64 go generate ./...
CUBE_HARD_DENY_TEST_MANAGEMENT_IP=203.0.113.7 \
  GOMAXPROCS=2 go test -v -count=1 \
  -run 'TestBaarcha|TestClassifyEgressFlow|TestSessionPolicy' ./...
cd ../../cubecow
cargo +1.89.0 build --locked --release --lib
mkdir -p ../Cubelet/third_party/cubecow/include ../Cubelet/third_party/cubecow/lib
cp include/cubecow.h ../Cubelet/third_party/cubecow/include/
cp target/release/libcubecow.a ../Cubelet/third_party/cubecow/lib/
cd ../Cubelet
GOMAXPROCS=2 go build -o /tmp/cubelet-hard-deny ./cmd/cubelet
```

Replace documentation IPs with actual protected addresses before any service
test. With no generated header, the patch intentionally fails compilation.
The `go generate` step embeds the modified BPF objects in Go. Rebuilding
Cubelet is necessary; copying a loose `.o` beside the existing binary does not
replace the embedded dataplane. Worker rollout also requires attachment/map
recovery verification, not merely a successful binary restart.
Do not work around missing native `cubecow.h`/`libcubecow.a` by disabling CGO:
the non-CGO storage stub cannot perform the required snapshot/pause operations.

The Go regression inserted by the patch installs permissive allow-map entries
for protected targets and proves the hard-deny verdict wins for TCP/UDP/ICMP,
including cached sessions with unchanged policy versions. **Any SKIP is a failed
attestation**, not a pass. The full-dataplane test also loads all three generated
program collections using fresh anonymous maps, without attaching interfaces or
reusing existing worker pins. Inspect the output and verifier errors. Run the full
CubeNet suite as well; changed old assumptions need review rather than deletion.

Locally, `python3 test_hard_deny.py` compiles and executes the actual generated C
predicate using a userspace harness. That verifies CIDR boundaries and the narrow
DNS exception; it does not load eBPF or establish network isolation.

## Required live packet checks before enabling domains

Use two disposable guest sandboxes and listeners containing only sentinel data.
Prove an approved public registry succeeds and an unapproved domain fails.
Supply an approved-domain DNS answer pointing at each protected destination;
show requests fail even after the answer is learned. Test host public/private
interfaces, loopback, link-local metadata, another guest, Cube API/proxy/storage
addresses, IPv6, IP fragments, IPv4 encapsulation and forged TCP/source ports.
Repeat while paused/resumed and after worker restart/policy reload.

Cube has separate TCP reply paths for gateway and exposed-port connections.
They must continue to carry legitimate Cubelet/bootstrap/preview responses.
The patch blocks gateway ICMP initiation; it does not claim to replace the
existing TCP reply-path validation. Specifically test that non-SYN packets or
forged exposed source ports cannot establish or access a new management flow.
Do not enable domain egress until this is demonstrated on the deployed kernel.

Do not combine this patch with inherited CubeEgress L7 forwarding/injection
rules without reviewing the proxy's own DNS/SSRF boundary: a host-side proxy
connection does not traverse the guest's TAP classifier. The initial domain
policy should use plain allowOut only, from a reviewed template with no inherited
network allowances or L7 rules.

## Model credentials

Keep global model/API credentials in the existing host-side auth proxy or a
separate reviewed relay. Give a guest only an opaque per-sandbox, short-lived
relay capability scoped to owner, project, provider, allowed operations and
budget; revoke it on deletion. Authenticate and authorize on every relay call,
reject arbitrary upstream URLs, and forward only to fixed provider origins.
Do not add general access to the worker's API/auth-proxy ports, place global
credentials in guest environment/snapshots, or expose a new public gateway as a
side effect of enabling package downloads.
