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

For a reviewed replacement candidate in that same disposable cluster, provide `APP_BENCH_CUBE_TEMPLATE=tpl-...` together with its exact `APP_BENCH_CUBE_IMAGE=sha256:...`. Both values are retained in the report. Set `APP_BENCH_REQUIRE_VITE_PATCH=1` to verify the exact pinned patch survives publication and remix. This check runs after the timed operations.

Run:

```sh
./app-lifecycle.test -test.run '^TestOperatorMatchedAppLifecycle$' -test.v -test.timeout=11m
```

Default execution performs one labelled warm-up and three measured repetitions, each bounded by the overall ten-minute fixture context. Direct HTML/assets readiness has its own 90-second ceiling. Publish timing includes Docker stop/restart; Cube source export stays live.

The original offline Docker remix failure must be retained. For subsequent accepted-action runs in that same offline environment, set `APP_BENCH_SKIP_REMIX=1`; the report explicitly records the omission. Do not enable network access or silently substitute prepared dependencies to manufacture a result.

For a separate one-app source/environment reload check, set `APP_BENCH_EDIT_PROBE=1`. This edits `src/App.tsx`, validates the transformed marker, then writes a harmless `.env.local` public variable and requires its new value in transformed output. Preserve the baseline report before running: each invocation writes `report-<backend>.json`. The original unpatched templates reproduce a cold Vite optimizer shutdown race on both providers. The diagnosis, pinned dependency patch and separate deterministic Docker/Cube regressions are recorded in `docs/cube-pilot-results/vite-cold-reload-2026-09-24/README.md`; production adoption and rebuilt-template acceptance are separate requirements.

With that separate edit probe, `APP_BENCH_POSTGRES_PROBE=1` installs a temporary Vite middleware only in the synthetic fixture. It counts guest `/proc/*/comm` entries named `postgres` and checks whether `/home/sandbox/.baarcha-postgres/data` exists. Both must be absent for the database-free React Pro preset. Process arguments, environment variables and file contents are not returned. This probe changes fixture configuration after the edit checks and is excluded from lifecycle timing runs.

Use the approved existing pilot only. No model calls, paid API calls, production project changes, raw packet trials, firewall changes, broad pruning, or arbitrary fixture egress are needed. The fixture only deletes sandboxes recorded in its own private state store; retained pilot guests and templates are outside its cleanup scope.
