#!/usr/bin/env python3
"""Check the public tunnel path, not cloudflared's optimistic local readiness."""
import argparse
import json
import re
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path

SERVICE = 'baarcha-preview-tunnel.service'
COOLDOWN = 900

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def probe(url):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    request = urllib.request.Request(url, headers={'Cache-Control': 'no-cache', 'User-Agent': 'Baarcha-Tunnel-Health/1'})
    try:
        try:
            response = opener.open(request, timeout=8)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body = response.read(32768)
            disconnected = (response.status == 530 and response.headers.get('Server', '').lower() == 'cloudflare'
                            and bool(response.headers.get('CF-Ray')) and re.search(rb'\b1033\b', body) is not None)
            return {'status': response.status, 'disconnected': disconnected}
    except (OSError, urllib.error.URLError, TimeoutError):
        return {'status': 0, 'disconnected': False}


def decision(state, public, local_ok, active, now):
    state = dict(state)
    previous = state.get('checked_at', 0)
    state['checked_at'] = now
    eligible = active and local_ok and public['disconnected']
    state['failures'] = (state.get('failures', 0) + 1 if 0 <= now - previous <= 120 else 1) if eligible else 0
    if state['failures'] >= 3 and now - state.get('restarted_at', 0) >= COOLDOWN:
        state.update(failures=0, restarted_at=now)
        return state, 'reconnect'
    return state, 'observe'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--worker', choices=['vps', 'b200-01'], required=True)
    parser.add_argument('--check', action='store_true', help='Read-only deployment acceptance')
    args = parser.parse_args()
    # These dedicated DNS names terminate at the local gateway's 404 path.
    # They never authorize a project, wake a VM, or execute app/model code.
    now = time.time()
    public = probe(f'https://preview-check-{args.worker}.baarcha.tn/?probe={int(now)}')
    local_ok = probe('http://127.0.0.1:8095/healthz')['status'] == 200
    active = subprocess.run(['systemctl', 'is-active', '--quiet', SERVICE], timeout=5).returncode == 0
    if args.check:
        print(json.dumps({'public_status': public['status'], 'local_ok': local_ok, 'active': active}))
        return 0 if public['status'] == 404 and local_ok and active else 1
    path = Path('/var/lib/baarcha-preview-tunnel-health/state.json')
    state = json.loads(path.read_text()) if path.exists() else {}
    state, action = decision(state, public, local_ok, active, now)
    # Persist the cooldown before attempting recovery, including failed restarts.
    temp = path.with_suffix('.new')
    temp.write_text(json.dumps(state)); temp.chmod(0o600); temp.replace(path)
    if action == 'reconnect':
        subprocess.run(['systemctl', 'try-restart', SERVICE], check=True, timeout=30)
    print(json.dumps({'worker': args.worker, 'public_status': public['status'], 'local_ok': local_ok,
                      'active': active, 'action': action, 'failures': state['failures']}))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
