#!/usr/bin/env python3
"""Read-only drain observations; never creates an authorization receipt.

No request bodies, headers, command lines, remote addresses or environment
values are collected. A connected TCP socket is NOT an active-request counter.
"""
import argparse
import collections
from contextlib import closing
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
import urllib.request

DB = '/var/lib/sandboxd/state/sandboxd.db'
WRITERS = ('baarcha-project-env-apply', 'baarcha-classroom-egress', 'baarcha-fennec-meet-egress')
STATES = {'01': 'established', '02': 'syn-sent', '03': 'syn-received', '04': 'fin-wait-1',
          '05': 'fin-wait-2', '06': 'time-wait', '07': 'closed', '08': 'close-wait',
          '09': 'last-ack', '0A': 'listen', '0B': 'closing'}


def command(argv):
    result = subprocess.run(argv, check=True, capture_output=True, timeout=10)
    if len(result.stdout) > 1048576 or len(result.stderr) > 1048576:
        raise ValueError('command output exceeds observation bound')
    return result.stdout


def socket_inodes(fdroot):
    result = set()
    for fd in fdroot.iterdir():
        try:
            target = os.readlink(fd)
        except FileNotFoundError:
            continue  # descriptors may close while observing
        match = re.fullmatch(r'socket:\[(\d+)\]', target)
        if match:
            result.add(match.group(1))
    return result


def parse_tcp(raw, owned):
    result = []
    for line in raw.splitlines()[1:]:
        fields = line.split()
        if len(fields) < 10:
            raise ValueError('invalid TCP table')
        if fields[9] not in owned:
            continue
        result.append({'inode': fields[9], 'state': STATES.get(fields[3], 'unknown'),
                       'local_port': int(fields[1].rsplit(':', 1)[1], 16)})
    return result


def process_sockets(pid, proc=Path('/proc')):
    root = proc / str(pid)
    before = (root / 'stat').read_text().rsplit(')', 1)[1].split()[19]
    owned = socket_inodes(root / 'fd')
    rows = []
    for family in ('tcp', 'tcp6'):
        rows += parse_tcp((root / 'net' / family).read_text(), owned)
    after = socket_inodes(root / 'fd')
    generation = (root / 'stat').read_text().rsplit(')', 1)[1].split()[19]
    stable = before == generation and owned == after
    # Non-TCP includes Unix-domain sockets and UDP, not necessarily work. Keep
    # these unresolved instead of fabricating an idle/active classification.
    known = {row['inode'] for row in rows}
    counts = collections.Counter(row['state'] for row in rows)
    return {'pid': pid, 'process_generation': before, 'stable_socket_set': stable,
            'tcp_states': dict(counts), 'tcp_non_listen': sum(v for k, v in counts.items() if k != 'listen'),
            'tcp_local_ports': sorted({row['local_port'] for row in rows}),
            'tcp_connections_on_listening_ports': sum(row['state'] != 'listen' and row['local_port'] in
                {item['local_port'] for item in rows if item['state'] == 'listen'} for row in rows),
            'unclassified_socket_count': len(owned - known), 'request_count': None}


def unit(name):
    raw = command(['systemctl', 'show', name, '-p', 'Id', '-p', 'LoadState', '-p', 'ActiveState',
                   '-p', 'SubState', '-p', 'MainPID', '-p', 'ControlGroup']).decode()
    return dict(line.split('=', 1) for line in raw.splitlines() if '=' in line)


def unit_processes(value):
    group = value.get('ControlGroup', '')
    if not group:
        return []
    base = Path('/sys/fs/cgroup')
    root = base / group.lstrip('/')
    if root.resolve(strict=True) != root or not root.is_relative_to(base):
        raise ValueError('unexpected control group')
    pids = set()
    for file in root.rglob('cgroup.procs'):
        pids.update(int(p) for p in file.read_text().split())
    return sorted(pids)


def sqlite_observation():
    # sqlite3.Connection's context manager commits/rolls back; it does NOT
    # close the connection. Retained observation frames must not keep DB FDs
    # open while the independent coordinator scans for maintenance writers.
    with closing(sqlite3.connect('file:' + DB + '?mode=ro', uri=True, timeout=2)) as db:
        db.execute('PRAGMA query_only=ON')
        db.execute('BEGIN')
        tasks = dict(db.execute('SELECT status,count(*) FROM task GROUP BY status'))
        count = db.execute('SELECT count(*) FROM runtime_binding').fetchone()[0]
        return {'task_status_counts': tasks, 'runtime_bindings': count,
                'active_tasks': sum(tasks.get(x, 0) for x in ('running', 'starting', 'queued', 'pending'))}


