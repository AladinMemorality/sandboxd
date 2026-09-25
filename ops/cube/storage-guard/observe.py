#!/usr/bin/env python3
"""Root-only, bounded read-only storage probe; publishes no credentials.

Run once from a systemd timer. This script never starts/stops guests or services,
changes routes, grows filesystems, or mounts tenant data. Only its own sequence,
lock, and observation files are written. See README.md for enrollment.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile
import time

STATE_DIR = Path('/var/lib/sandboxd/cube-storage-observer')
OUTPUT_DIR = Path('/run/sandboxd-cube-storage')
SSH = ['/usr/bin/ssh', '-i', '/opt/baarcha-cube/worker-01/operator-key', '-p', '20222',
       '-oBatchMode=yes', '-oConnectTimeout=5', '-oServerAliveInterval=3',
       '-oServerAliveCountMax=1', '-oStrictHostKeyChecking=yes',
       '-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts', 'root@127.0.0.1']
HEX = re.compile(r'[0-9a-f]{32}\Z')
UUID = re.compile(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z')
CONFIG_KEYS = {'observation_path', 'observer_id', 'worker_machine_id', 'expected_boot_id', 'inner_fs_uuid', 'outer_fs_uuid', 'outer_boot_id'}
# Fixed command, no interpolated paths or config. The clock belongs to the outer
# host shared with the controller; nested wall-clock offset cannot skew epochs.
PROBE = r'''
import json, os, subprocess
from pathlib import Path
boot=Path('/proc/sys/kernel/random/boot_id').read_text().strip()
source,kind,uuid=subprocess.check_output(['findmnt','-n','-o','SOURCE,FSTYPE,UUID','--target','/data'],text=True).split()
if source!='/dev/vdb' or kind!='xfs': raise SystemExit('unexpected storage mount')
s=os.statvfs('/data')
if boot!=Path('/proc/sys/kernel/random/boot_id').read_text().strip(): raise SystemExit('boot changed')
print(json.dumps({'worker_machine_id':Path('/etc/machine-id').read_text().strip(),'worker_boot_id':boot,'inner_fs_uuid':uuid,'inner_free_bytes':s.f_bavail*s.f_frsize}))
'''

class Invalid(Exception):
    pass


def strict_json(raw):
    def unique(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise Invalid('duplicate JSON field')
            value[key] = item
        return value
    return json.loads(raw, object_pairs_hook=unique)


def trusted_read(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        s = os.fstat(fd)
        if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022 or s.st_nlink != 1 or s.st_size > 8192:
            raise Invalid('untrusted operator file')
        raw = os.read(fd, 8193)
        if len(raw) > 8192:
            raise Invalid('operator file too large')
        return strict_json(raw)
    finally:
        os.close(fd)


def validate_config(c):
    if set(c) != CONFIG_KEYS or c['observation_path'] != str(OUTPUT_DIR / 'observation.json'):
        raise Invalid('unexpected observer configuration')
    for key in ('observer_id', 'worker_machine_id'):
        if not isinstance(c[key], str) or not HEX.fullmatch(c[key]) or c[key] == '0' * 32:
            raise Invalid('invalid pinned identity')
    for key in ('expected_boot_id', 'inner_fs_uuid', 'outer_fs_uuid', 'outer_boot_id'):
        if not isinstance(c[key], str) or not UUID.fullmatch(c[key]):
            raise Invalid('invalid pinned filesystem or boot identity')


def secure_dir(path):
    path.mkdir(mode=0o700, parents=False, exist_ok=True)
    s = path.lstat()
    if not stat.S_ISDIR(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022:
        raise Invalid('untrusted output directory')


def atomic_json(path, value):
    fd, temp = tempfile.mkstemp(prefix='.pending-', dir=path.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write((json.dumps(value, sort_keys=True, separators=(',', ':')) + '\n').encode())
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, path)
        d = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(d)
        finally:
            os.close(d)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def probe_inner():
    result = subprocess.run(SSH + ['python3', '-'], input=PROBE, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=12, check=True)
    if len(result.stdout) > 4096:
        raise Invalid('oversized worker observation')
    return strict_json(result.stdout)


def probe_outer():
    target = Path('/mnt/nvme/baarcha-cube/worker-01/data.qcow2')
    s = target.lstat()
    if not stat.S_ISREG(s.st_mode) or s.st_uid != 0:
        raise Invalid('unexpected worker disk backing file')
    fields = subprocess.check_output(['/usr/bin/findmnt', '-n', '-o', 'FSTYPE,UUID', '--target', str(target)], text=True, timeout=2).split()
    if len(fields) != 2 or fields[0] not in ('xfs', 'ext4'):
        raise Invalid('unexpected outer storage filesystem')
    v = os.statvfs(target.parent)
    return {'outer_fs_uuid': fields[1], 'outer_free_bytes': v.f_bavail * v.f_frsize}


def boottime_ns():
    return time.clock_gettime_ns(time.CLOCK_BOOTTIME)


def read_outer_boot():
    return Path('/proc/sys/kernel/random/boot_id').read_text().strip()


def make_observation(c, previous, inner_probe=probe_inner, outer_probe=probe_outer, clock=boottime_ns, outer_boot=read_outer_boot):
    validate_config(c)
    if outer_boot() != c["outer_boot_id"]:
        raise Invalid("outer boot differs from reviewed pin")
    start = clock()
    if previous:
        if set(previous) != {'observer_id', 'generation', 'started_ns', 'outer_boot_id'} or previous['observer_id'] != c['observer_id'] or type(previous['generation']) is not int or previous['generation'] < 1 or previous['generation'] >= (1 << 63)-1 or type(previous['started_ns']) is not int or (previous['outer_boot_id'] == c['outer_boot_id'] and start <= previous['started_ns']):
            raise Invalid('observer sequence or clock moved backwards')
    inner, outer = inner_probe(), outer_probe()
    if set(inner) != {'worker_machine_id', 'worker_boot_id', 'inner_fs_uuid', 'inner_free_bytes'} or set(outer) != {'outer_fs_uuid', 'outer_free_bytes'}:
        raise Invalid('unexpected probe fields')
    pins = {'worker_machine_id': c['worker_machine_id'], 'worker_boot_id': c['expected_boot_id'], 'inner_fs_uuid': c['inner_fs_uuid'], 'outer_fs_uuid': c['outer_fs_uuid']}
    combined = {**inner, **outer}
    if any(combined[k] != v for k, v in pins.items()):
        raise Invalid('probe identity differs from reviewed pins')
    for key in ('inner_free_bytes', 'outer_free_bytes'):
        if type(combined[key]) is not int or not 0 <= combined[key] < (1 << 63):
            raise Invalid('invalid free-space counter')
    done = clock()
    if outer_boot() != c["outer_boot_id"] or done < start or done - start > 20_000_000_000:
        raise Invalid('probe clock moved or probe took too long')
    return {'version': 1, 'observer_id': c['observer_id'], 'generation': (previous['generation'] if previous else 0) + 1,
            'outer_boot_id': c['outer_boot_id'], 'started_boottime_ns': start, 'completed_boottime_ns': done, **combined}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True, type=Path)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise Invalid('observer requires root')
    c = trusted_read(args.config)
    validate_config(c)
    secure_dir(STATE_DIR)
    secure_dir(OUTPUT_DIR)
    lock = os.open(STATE_DIR / 'lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        state_path = STATE_DIR / 'sequence.json'
        old = trusted_read(state_path) if state_path.exists() else None
        o = make_observation(c, old)
        # Persist the generation BEFORE publishing. A crash may skip a number,
        # but cannot publish the same epoch with different bytes after restart.
        atomic_json(state_path, {'observer_id':o['observer_id'], 'generation':o['generation'], 'started_ns':o['started_boottime_ns'], 'outer_boot_id':o['outer_boot_id']})
        atomic_json(Path(c['observation_path']), o)
    finally:
        os.close(lock)

if __name__ == '__main__':
    try:
        main()
    except (Invalid, OSError, ValueError, subprocess.SubprocessError):
        raise SystemExit('Cube storage observation unavailable; previous observation not refreshed')
