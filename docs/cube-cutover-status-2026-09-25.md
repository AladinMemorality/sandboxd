# Cube cutover status, 2026-09-25

At 11:32 UTC production had **65 Docker projects and zero Cube bindings**.
The platform and controller were healthy, with no running coding tasks. Global
Cube creation selection is implemented but remains disabled in production.
Changing that selection does not migrate existing projects.

The reviewed capacity on this VPS is four active Cube guests. A twelve-guest
workload failed its existing acceptance bounds; the four-guest workload passed
without relaxing them. See [measured workload](../ops/cube/workload/four-app-validation-2026-09-25.json).
Durable admission, idle reclamation and capacity-aware preview retries implement
that limit. Always-on apps, active streams and coding tasks remain protected.

The controlled abrupt worker interruption exposed lost Cube registration metadata
on a private tmpfs mount. Native same-ID recovery failed. Independent current-disk
capture and isolated replay/export subsequently recovered the latest committed
app files, home files and PostgreSQL row into a new owned sandbox, without another
commit. [Recorded recovery result](../ops/cube/production-worker/recovery/results/2026-09-25/current-disk-replacement.json).
Original disks and archives remain retained; no production binding changed.

Production cutover still requires:

- Install and validate persistent Cube metadata and durable pause ordering,
  including clean restart and another owned interruption test.
- Validate the worker shutdown/startup coordinator and actual encrypted paired
  backup restoration. An encrypted fixture round-trip is not a worker restore.
- Exercise the replacement journal against real Cube imports and stable bindings.
- Refresh and freeze the entire fleet inventory under the traffic/write drain;
  preserve source containers, full homes, databases, history and snapshots.
- Merge and deploy the combined revision, migrate every frozen binding, enable
  global creation, and verify existing/new apps, URLs, private screenshots,
  publish/remix, optional databases and model/bridge traffic in production.

The older 59/61-project inventories and preparation reports are historical
evidence. They are not the final migration manifest. MyHomeTroc is included.
Screenshots remain an independent shared warmed platform service.
