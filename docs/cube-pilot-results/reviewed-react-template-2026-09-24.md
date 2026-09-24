# Reviewed React Cube template — 2026-09-24

The production-base React Pro image passed a disposable Cube functional run.
No production projects, files, credentials, routing, or services were changed.
See [the structured result](reviewed-react-template-2026-09-24.json) for immutable
source/image/binary identities and individual timings.

The successful template is `tpl-816ce9e72cf6479788f5b9bc`, alias
`baarcha-react-reviewed-20260924-v2`, backed by image
`sha256:88533a511dcc094b8239a3ecb43e8e440edf21289c09e566eb134f426107c53e`.
It uses one CPU, 1024 MiB RAM, a 10 GiB writable layer, and the reviewed production
base. Template readiness is credential-free bootstrap port 49983 `/health`;
per-instance init supplies fresh supervisor credentials. Direct egress remains
deny-all and Cube CA injection is disabled.

## Checks and timings

The actual image passed a UID 1000 Docker bootstrap fixture with no network:
no supervisor before init, same-init retry succeeds, changed credentials are
rejected, unauthenticated supervisor access is rejected, and the app remains
stopped until the authenticated reverse channel connects.

Two real Cube guests then received independent fresh credentials. A host fixture
used the actual runtime client and reverse-channel implementation. Its policy
and dial callback deny every outbound connection; it does not call external
models or services. Both guests served the React HTML shell, transformed
`/src/main.tsx`, and Vite client. A supervisor token from the other guest was
rejected. Creation through these readiness checks took 7206 and 7256 ms.

Three pause/connect cycles took 939, 1587, and 965 ms through authenticated
channel reattachment and frontend response (median 965 ms). The supervisor boot
time, frontend PID, and restart counter remained identical. This proves the
observed process survived these three pauses; it is not a general application
memory or database consistency test. Both guests were deleted after the run.
These are small functional samples, not a controlled fleet benchmark.

## Failures caught before the successful run

The initial image `f62ac536...` predated the supervisor's channel-wait change.
An actual bootstrap fixture caught its frontend starting early. It was replaced
with a build from committed source `1fbeac29...`; its template must not be used.
The first template distribution also exhausted the disposable VM's root disk.
Only this attempt's partial download and imported Docker image were removed.
The operator subsequently expanded the disposable root disk offline to 160 GiB,
with 85 GiB free afterward; retained test artifacts were preserved.

After reboot, the disposable registry required restarting before the new image
could be pulled. The fixture's first resume attempt also exposed a harness bug:
Cube Connect returns metadata without the original ingress credential. The
harness was corrected to retain the creation credential, matching the existing
production binding behavior. Earlier failed-run guests were also deleted.

## Evidence and remaining gates

Sanitized reports, exact fixture source/binary, bootstrap checker, image build
inputs, and logs remain on the outer test host under:
`/opt/baarcha-bench/cube-global-20260924/template-review/cube-reviewed-app-image/`.
The successful template/image remain reusable in the nested VM. Guest fixture
source is `main.go`, binary `reviewed-pilot`, report `react-cube-report-v2.json`;
the JSON alongside this document records their SHA256 hashes. Bootstrap evidence
is `bootstrap-report-v2.json`; refreshed input identity is `current-inputs.json`.

This closes a React starter/template/bootstrap/resume acceptance item. It does
not close real-browser rendering, other preset builds, per-project native ABI,
large workspace/home migration and rollback, disk-capacity budgets, model relay,
ordinary external dependency access, or complete-fleet cutover acceptance. It
includes no packet-level or extended network-isolation trial.
