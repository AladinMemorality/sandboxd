# Disposable matched app lifecycle harness

`app_lifecycle_benchmark_test.go` is an operator-only API-package fixture. It deliberately is not part of ordinary CI and never opens a production store. The measured results and limitations are recorded in `docs/cube-pilot-results/app-lifecycle-2026-09-24/README.md`.

Copy the fixture into `control-plane/internal/api/operator_lifecycle_test.go` in an isolated checkout of the recorded source commit. Compile using Go 1.22 on Linux with a 2-CPU/2-GiB build container and the existing module cache; the compilation container can use `--network none`. The fixture needs the checkout's migrations, so run its compiled binary from `control-plane/internal/api`.

The stage must be an absolute path containing `app-lifecycle` with an operator-created `disposable-benchmark` marker. Workspaces and SQLite are created in unique short `/tmp/app-lifecycle-owned-*` directories so host Unix sockets stay within Linux path limits. Reports are written mode 0600 to the stage. Failed cleanup retains the owned work directory for recovery; successful cleanup removes it.

Required environment:

```sh
APP_LIFECYCLE_BENCH=1
APP_BENCH_BACKEND=docker # or cube
APP_BENCH_STAGE=/absolute/disposable/app-lifecycle-stage
APP_BENCH_SOURCE_COMMIT=<the compiled control-plane commit>
```

The Docker arm additionally requires `APP_BENCH_NETWORK=cube-app-bench-<unique-suffix>`. Create that **dedicated** bridge with `docker network create --internal`; the fixture verifies it is internal. It uses the exact pinned production image and the normal cold-seeding path. Remove only the owned bridge after confirming it has no containers.

The Cube arm is intentionally tied to the existing marked nested pilot's localhost API/proxy, private credential file, and reviewed React Pro v3 template. It reads the credential internally and does not print it. Only synthetic fixture app IDs enter the runtime allowlist. This is not a deployable production configuration.

Run:

```sh
./app-lifecycle.test -test.run '^TestOperatorMatchedAppLifecycle$' -test.v -test.timeout=11m
```

Default execution performs one labelled warm-up and three measured repetitions, each bounded by the overall ten-minute fixture context. Direct HTML/assets readiness has its own 90-second ceiling. Publish timing includes Docker stop/restart; Cube source export stays live.

The original offline Docker remix failure must be retained. For subsequent accepted-action runs in that same offline environment, set `APP_BENCH_SKIP_REMIX=1`; the report explicitly records the omission. Do not enable network access or silently substitute prepared dependencies to manufacture a result.

For a separate one-app source/environment reload check, set `APP_BENCH_EDIT_PROBE=1`. This edits `src/App.tsx`, validates the transformed marker, then writes a harmless `.env.local` public variable and requires its new value in transformed output. Preserve the baseline report before running: each invocation writes `report-<backend>.json`. This edit probe currently reproduces an unresolved environment-reload hang on both providers.

Use the approved existing pilot only. No model calls, paid API calls, production project changes, raw packet trials, firewall changes, broad pruning, or arbitrary fixture egress are needed. The fixture only deletes sandboxes recorded in its own private state store; retained pilot guests and templates are outside its cleanup scope.
