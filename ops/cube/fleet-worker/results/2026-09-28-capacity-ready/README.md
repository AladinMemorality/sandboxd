# 100 concurrent sandboxes accepted — 2026-09-28

Production admission is enabled for **96 B200 + 4 VPS**, using the existing
2 vCPU / 2048 MiB runtime profile. This is the tested serving capacity, not a
measurement of the physical hardware ceiling or 100 simultaneous builds.

The run included all 71 customer web projects and 29 private Vite filler pages.
Two deprioritized Minecraft tunnels and one old operator fixture were excluded
from the HTTP profile.68 customer projects now retain their canonical IDs on
B200; three web projects remain on Cube/VPS. Relocations used encrypted S3
snapshots fetched directly by B200, exact file comparisons, preview and wake
checks. No project archives were sent over the VPS–B200 management link.

## Acceptance

- All 100 native states,2CPU/2048MiB allocations and worker placements passed.
  All reads succeeded on the first attempt; no native HTTP 408 was observed.
- Independent worker-local containerd snapshots matched 96 B200 + 4 VPS RUNNING
  task IDs exactly, sampled 3.55 seconds apart.
- Three rounds:300 successful HTML/page-entry checks and all discovered entry
  assets succeeded. Median 1.739s,95th percentile 2.652s. Background visitors also
  reported no failures while normal120-second idle policy remained enabled.
- The 101st create returned503/runtime_capacity and allocated no sandbox.
- Pausing and restarting an owned B200 runtime at peak passed in 8.815 seconds.
- Fresh public browsers at peak rendered Avocall in 8.631s and Brandish Creative
  Lab3 in 8.442s, HTTP 200, no JavaScript errors, no opening overlay.

These are bounded serving checks, not a long-duration soak, full interaction
coverage or saturated concurrent package installs, builds or Claude tasks.
Automatic checkpoint/failover, source-only dependency reconstruction and worker
reboot recovery remain separate unfinished work.

## Resources observed

Ten samples during the 100-runtime serving phases recorded the B200 worker at up to
98.81 GiB resident/cgroup memory including guest caches, median 0.9003 and maximum
1.4536 logical CPU cores. These measure the lightweight serving profile only.
The inference container remained at 79.07 GiB and about 3.76 CPU cores. The worker
has only /dev/kvm exposed, no GPU device requests, runtime runc, not privileged.
The VPS worker sample recorded 39.46 GiB including guest/cache memory and 1.00 CPU
core. Allocated limits differ from these observed consumption samples.

## Fixes deployed

Native CubeAPI previously requested a full worker inventory after each single
sandbox lookup. CubeMaster serializes those scans, producing a queue and empty
HTTP 408 responses at its30-second timeout. Patch 0012 uses the existing single-ID
detail response; creation timestamps, resource values and IDs matched before/after.
A 12-read comparison improved median 5.136s to 0.311s.

Patch 0013 makes Connect(running) actually extend the requested lease and surface
renewal errors. Controller maintenance uses the authoritative expiration to skip
unneeded renewals and gives lifecycle mutations a fresh 130-second budget. The
root/offline admission CLI now applies the same fleet placement checks as the
controller.149 Rust tests and Go API/cube/cubeconfig/cube-migrate suites pass;
the actual controller image passes isolated SQLite startup smoke validation.

Controller:007a9a01c0d60154b4c06e89b7001c37143132cafa037593729cf66ae1541e81.
Image:sha256:d10d15b77e42cb448d6a178915fed6d8f4e36ef7dc2adf7daca88a0f89c1a9f6.
Native API:dce2ec6c09dbfaf6fa6ccc83dbf64ec1c7cf176f4b9439be920788a954ea873e.
B200 Cubelet:d4c3f2813cd77979d6a456a31eb7615195b0a01d847d91b50f312faea2a12650.
JavaScript gzip is also live on B200; one measured module shrank 84.4% on the wire
with the same decoded hash. Workers retain sticky project placement and caches.

## Cleanup and retained evidence

The serving test passed, but its initial cleanup left two owned pending DELETE
records after the native runtime disappeared. It therefore exited nonzero on
cleanup. An explicitly scoped reconciliation repeated DELETE for those exact
owned runtimes, required native 404 and B200 inventory containing exactly the 68
retained customer runtimes, backed up SQLite, and briefly paused the controller
for exact admission-token compare-and-swap. Storage grants remain conservative.
Canonical deletion then removed the two app rows. This is operator recovery,
not an assertion that provider deletion is fully automatic or infallible.

All 30 owned test apps (29 fillers + overflow app) are removed. All 74 original
bindings match baseline exactly; no pending admission remains; readiness is 200.
Originally idle customer projects were returned to idle. The final observed
charged count was B200 0/VPS 1, while the enabled capacity remains96 + 4.
`acceptance.json` preserves the original serving/cleanup result alongside the
separate successful cleanup receipt; it does not overwrite the failed cleanup.
Earlier two live100 failures are retained in the rollout handoff and private
job directories. This successful third run does not erase those failures.

Private job:capacity-ready/live-capacity-861f6c3f544b37f5.
`local-native-proof-03.json`, browser result JSON and resource samples contain
only bounded operational evidence. Credentials, signed URLs, source archives,
customer file content and database backups are excluded from this directory.