def shutdown_observation(controller, since):
    if not since:
        return {'checked': False, 'regular_http_shutdown_success_observed': None}
    parsed = datetime.datetime.fromisoformat(since.replace('Z', '+00:00'))
    now = datetime.datetime.now(datetime.timezone.utc)
    if parsed.tzinfo is None or not 0 <= (now - parsed).total_seconds() <= 1800:
        raise ValueError('shutdown observation needs an explicit last-30-minute UTC start')
    # Docker sends application logs to either stream. Do not retain their text.
    result = subprocess.run(['docker', 'logs', '--since', since, controller['Id']],
                            capture_output=True, check=True, timeout=10)
    raw = result.stdout + result.stderr
    if len(raw) > 1048576:
        raise ValueError('shutdown logs exceed observation bound')
    received = raw.count(b'shutdown: signal received')
    failed = raw.count(b'shutdown: http server shutdown failed')
    closed_error = raw.count(b'shutdown: store close failed')
    s = controller['State']
    finished = datetime.datetime.fromisoformat(s['FinishedAt'].replace('Z', '+00:00')) if not s['Running'] else None
    observed = (not s['Running'] and s['Pid'] == 0 and s['ExitCode'] == 0 and not s['OOMKilled']
                and finished is not None and finished >= parsed and received == 1 and failed == 0 and closed_error == 0)
    return {'checked': True, 'signal_events': received, 'http_shutdown_errors': failed,
            'store_close_errors': closed_error, 'regular_http_shutdown_success_observed': observed,
            'hijacked_websocket_completion_proven': False}


def collect(controller_id, since=None):
    report = {'version': 1, 'observed_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'observation_only': True, 'drain_receipt_created': False, 'errors': []}
    def checked(name, fn):
        try:
            report[name] = fn()
        except Exception as error:
            report[name] = None
            report['errors'].append({'check': name, 'error_class': type(error).__name__})
    controller = json.loads(command(['docker', 'inspect', 'src-sandboxd-1']))[0]
    if controller['Id'] != controller_id:
        raise ValueError('controller identity changed')
    report['controller'] = {'id': controller_id, 'image': controller['Image'],
                            'running': controller['State']['Running'], 'pid': controller['State']['Pid'],
                            'restart_policy': controller['HostConfig']['RestartPolicy']['Name']}
    checked('controller_sockets', lambda: process_sockets(controller['State']['Pid']) if controller['State']['Pid'] else None)
    checked('controller_shutdown', lambda: shutdown_observation(controller, since))
    checked('database', sqlite_observation)
    values = {}
    for name in WRITERS:
        for suffix in ('.service', '.timer'):
            checked(name + suffix, lambda n=name + suffix: unit(n))
            values[name + suffix] = report[name + suffix]
    report['scheduled_runtime_writers_inactive'] = all(
        value and value.get('LoadState') == 'loaded' and value.get('ActiveState') == 'inactive'
        and value.get('MainPID', '0') == '0' for value in values.values())
    checked('platform_unit', lambda: unit('baarcha-landing.service'))
    if report['platform_unit']:
        checked('platform_sockets', lambda: [process_sockets(p) for p in unit_processes(report['platform_unit'])])
    def caddy():
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open('http://127.0.0.1:2019/config/', timeout=5) as response:
            raw = response.read(1048577)
        if len(raw) > 1048576:
            raise ValueError('Caddy config bound exceeded')
        expected = Path('/opt/baarcha-cube/worker-01/cutover-routing/offline.json').read_bytes()
        return {'loaded_sha256': hashlib.sha256(raw).hexdigest(),
                'reviewed_offline_sha256': hashlib.sha256(expected).hexdigest(),
                'equals_reviewed_offline': json.loads(raw) == json.loads(expected)}
    checked('caddy', caddy)
    report['limits'] = [
        'TCP counts include idle keepalive pools and cannot identify HTTP methods, paths or active handlers.',
        'A stable sampled socket set does not prove absence of delayed tool/thumbnail work.',
        'Controller normal HTTP shutdown excludes hijacked WebSockets; no automatic completion assertion for them.',
        'Inference-only model and voice connections are intentionally not stop prerequisites.',
        'Direct operator writers, provider jobs, platform thumbnail leases and fence route coverage need separate evidence.',
        'This report never authorizes a drain or sets pre-drain receipt flags.'
    ]
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--controller-id', required=True)
    parser.add_argument('--shutdown-since')
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    if sys.platform != 'linux' or os.geteuid() != 0 or not re.fullmatch('[a-f0-9]{64}', args.controller_id):
        parser.error('native Linux root and exact controller ID required')
    path = Path(args.output)
    if not path.is_absolute() or path.parent.resolve(strict=True) != path.parent:
        parser.error('real absolute output directory required')
    if path.parent.stat().st_uid != 0 or path.parent.stat().st_mode & 0o077:
        parser.error('root-private output directory required')
    value = collect(args.controller_id, args.shutdown_since)
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'w') as output:
        json.dump(value, output, indent=2); output.write('\n')
    print(json.dumps({'output': str(path), 'observation_only': True, 'error_count': len(value['errors'])}))
    return 1 if value['errors'] else 0


if __name__ == '__main__':
    raise SystemExit(main())
