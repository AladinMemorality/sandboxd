# Global Cube release, September 27, 2026

All 72 customer projects are migrated. Production has 73 Cube bindings including
the retained operator fixture, no Docker provider rows, 72 completed migration
journals, and no pending admissions. New projects default to Cube. The deployed
`cube-controller` executable replaces the legacy sandboxd daemon and has no
Docker socket. The existing Compose service/network alias remains for relay and
operator compatibility. Originals and verified migration archives are retained.

Deployed binary source: `1f4d05172f25579175e93c25b598207523874de5`.
Image: `sha256:41bee66097c64e6474d69fc44b2e68a8c75b69b70972aef858f31a7b0c886f11`.
Controller: `f4839da38381ad1031eaa19dc959a5062ec0046840cd175aefa66679c5af3866`.
The three Compose layers, retained environment, actual executable/mounts,
controller readiness, relay configuration, and 73 bindings were independently
checked. The unchanged worker boot is recorded in result.json; no worker power
operation was performed for these releases.

The binary includes graceful shutdown evidence and a bounded provider readiness
wait while the management relays join its network namespace. Actual-process
controller tests, targeted Go race tests and vet passed before deployment.
Earlier staged candidates failed file-mode/startup checks; only the image above
is deployed. Their private journals remain intact.

New-project acceptance verified Cube allocation, file write/read, pause/resume
with preserved contents, authenticated preview, and history. Cold preview needed
about two seconds of retries. The first remix attempt received a provider create
500 and left an empty pending reservation. Exact read-only provider evidence
showed no matching runtime or in-flight create; that owned reservation and its
storage grant were reconciled. A separate remix retry returned 201 and preserved
the source marker. All owned acceptance apps were removed. The failed first
attempt remains failed, not rewritten as success. See remix.json for the retry.

Motion data is now on the existing NVMe filesystem through an enabled bind mount
at its unchanged application path. The source/target manifests matched exactly:
326,052,891 bytes, eight projects, 31 terminal media jobs. Original HDD data is
retained beneath the mount. Worker boot ordering requires the NVMe mount.

The worker restart exposed a directory bind-mount defect: systemd recreated
`/run/baarcha-motion-studio`, while the controller still held the old empty
inode. Production now has `40-preserve-runtime.conf` with
`RuntimeDirectoryPreserve=yes`; the source UDS drop-in includes the same setting.
A fenced controller stop/start refreshed its mount, verified by matching device
and inode and the socket's presence. The media worker was not restarted again.
The eight-project browser gallery and both worker APIs passed, as did Avocall;
there were no loading overlays, JavaScript errors or observed 5xx responses.

The restart coordinator initially refused after Brandish resumed following an
acknowledged pause. A controller-only continuation validated the same binding and
quiescent tasks. A post-reopen inventory observation raced normal preview activity;
an independent stable observation then passed. Continuous lock descriptors were
retained through recovery, only the pinned idle coordinators were retired, and
all four outer locks plus the nested lock were independently reacquired. Use the
closure receipt below, not the earlier pending journals, as terminal evidence.

Private production evidence:

- `/opt/baarcha-cube/worker-01/maintenance/controller-final-finish-20260927-04/`
- `/opt/baarcha-bench/cube-controller-final-20260927-03/`
- `/opt/baarcha-bench/cube-motion-nvme-20260927-02/complete.json`
- `/opt/baarcha-cube/worker-01/maintenance/motion-socket-finish-20260927-02/`
- `/opt/baarcha-cube/worker-01/maintenance/motion-socket-close-20260927-01/complete.json`
- `/opt/baarcha-bench/cube-motion-socket-20260927-01/independent-final-verification.json`

No paid AI or media acceptance task was submitted. A successful new production
AI coding task remains unverified; the four earlier paid failures remain failures.
Automatic host-reboot acceptance and off-host disaster recovery are also outside
this completed migration and remain unaccepted. The four-active-guest limit is
unchanged. Minecraft public game tunnels are explicitly deferred by the user.
