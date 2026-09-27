#!/usr/bin/env python3
"""Fixed forced SSH command for the private fleet storage observer key."""
import json
import os
from pathlib import Path
import stat
import subprocess
import sys


def main():
    assert os.environ.get('SSH_ORIGINAL_COMMAND') == 'observe'
    if sys.argv[1:] == ['inner']:
        assert os.geteuid() == 0
        assert subprocess.check_output(['hostname'], text=True).strip() == 'baarcha-cube-worker-b200-01'
        boot = Path('/proc/sys/kernel/random/boot_id').read_text().strip()
        source, kind, uuid = subprocess.check_output(['findmnt','-n','-o','SOURCE,FSTYPE,UUID','--target','/data'], text=True).split()
        assert source == '/dev/vdb' and kind == 'xfs'
        s = os.statvfs('/data')
        assert boot == Path('/proc/sys/kernel/random/boot_id').read_text().strip()
        result = {'worker_machine_id':Path('/etc/machine-id').read_text().strip(),
                  'worker_boot_id':boot, 'inner_fs_uuid':uuid,
                  'inner_free_bytes':s.f_bavail*s.f_frsize}
    elif sys.argv[1:] == ['outer']:
        assert os.geteuid() == 1013
        target = Path('/raid/baarcha-cube-worker-b200-01/data.qcow2')
        s = target.lstat()
        assert stat.S_ISREG(s.st_mode) and s.st_uid == 1013
        kind, uuid = subprocess.check_output(['findmnt','-n','-o','FSTYPE,UUID','--target',str(target)], text=True).split()
        assert kind in ('xfs','ext4')
        s = os.statvfs(target.parent)
        result = {'outer_fs_uuid':uuid, 'outer_free_bytes':s.f_bavail*s.f_frsize}
    else:
        raise ValueError('unknown probe')
    print(json.dumps(result))


if __name__ == '__main__':
    try:
        main()
    except Exception:
        raise SystemExit('Storage probe unavailable')
