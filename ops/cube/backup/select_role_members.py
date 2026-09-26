#!/usr/bin/env python3
"""Extract only reviewed regular members from authenticated restored role archives.

No archive.extract()/extractall(), preserved ownership, links, or executable modes.
This does not decrypt, boot, restore PostgreSQL, or contact any service.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import tarfile
import tempfile

import cold_pair

ROLES = {'rollback', 'controller-config', 'worker-config', 'worker-launch', 'library'}
HASH = re.compile(r'^[0-9a-f]{64}$')
MAX_MEMBER = 32 << 20
MAX_TOTAL = 256 << 20
MAX_HEADERS = 2_000_000


def need(ok, message):
    if not ok:
        raise RuntimeError(message)


def canonical_member(value):
    need(isinstance(value, str) and value and '\x00' not in value,
         'Invalid member path')
    p = PurePosixPath(value)
    need(not p.is_absolute() and all(x not in ('', '.', '..') for x in value.split('/'))
         and str(p) == value, 'Noncanonical archive member')
    return value


def regular(path, max_size=None):
    path = Path(path)
    info = path.lstat()
    need(path.is_absolute() and path.resolve() == path and stat.S_ISREG(info.st_mode)
         and info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == 0o600
         and info.st_nlink == 1, 'Unsafe private input')
    need(max_size is None or info.st_size <= max_size, 'Oversized input')
    return path


def read_json(path, bound):
    return json.loads(regular(path, bound).read_bytes())


def validate_plan(plan):
    need(set(plan) == {'version', 'capture_directory', 'capture_manifest_sha256', 'selections'}
         and plan['version'] == 1 and HASH.fullmatch(plan['capture_manifest_sha256']),
         'Unreviewed selection plan')
    selections = plan['selections']
    need(isinstance(selections, list) and 1 <= len(selections) <= 64,
         'Bounded explicit selection required')
    targets, members = set(), set()
    for item in selections:
        need(set(item) == {'role', 'member', 'output', 'sha256', 'bytes'}, 'Unknown selection field')
        need(item['role'] in ROLES and HASH.fullmatch(item['sha256']), 'Unreviewed role or hash')
        canonical_member(item['member'])
        need(re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}', item['output'])
             and item['output'] not in ('complete.json', 'INCOMPLETE.json'), 'Unsafe output name')
        need(type(item['bytes']) is int and 0 <= item['bytes'] <= MAX_MEMBER, 'Member exceeds bound')
        need(item['output'] not in targets and (item['role'], item['member']) not in members,
             'Duplicate selection')
        targets.add(item['output'])
        members.add((item['role'], item['member']))
    need(sum(x['bytes'] for x in selections) <= MAX_TOTAL, 'Selected total exceeds bound')


def copy_selected(archive_path, selections, stage):
    wanted = {x['member']: x for x in selections}
    seen = set()
    # Seekable tar skips unselected payloads; clear remembered members so a large
    # owner-home archive cannot accumulate millions of TarInfo objects in RAM.
    with tarfile.open(archive_path, 'r:') as archive:
        for number, member in enumerate(archive):
            need(number < MAX_HEADERS, 'Archive header limit exceeded')
            name = member.name.rstrip('/') if member.isdir() else member.name
            canonical_member(name)
            if name in wanted:
                item = wanted[name]
                need(name not in seen and member.isfile() and not member.issparse()
                     and member.size == item['bytes'], 'Selected member type/size/uniqueness differs')
                seen.add(name)
                target = stage / item['output']
                fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                h, total = hashlib.sha256(), 0
                with os.fdopen(fd, 'wb') as dest, archive.extractfile(member) as source:
                    while True:
                        block = source.read(min(1 << 20, item['bytes'] - total + 1))
                        if not block:
                            break
                        total += len(block)
                        need(total <= item['bytes'], 'Selected payload exceeds reviewed size')
                        h.update(block)
                        dest.write(block)
                    dest.flush()
                    os.fsync(dest.fileno())
                need(total == item['bytes'] and h.hexdigest() == item['sha256'],
                     'Selected payload differs from reviewed source')
            archive.members.clear()
    need(seen == set(wanted), 'Selected source member missing')


def extract(plan, output):
    validate_plan(plan)
    capture = cold_pair.private_directory(plan['capture_directory'])
    manifest_path = regular(capture / 'manifest.json', 1 << 20)
    need(cold_pair.digest(manifest_path) == plan['capture_manifest_sha256'],
         'Authenticated capture manifest changed')
    manifest = read_json(manifest_path, 1 << 20)
    need(manifest.get('version') == 1 and manifest.get('kind') == 'cold-cube-pair',
         'Wrong capture manifest')
    cold_pair.capture_timestamp(manifest)
    output = Path(output).absolute()
    cold_pair.private_directory(output.parent)
    need(not output.exists() and not output.is_symlink(), 'Output must be new')
    roles = sorted({x['role'] for x in plan['selections']})
    for role in roles:
        path = regular(capture / role)
        record = manifest['files'][role]
        need(path.stat().st_size == record['bytes'] and cold_pair.digest(path) == record['sha256'],
             'Selected role archive differs from authenticated capture')
    stage = Path(tempfile.mkdtemp(prefix='.incomplete-selected-', dir=output.parent))
    for role in roles:
        copy_selected(capture / role, [x for x in plan['selections'] if x['role'] == role], stage)
        need(cold_pair.digest(capture / role) == manifest['files'][role]['sha256'],
             'Role archive changed during selection')
    need(cold_pair.digest(manifest_path) == plan['capture_manifest_sha256'],
         'Capture manifest changed during selection')
    receipt = {'version': 1, 'capture_manifest_sha256': plan['capture_manifest_sha256'],
               'selection_plan_sha256': hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(',', ':')).encode()).hexdigest(),
               'files': {x['output']: {'sha256': x['sha256'], 'bytes': x['bytes'], 'role': x['role'], 'member': x['member']}
                         for x in plan['selections']}, 'application_restore_verified': False}
    write_receipt(stage / 'complete.json', receipt)
    fd = os.open(stage, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
    cold_pair.publish_directory(stage, output)
    fd = os.open(output.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
    return receipt


def write_receipt(path, data):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump(data, out, indent=2)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())


def main():
    cold_pair.native_host()
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--plan', required=True)
    p.add_argument('--output', required=True)
    args = p.parse_args()
    receipt = extract(read_json(args.plan, 1 << 20), args.output)
    print(json.dumps({'selected_files_verified': len(receipt['files']), 'application_restore_verified': False}))


if __name__ == '__main__':
    try:
        main()
    except Exception:
        raise SystemExit('Selected-role extraction refused; incomplete private evidence retained.')
