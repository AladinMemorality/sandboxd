#!/usr/bin/env python3
"""Private API runner preflight. This file never creates/deletes a guest."""
import http.client
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import time


def read_private_json(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o600:
        raise RuntimeError('private root-owned regular JSON required')
    with path.open('rb') as stream:
        data = stream.read(65537)
    if len(data) > 65536:
        raise RuntimeError('private JSON exceeds bound')
    return json.loads(data)


def validate_handoff(value, family, run_id, now):
    return (value.get('purpose') == 'DISPOSABLE_CUBE_API_HANDOFF' and
            value.get('family') == family and value.get('run_id') == run_id and
            value.get('no_customer_guests') is True and
            value.get('previous_family_cleanup_verified') is True and
            isinstance(value.get('expires_at'), int) and
            now < value['expires_at'] <= now + 1800)


def empty_cli_inventory(value):
    counts = re.findall(r'^\s*SANDBOX_COUNT\s+(\d+)\s*$', value, re.MULTILINE)
    return counts == ['0']


def main():
    if len(sys.argv) != 4 or os.getuid() != 0:
        raise RuntimeError('root operator runner required')
    stage, family, run_id = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
    marker = stage / 'handoff.json'
    if not validate_handoff(read_private_json(marker), family, run_id, int(time.time())):
        raise RuntimeError('fresh exact coordinator handoff required')
    completion = Path('/root/cube-production/security-fixture-complete')
    info = completion.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
        raise RuntimeError('reviewed security cleanup marker required')
    credentials = read_private_json(Path('/root/cube-production/test-secrets.json'))
    key = credentials.get('cube_key')
    if not isinstance(key, str) or not key or '\r' in key or '\n' in key:
        raise RuntimeError('private API credential unavailable')
    # No environment proxy, redirects, hostname resolution or response-body logs.
    connection = http.client.HTTPConnection('127.0.0.1', 3000, timeout=5)
    try:
        connection.request('GET', '/sandboxes', headers={'X-API-Key': key})
        response = connection.getresponse()
        data = response.read(65537)
        if response.status != 200 or len(data) > 65536 or json.loads(data) != []:
            raise RuntimeError('authenticated API inventory is not empty')
    finally:
        connection.close()
    # The CLI's --all is an independent check that also includes paused guests.
    result = subprocess.run(['cubemastercli', '-a', '127.0.0.1', 'list', '--all', '--wide'],
                            capture_output=True, timeout=10, check=True, text=True)
    if len(result.stdout) > 65536 or not empty_cli_inventory(result.stdout):
        raise RuntimeError('all-state inventory is not empty')
    consumed = stage / 'runs' / f'{family}-{run_id}' / 'handoff-consumed.json'
    if consumed.exists():
        raise RuntimeError('handoff already consumed')
    # Same filesystem, under the wrapper's exclusive operator flock.
    marker.rename(consumed)


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('API acceptance preflight refused; check private handoff, credentials, and zero all-state guest inventory', file=sys.stderr)
        sys.exit(2)
