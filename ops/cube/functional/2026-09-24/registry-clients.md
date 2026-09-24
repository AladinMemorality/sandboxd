# Public registry and native HTTPS acceptance

`cube_registry_acceptance_test.go` is an opt-in API-package fixture for the
existing disposable nested Cube cluster. It creates one synthetic app with its
own private SQLite store and deletes only that app's guest. Production state,
provider credentials, model calls, owner workspaces and network policy are not
used or modified. It does not exercise protected addresses, raw packets, or the
previously restricted isolation tests.

The guest uses reviewed React Pro v3 template `tpl-ce9efc43b71248d9a0adfb90`.
The real broker performs public IPv4 resolution and numeric dialing with the
ordinary operator policy. The explicit protected inventory includes the outer
VPS, local interfaces, management/proxy endpoints, platform and Cube domains.
There is no test dial hook, DNS override, allow rule, network verification flag,
global admission, or fixed-service callback. Direct guest NIC egress remains
denied under the cluster's existing policy. That statement describes configured
routing; it is not new NIC enforcement evidence.

Seven checks run in the guest:

1. Expected loopback proxy variables and native Node proxy opt-in.
2. Native Node `fetch` over verified HTTPS to the public npm ping endpoint.
3. Default native Node HTTPS agent to the same endpoint.
4. curl over verified HTTPS to pinned PyPI package metadata.
5. npm installs `is-number@7.0.0` with a fresh cache and verifies the installed version.
6. pnpm installs the same package with a fresh store and verifies its version.
7. A fresh Python venv installs `packaging==24.2` using pip with no cache and
   verifies the package exists.

Package lifecycle scripts and optional dependency discovery are disabled where
applicable; no downloaded package code is invoked. Subprocesses have bounded
time/output and errors record only an operation/exit code. This is real public
registry compatibility, not arbitrary SDK/database/WebSocket support, full
application installation, or a performance/load benchmark. No credentials or
environment values are included in reports. Source import reexecutes the
supervisor before the fixture runs, also requiring the normal broker reattach.

Copy the fixture into `control-plane/internal/api/operator_registry_test.go` in
an isolated source checkout. Compile with Linux Go 1.22 and CGO for SQLite; the
existing cached-module build container may use `--network none`. Run the binary
from that checkout's `control-plane/internal/api` directory so migrations resolve.
Create a mode-0700 evidence directory containing an empty `disposable-registry`
marker. The nested cluster credential file stays at its existing private path.

```sh
CUBE_REGISTRY_FUNCTIONAL=1 \
CUBE_REGISTRY_STAGE=/root/cube-registry-acceptance \
./registry.test -test.run '^TestOperatorCubeRegistryClients$' -test.v -test.timeout=9m
```

Read the resulting mode-0600 `registry-report.json`, including all seven check
results and `vm_deleted`, and independently verify no owned guest remains.
Failed checks must be retained; do not substitute prewarmed caches or relax
policy to make the fixture pass.

## Actual nested result, 2026-09-24

**All seven checks passed in 10.67 seconds** on Node 22.23.2. This is the whole
fixture duration, not an application lifecycle or throughput benchmark. The
[report](registry-report.json) and [test output](registry-test-output.txt) retain
the results, template identity and successful guest deletion. The host source
was archived from runtime commit `b74fa7c0286225e6994e6e6b3ecae645b4f903b8`, with
this fixture as its sole overlay, and compiled using CGO-enabled Linux Go 1.22
with vet in the offline build container. Actual execution used the existing
nested worker and the production broker implementation; no policy exception or
global admission was enabled.

The [initial report](registry-report-initial.json) and
[initial output](registry-test-output-initial.txt) preserve an initial six-pass,
one-fail result: the fixture invoked system `python3 -m pip`. The image installs
`python3-venv`, and the actual runtime dependency preparation creates a venv.
The final fixture follows that path; it did not modify the image or bypass
network policy. The initial failing subprocess did not retain stderr, so the
report records its exit code rather than claiming a captured diagnostic.

Both runs recorded successful exact-guest deletion. A subsequent independent
read-only cluster listing contained the previously preserved paused guest and a
new guest launched by the separate MyHomeTroc acceptance worker after this run;
that worker confirmed ownership. No unrelated guest was deleted.

This closes representative public-registry/native-HTTPS acceptance for this
reviewed image. It does not establish full tenant dependency graphs, MyHomeTroc
native ABI, arbitrary clients, production routing or worker NIC isolation.
