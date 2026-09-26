"""Reconcile one reviewed template default absent from an imported owner home.

The caller must prove the source absence and imported/quiesced target identity.
No arbitrary path, hash or deletion list is accepted from a project.
"""
import hashlib
import os
from pathlib import Path
import stat

STOCK = b'# ~/.bash_logout: executed by bash(1) when login shell exits.\n\n# when leaving the console clear the screen to increase privacy\n\nif [ "$SHLVL" = 1 ]; then\n    [ -x /usr/bin/clear_console ] && /usr/bin/clear_console -q\nfi\n'
STOCK_SHA256 = '26882b79471c25f945c970f8233d8ce29d54e9d5eedcd2884f88affa84a18f56'


def generation(value):
    return (value.st_dev, value.st_ino, value.st_mode, value.st_uid, value.st_gid,
            value.st_nlink, value.st_size, value.st_mtime_ns, value.st_ctime_ns)


def reconcile(home='/home/sandbox', uid=1000, gid=1000):
    home = Path(home)
    if home.resolve(strict=True) != home:
        raise RuntimeError('Canonical owner home required')
    parent = os.open(home, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        try:
            before = os.stat('.bash_logout', dir_fd=parent, follow_symlinks=False)
        except FileNotFoundError:
            return dict(path='.bash_logout', absent=True, removed=False)
        if not (stat.S_ISREG(before.st_mode) and before.st_nlink == 1 and before.st_uid == uid
                and before.st_gid == gid and stat.S_IMODE(before.st_mode) == 0o644 and before.st_size == 220):
            raise RuntimeError('Unreviewed template default metadata; retained unchanged')
        fd = os.open('.bash_logout', os.O_RDONLY | os.O_NOFOLLOW, dir_fd=parent)
        with os.fdopen(fd, 'rb') as stream:
            if generation(os.fstat(stream.fileno())) != generation(before):
                raise RuntimeError('Template default generation changed')
            raw = stream.read(221)
            if raw != STOCK or hashlib.sha256(raw).hexdigest() != STOCK_SHA256:
                raise RuntimeError('Template default content differs; retained unchanged')
            if generation(os.fstat(stream.fileno())) != generation(before):
                raise RuntimeError('Template default changed during review')
        if generation(os.stat('.bash_logout', dir_fd=parent, follow_symlinks=False)) != generation(before):
            raise RuntimeError('Template default path changed')
        os.unlink('.bash_logout', dir_fd=parent)
        os.fsync(parent)
        return dict(path='.bash_logout', absent=True, removed=True, sha256=STOCK_SHA256, bytes=220)
    finally:
        os.close(parent)
