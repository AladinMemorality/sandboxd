#!/usr/bin/env python3
"""Read-only, native-worker preflight immediately before each serialized import.

No quota changes, pruning, mounting or remote requests. This is a point-in-time
check, not a reservation; keep template builders and other imports stopped.
"""
import json
import os
from pathlib import Path
import stat
import sys

MIN_FREE_BYTES = 48 * 1024 ** 3
DATA_MOUNT = Path('/data')
PATHS = (DATA_MOUNT / 'cubelet', DATA_MOUNT / 'cubelet/storage')


def checked_capacity(available_bytes, filesystem, mountpoint):
    if filesystem != 'xfs' or mountpoint != str(DATA_MOUNT):
        raise ValueError('expected dedicated /data XFS filesystem')
    if available_bytes < MIN_FREE_BYTES:
        raise ValueError('less than 48 GiB available on the worker data filesystem')
    return {'eligible': True, 'available_bytes': available_bytes,
            'minimum_available_bytes': MIN_FREE_BYTES, 'filesystem': filesystem,
            'mountpoint': mountpoint, 'reservation': False}


def data_mount_info(text):
    matching = []
    for line in text.splitlines():
        left, sep, right = line.partition(' - ')
        fields, kind = left.split(), right.split()
        if sep and len(fields) >= 6 and kind and fields[4] == str(DATA_MOUNT):
            matching.append((kind[0], fields[4]))
    if len(matching) != 1:
        raise ValueError('dedicated /data mount is missing or ambiguous')
    return matching[0]


def inspect():
    if os.geteuid() != 0:
        raise ValueError('run this check as native worker root')
    if Path('/.dockerenv').exists():
        raise ValueError('run outside containers in the worker mount namespace')
    kind, mount = data_mount_info(Path('/proc/self/mountinfo').read_text())
    device = os.lstat(DATA_MOUNT).st_dev
    for target in PATHS:
        for path in (target, *target.parents):
            if path == Path('/'):
                break
            info = os.lstat(path)
            if not stat.S_ISDIR(info.st_mode):
                raise ValueError('data filesystem path is not a real directory')
        if os.lstat(target).st_dev != device:
            raise ValueError('Cube data paths do not share the reviewed /data mount')
    fs = os.statvfs(DATA_MOUNT)
    return checked_capacity(fs.f_bavail * fs.f_frsize, kind, mount)


def main():
    if len(sys.argv) != 1:
        print(json.dumps({'eligible': False, 'reason': 'no override flags accepted'}))
        return 2
    try:
        print(json.dumps(inspect()))
        return 0
    except (OSError, ValueError) as error:
        print(json.dumps({'eligible': False, 'reason': str(error),
                          'minimum_available_bytes': MIN_FREE_BYTES}))
        return 2


if __name__ == '__main__':
    sys.exit(main())
