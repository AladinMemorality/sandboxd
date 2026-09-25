# Disposable recovery appliance

`provision.py` creates one separate 3-GiB/2-vCPU Ubuntu rescue VM from the
previously signature-verified pinned cloud image. It does not start the VM.
QEMU runs as an unprivileged dedicated host user, with a 4-GiB cgroup limit,
no host filesystem shares, no production disks and no public listener.
The only management endpoint is host-loopback SSH20223.

The QEMU user network uses `restrict=on` and disables IPv6. The
[QEMU8.2 documentation](https://qemu.readthedocs.io/en/v8.2.10/system/invocation.html)
states that restricted user networking blocks guest traffic to the host and
external network, except explicit forwarding rules. This VM forwards only
host-loopback SSH to guest22. The recovery exporter additionally requires
interfaces down while it parses the cloned tenant filesystem.

Only a verified current-disk clone and exact reviewed image layers may be
transferred into this appliance. Do not mount a tenant filesystem on the outer
host or worker. Never attach the authoritative writable source to this VM.
Outputs remain untrusted data until archive and application validation pass.
The private operator key stays outside the VM, repository and logs.

Provisioning evidence is in `provisioning.json`. No boot, filesystem-recovery
or PostgreSQL recovery success is implied by provisioning. The VM is not
enabled on boot and must be stopped after owned recovery acceptance.
