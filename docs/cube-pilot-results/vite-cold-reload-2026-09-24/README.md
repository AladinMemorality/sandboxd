# Cold Vite environment reload, 2026-09-24

The shared Docker/Cube `.env.local` failure was a Vite 5.4.21 dependency-optimizer shutdown race. It was not a Cube network failure or screenshot problem. A cold optimizer leaves transformed dependency requests waiting on processing promises; Vite closes its HTTP listener and cancels optimization without settling those promises. Its plugin container then waits indefinitely during restart.

The original normal Docker API edit probe was rerun with bounded failure diagnostics: `/` became unavailable for the full 90-second window. A temporary diagnostic plugin showed watcher and HMR shutdown completing while plugin-container shutdown retained four optimized React/sonner requests. Warm-cache success did not establish a fix. `optimizeDeps.holdUntilCrawlEnd=false` and waiting for `server.waitForRequestsIdle()` before close both failed the cold regression and were not shipped.

## Change

The three Vite templates pin version `5.4.21` and use pnpm's standard `patchedDependencies` support. The two-line patch settles the current and queued dependency-processing promises after optimizer cancellation. It changes neither supervisor restart policy nor application configuration. It is a local patch, not an upstream released fix.

- Upstream bundled JavaScript SHA256: `bfa94186daff535fefdf286088c1588fad6b02c01ef11cd3b42a6cb3e4c90767`.
- Patched bundled JavaScript SHA256: `88f35a22eb6ae9d58e54b94c1544f6c1cc90e3e2eac42542c5ea834179ef24f7`.
- pnpm patch-file SHA256: `56e863247e5b83428c6d258b48fed1409763d933dde105334dd43f26aec779da`.

Exact package versions, registry archive integrity and patch hash are recorded in each template lockfile. pnpm recreates patched package entries during reinstall; no bootstrap script edits shared pnpm hardlinks. The disposable comparison fixture separately verifies atomic replacement against an unchanged hardlink witness.

The relevant upstream implementation is [Vite 5.4.21's optimizer](https://github.com/vitejs/vite/blob/v5.4.21/packages/vite/src/node/optimizer/index.ts). Vite 6.4.1 includes promise settlement on an early closed `runOptimizer` path, but that alone did not cover the final-shutdown race in this fixture; no major-version upgrade is represented as verified.

## Verification

[`scripts/vite-reload-regression.mjs`](../../../scripts/vite-reload-regression.mjs) uses a disposable marker, fresh child processes and unique cold caches. A synthetic two-second optimizer delay makes the race reproducible. It validates transformed source edits, the new public environment value, and bounded server shutdown. The times in these reports include the artificial delay and are **not performance benchmarks**.

- Linux Docker, 1 CPU / 1 GiB, reviewed React Pro v3 image: unpatched cold reload failed as expected with four pending dependency requests; patched cold reload and shutdown passed three times. The hardlink witness remained unchanged. See `docker-regression-results.json`.
- Actual nested Cube v3 guest: the same baseline failure and three patched passes; whole operator test passed in 33.64 seconds and deleted its guest. Direct NIC egress remained disabled and the reverse broker denied all destinations. See `cube-regression-results.json` and `cube-reload-output.txt`.
- Standard pnpm 10.25.0 frozen installs of React Pro, React Standard and Marketplace produced the exact patched bundle. Each passed three cold reload/shutdown repetitions and its normal production build. React Pro also passed a fresh offline frozen reinstall after moving the original `node_modules` aside. See `pnpm-installer-results.json`.
- CI now runs the installed-patch cold regression against the built base image on both existing architecture jobs. Those new CI runs have not been executed merely by adding the workflow step.

Two initial Cube fixture attempts used marker names excluded by the existing source-publication policy. Both failed closed and cleaned up. The corrected marker is `README.reload-fixture.txt`; no source policy was relaxed for fixture markers. These are fixture failures, not passing environment-reload results.

## Rollout limits

No production application was modified. Existing projects need reviewed package/lock/patch adoption without replacing their source or custom pnpm settings; npm-only/custom package managers are not covered by pnpm metadata. Rebuilt Cube templates must include the patch and the latest guest source-archive policy permitting root `patches/*.patch`. The old reviewed v3 guest strips patch files from source publication, so these v3 tests prove the runtime fix only, not patched publication/remix compatibility. The [composed candidate acceptance](../react-pro-candidate-2026-09-24/README.md) subsequently verified patch retention through publish/remix, normal environment reload, retained lifecycle speed, and absence of PostgreSQL processes/state in React Pro.

The original diagnosis and failed alternatives remain in the private operator stage `/opt/baarcha-bench/cube-global-20260924/app-lifecycle-reload`; actual Cube evidence is under `/root/app-lifecycle-reload` in the disposable nested VM. No isolation, fleet migration, billing or production capacity acceptance is implied.
