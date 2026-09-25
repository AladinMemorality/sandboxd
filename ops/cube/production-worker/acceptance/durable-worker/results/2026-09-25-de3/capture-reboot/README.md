# Post-capture clean reboot continuity

Actual operator execution completed 2026-09-25 at 14:16:31 UTC. This is the
separate clean reboot after capturing the unavailable synthetic running-loss
sandbox `524905c6b8a84f9b90470e0f0a813894`.

The original source remains retained. Its full 10 GiB disk SHA256, inode,
size and modification time matched before and after this reboot. Current disk,
upper and lower identities matched the capture plan. Master retained exactly
the unavailable fixture, with no containerd tasks or source disk references.
All actual persistent metadata roots and eight templates passed readiness.

The installed nested helper had gracefully stopped management and Docker before
capture. The operator then used QMP `system_powerdown`, observed the exact QEMU
process exit successfully, and started the same disk pair. The original capture
fence is retained unchanged; `continuity.json` binds it and the captured manifest
to this later execution boot. This is not native running-loss resume, journal
recovery acceptance, production coordinator acceptance, or production migration.

The full reboot/check operation lasted approximately 3 minutes 37 seconds. This
is a worker restart measurement, not a paused guest resume latency.
