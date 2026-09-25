#!/usr/bin/env python3
"""Offline preparation only. Does not call APIs, execute code or alter a guest."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat

WORKER = 'operator_frontend_profile'
BASE = '.operator-frontend-build-20260925'
APP = '01M3CZB4HXT2Y8HP8CEY75PCWY'
SANDBOX = '01M3D1Q0E1KM1FEM244XVHEC65'


def need(ok, message):
    if not ok:
        raise ValueError(message)


def command(nonce):
    need(bool(re.fullmatch('[0-9a-f]{16}', nonce)), 'invalid run nonce')
    return 'python3 %s/%s/input/guest.py --run %s' % (BASE, nonce, nonce)


def candidate(original, nonce):
    need(len(original.encode()) <= 65536, 'manifest too large')
    need(WORKER not in original, 'worker already present')
    lines = original.splitlines(keepends=True)
    need(not any(line.strip() in ('---', '...') or re.match(r'^\s*\t', line) for line in lines), 'unsupported YAML layout')
    matches = [i for i, line in enumerate(lines) if re.match(r'^workers:', line)]
    need(len(matches) <= 1, 'duplicate workers key')
    indent = '  '
    insert = len(lines)
    if matches:
        at = matches[0]
        header = lines[at].strip()
        if re.fullmatch(r'workers:\s*\[\]\s*(?:#.*)?', header):
            lines[at] = 'workers:\n'
            insert = at + 1
        else:
            need(bool(re.fullmatch(r'workers:\s*(?:#.*)?', header)), 'workers must be a plain block list')
            insert = at + 1
            while insert < len(lines):
                line = lines[insert]
                if line.strip() and not line.startswith((' ', '#', '-')):
                    break
                if re.match(r'^ *- ', line):
                    indent = re.match(r'^( *)-', line)[1]
                insert += 1
    else:
        if lines and not lines[-1].endswith('\n'):
            lines[-1] += '\n'
        lines.append('workers:\n')
        insert = len(lines)
    worker = '%s- name: %s\n%s  command: %s\n' % (indent, WORKER, indent, command(nonce))
    if insert and not lines[insert - 1].endswith('\n'):
        lines[insert - 1] += '\n'
    lines.insert(insert, worker)
    return ''.join(lines)


def verify_validations(before, after, nonce):
    need(before.get('valid') is True and after.get('valid') is True, 'runtime manifest validation failed')
    expected = json.loads(json.dumps(before['effective']))
    workers = expected.get('workers') or []
    need(not any(w['name'] == WORKER for w in workers), 'existing worker conflict')
    expected['workers'] = workers + [{'name': WORKER, 'command': command(nonce)}]
    need(after.get('effective') == expected, 'candidate changes existing service definitions')


def verify_restore(current, original, installed_hash):
    need(hashlib.sha256(current).hexdigest() == installed_hash, 'manifest drift: do not overwrite')
    need(bool(original), 'original manifest required')
    return original  # exact bytes, not reserialized YAML


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--original', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--run', required=True)
    args = parser.parse_args()
    need(args.original.resolve() == args.original, 'canonical manifest input required')
    fd = os.open(args.original, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as f:
        st = os.fstat(f.fileno())
        need(stat.S_ISREG(st.st_mode) and st.st_size <= 65536, 'invalid manifest input')
        original = f.read(65537)
        need(len(original) <= 65536, 'manifest limit')
    replacement = candidate(original.decode(), args.run).encode()
    args.out.mkdir(mode=0o700)  # no reuse/overwrite
    source = Path(__file__).resolve().parent
    files = {'original.yaml': original, 'candidate.yaml': replacement,
             'guest.py': (source / 'guest.py').read_bytes(), 'probe.mjs': (source / 'probe.mjs').read_bytes()}
    plan = {'version': 1, 'run': args.run, 'app_id': APP, 'sandbox_id': SANDBOX,
            'owner_external_id': 'baarcha:103', 'worker': WORKER, 'command': command(args.run),
            'guest_stage': BASE + '/' + args.run, 'automatic_execution': False,
            'service_restart_notice': 'Adding and removing this worker reloads the supervisor, briefly restarting notes and PostgreSQL.',
            'files': {name: hashlib.sha256(data).hexdigest() for name, data in files.items()}}
    files['plan.json'] = (json.dumps(plan, indent=2) + '\n').encode()
    for name, data in files.items():
        fd = os.open(args.out / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'wb') as f:
            f.write(data); f.flush(); os.fsync(f.fileno())
    fd = os.open(args.out, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


if __name__ == '__main__':
    main()
