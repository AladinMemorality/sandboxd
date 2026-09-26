# Nested KVM cold-start readiness experiment

The September 26 Motion image reached READY rootfs distribution, but its template
failed with `Receive event timeout after 10000ms` before the guest supervisor
started. The guest kernel and agent entered main; no vsock-ready event appeared.
The original failed job and template remain retained. This resembles the nested
KVM timing failure documented in [Cube issue 94](https://github.com/TencentCloud/CubeSandbox/issues/94).
Similarity is a hypothesis, not proof of this worker's cause.

`0010-nested-kvm-cold-start-timeout.patch` applies only to CubeShim from upstream
`31d911e430fdf8a8879bd062b8e78066c1a8e89d` (v0.7.1). It gives fresh boots 60 seconds
for the actual VsockServerReady event and preserves the ten-second restored-guest
wait. Kernel, guest agent, VMM sources and snapshot format are unchanged.

The isolated candidate was copied from the exact clean CubeShim/hypervisor source
subtrees and built with the existing Rust 1.89 toolchain and locked dependencies.
Native build limits: 2 CPU, 4 GiB, 256 tasks, 30 minutes. The first linker attempt
failed because the development `libcap-ng.so` alias was absent. The continuation
used a private linker directory pointing at the already installed runtime library;
no system package or library was changed. Six existing snapshot compatibility unit
tests passed; 60 other tests were filtered. The release binary built successfully,
and all 458 input source files still matched their pre-build manifest afterward.
Build-only evidence is in `results/2026-09-26/build.json`; it does not establish
production acceptance or authorize global migration.

Private candidate source/build logs:
`/root/cube-production/shim-coldstart-candidate-20260926-01` in the worker.
The guarded Motion registration experiment retains both stock and candidate
checksums, holds all four outer operator locks and the nested worker lock, refuses
active compute on the actual `/data/cubelet/cubelet.sock`, and makes one new
template request using the already reviewed image. It restores the stock shim
only after template jobs are terminal and native guest cleanup is observed.
There is no project binding/default change, data rewind, process kill or paid
model call in the inner experiment. Its result is separate from the build proof.

The initial attempt refused the upstream tar's UID 1001 before installing anything
or submitting a job. The reviewed continuation accepts only the exact stock
release hash/UID/GID/mode under the existing root-owned mode0700 services parent.
New and restored executables are root-owned mode0755. The failed preflight stays
in its original private stage rather than being rewritten as successful.

## Recorded live result

The new template `tpl-c0c9813b42db46898f7ddd9f` reached READY. Native
vsock readiness took **3093 ms**, below the original ten-second timeout: the trial
does not establish that the longer wait caused success. The original stock shim
bytes were restored after native cleanup, now root-owned0755. The installed
worker kernel/agent/VMM and permanent shim implementation remain unchanged.
All prior templates and both failed records remain; baseline is now nine READY
and two FAILED. No global template mapping or customer binding changed.

Post-checks: controller health/readiness200, worker active, one consistent durable
Cube binding and zero active guests, four prior canary tasks still failed, and
all four outer locks released. See `experiment.json` and companion receipts.
Authenticated guest capability, Motion UDS integration, owned-film acceptance
and customer migration are still pending. A READY template is not those gates.
