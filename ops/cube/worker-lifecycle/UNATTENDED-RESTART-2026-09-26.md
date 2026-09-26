# Unattended restart interruption, 26 September 2026

At 04:13:16 UTC, the host package updater requested
`systemctl restart baarcha-cube-worker-01.service`. The unattended-upgrade dpkg
log explicitly includes this unit; its waiting needrestart/systemctl processes
belonged to `apt-daily-upgrade.service`.

The supervisor refused the uncoordinated stop and retained QEMU. Its state was
`stop-blocked`, with systemd waiting in `deactivating/stop-sigterm`. No clean
stop was established. The sole owned Cube guest was paused; customer projects
remained on the Docker provider. Management services and the worker boot were
unchanged. Paused metadata alone does not prove application or SQL recovery.

The reviewed response canceled only the pending restart job 48182466. Before
and after, QEMU PID 3026420 retained start ticks 483403245 and supervisor PID
3026411 remained alive. The unit still needs explicit recovery; canceling a job
does not reset the supervisor. No signal, kill, adoption, power request or
synthetic clean receipt was used. The updater subsequently finished successfully,
and the storage observer completed successfully.

The exact-worker override in `99-baarcha-cube-worker.needrestart.conf` is now
installed at `/etc/needrestart/conf.d/99-baarcha-cube-worker.conf`, SHA256
`673bfb6c9e7830c959a77763a283461032cbcbf0d84755cdf44f70a2ef3ac821`.
It deselects this service from automatic restart while preserving outdated
process reporting and other service selection. A Perl semantic check verified
both exact names and rejected prefix/suffix matches, with an existing unrelated
override preserved. The existing updater configuration loads snippets using
`eval` and checks `$@`; a successful assignment returning zero is not a parse
failure.

Private production evidence is retained under
`/opt/baarcha-bench/cube-maintenance-incident-20260926/`, including
`cancel-worker-restart.json`. This change prevents this package-updater path
from recurring. It does not establish a complete coordinated host shutdown,
power-loss recovery, or the remaining global Cube rollout gates. Worker boot
enablement and customer migration remain pending actual recovery acceptance.
