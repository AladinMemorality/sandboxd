# Nested boot hold and source-gate review, 2026-09-25

Read-only inspection of the actual worker's systemd dependency properties and
stop scripts; no unit, service, container or firewall changed by this review.
These observations preceded the separately coordinated durable metadata upgrade.

## Exact existing startup graph

`cube-sandbox-control.target` was enabled. Its Wants includes the 14 services
listed in `lifecycle.py:SERVICES` **and** `cube-sandbox-webui.service`.
`cube-sandbox-cube-templatecenter.service` was independently enabled. The inactive
optional `cube-sandbox-s3lvol.service` is also held, making 16 service units in the
actual empty-upgrade hold set. The compute
target was disabled, but it Wants Cubelet and both egress units. Disabling a
single target therefore does not establish a boot hold.

The full reviewed management hold set is:

```
cube-sandbox-control.target
cube-sandbox-compute.target
cube-sandbox-mysql.service
cube-sandbox-redis.service
cube-sandbox-minio.service
cube-sandbox-coredns.service
cube-sandbox-dns.service
cube-sandbox-cubeops.service
cube-sandbox-cubemaster.service
cube-sandbox-cube-api.service
cube-sandbox-cubelet.service
cube-sandbox-cube-templatecenter.service
cube-sandbox-cube-lifecycle-manager.service
cube-sandbox-cube-proxy.service
cube-sandbox-cube-egress-net.service
cube-sandbox-cube-egress.service
cube-sandbox-webui.service
cube-sandbox-s3lvol.service
docker.service
docker.socket
```

Web UI is a boot-hold/preflight subject, although it is deliberately not required
for customer runtime readiness in the 14-service check. If the installer keeps
web UI available, it needs the same preflight guard; otherwise leave it held.
Do not add it to runtime readiness just to make unit lists appear identical.

Nested Docker independently owned containers with `unless-stopped` restart policy:
`cube-lifecycle-manager`, `cube-proxy`, `cube-sandbox-mysql`,
`cube-sandbox-redis`, `cube-sandbox-minio`, and `cube-production-registry`.
`cube-egress` and `cube-proxy-coredns` had restart policy `no`.
Thus Docker.service **and Docker.socket** must be held during an escrow/reboot
maintenance window, including socket activation. Support/containerd state must
remain intact. This review does not authorize pruning containers or volumes.

## Reviewed positive-file hold method

Root's isolated enrollment stage is
`/root/cube-production/durable-enrollment-20260925`. A persistent drop-in in each
exact unit above can require the **absent** positive condition file:

```ini
[Unit]
ConditionPathExists=/root/cube-production/durable-enrollment-20260925/allow-services
```

This condition applies separately to service, target and socket units. Place
`Restart=no`/`SendSIGKILL=no` only in `[Service]` for service units; those fields
are not valid for targets or sockets. Keeping the condition file absent prevents
unit execution even if another unit requests it. A condition skip is not health
or evidence of a successful stop: inspect actual service state, Docker process,
Cube-owned tasks/VMMs, open disks and persistent metadata independently.

Conditions do not stop already running units. Root must apply its controlled
stop sequence separately, preserving the required namespace escrow pin. Stop
management/app lifecycle writers first, Cubelet through its reviewed graceful
path after guest/task accounting, support containers through verified graceful
stops, then Docker last. Inspect unit jobs and PIDs, not only a zero command exit.
The source lifecycle gate remains false throughout this upgrade. This document
contains no command that creates the positive condition file or starts services.

The unit graph was verified read-only. The hold itself still requires root's
actual installed drop-in/load/boot acceptance; this review did not execute it.
A final installed-unit audit must also inspect unexpected startup units/jobs and
Docker restart policies rather than assuming this snapshot remains exhaustive.

## Existing helper escalation is separate from systemd settings

- LCM/proxy/webui wrappers call Compose `down --remove-orphans`, ignore errors,
  then a container removal helper. Default Compose stop timeout can force kill.
- MySQL/Redis/MinIO wrapper calls `down-support.sh`: scoped Compose stop/rm, or
  full Compose down. `CUBE_SANDBOX_REMOVE_VOLUMES=1` adds volume removal; never use
  that path for state escrow. Confirm exact service selection before stopping.
- CoreDNS uses `docker stop -t 10` then removal; egress uses `docker stop -t 15`.
- Ordinary systemd `SendSIGKILL=no` does not disable Docker/Compose's own timeout
  escalation. A graceful-only candidate must replace the relevant ExecStop with
  a reviewed fixed exact-container stop such as `docker stop --time=-1`, retaining
  the process/container on bounded observer timeout rather than deleting it.
- Native Cubelet's actual graceful PID/namespace behavior must be observed;
  replacing its stop script with a fixed TERM operation does not prove flush or
  release. Do not kill the namespace escrow pin before preserving required data.

These are review requirements, not commands to apply to a live worker.

## Source-gate transition remains blocked

`STOP_COORDINATOR_IMPLEMENTED=False` is still committed. Do not apply a text
substitution at install time. The eventual reviewed code transition is a single
explicit source change for controlled management enrollment only after the following prerequisites
and acceptance sequence are reviewed:

1. Exact durable patch stack and persistent metadata layout accepted, including
   ordinary pause/clean reboot and separately required crash/current-data proof.
2. Root's actual maintenance boot hold and service stop behavior accepted; no
   hidden helper force-kill or startup route bypasses the reviewed graph.
3. A fixed, scheduled native coordinator acceptance under final production drain,
   including the first genuine empty-worker stop receipt/marker. This actual
   end-to-end acceptance follows controlled management enrollment and remains a
   mandatory customer-routing gate; it is not fabricated as a prerequisite proof.
   Initial management enrollment remains non-tenant-ready.
4. Root-only final manifests, binaries, controller database/key backup, paired
   backup custody/restore plan, and rollback inputs retained and reviewed.
5. Candidate unit/load validation, current helper hashes, controller startup
   marker behavior and monitoring evidence failures accepted. Preflight's
   RemainAfterExit result is per boot; after intentional in-boot config/binary
   changes, rerun actual preflight before restarting components.

Do not flip the gate merely to get a unit to start. The existing absent-status
initial-empty branch and inert enrollment record are not substitutes for these
checks. No gate-changing patch or installer is supplied by this preparation.


Root subsequently reported actual graceful management stop and escrow of 84
quiesced databases. Its applied maintenance hold includes all 16 Cube service
units, the targets, Docker.service and Docker.socket, with persistent positive
allow-file conditions. Exact container IDs were stopped using `--time=-1`, and
Cubelet received TERM. This is root's actual enrollment evidence; this document's
read-only reviewer performed no service changes. Retain the corresponding private
stop/escrow logs; this result does not enable the candidate source gate or prove
future ordinary OS shutdown uses graceful retained-state helpers.
