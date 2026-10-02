# Public preview tunnel recovery

On 2026-10-02 the B200 preview tunnel returned Cloudflare HTTP 530 / error 1033
for both new and existing app hosts. Its process was active and its local
`/ready` endpoint reported four ready connections. Restarting only
`baarcha-preview-tunnel.service` restored authorized preview requests. The exact
reason Cloudflare lost the connections was not established; process liveness
and local readiness alone did not detect the outage.

`health.py` checks a dedicated public hostname every 30 seconds. The gateway
rejects this non-project hostname with 404 before authorization or runtime
access. Three consecutive Cloudflare 530/1033 responses permit reconnecting the
tunnel, only if the local gateway is healthy and the tunnel service is active.
Restarts are limited to once per 15 minutes. Other failures are logged without
restarting anything. A stopped service is never started by this monitor.

Run `configure_dns.py` on the VPS with its existing private DNS configuration.
It creates only `preview-check-vps.baarcha.tn` and
`preview-check-b200-01.baarcha.tn`, each pinned to its worker's existing tunnel.
It refuses conflicting records. Then run `install.sh vps` on the VPS and
`install.sh b200-01` on B200 from an exact checked-out release of this directory.
No controller, app, agent, credential, billing rule, or project binding changes.

Validation: `python3 -m unittest discover -s ops/cube/tunnel-health`.
Read-only acceptance: `health.py --worker <worker> --check`.
Logs: `journalctl -u baarcha-preview-tunnel-health`.
Rollback: disable/stop `baarcha-preview-tunnel-health.timer`; the tunnel itself
continues serving traffic. The isolated DNS records can remain without impact.
