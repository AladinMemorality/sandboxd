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


def reconcile_empty_cache(home='/home/sandbox', uid=1000, gid=1000):
    """Caller must prove source.cache absent. Never remove cache/user payloads."""
    home = Path(home)
    if home.resolve(strict=True) != home: raise RuntimeError('Canonical owner home required')
    parent = os.open(home, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    child = None
    try:
        try: before = os.stat('.cache', dir_fd=parent, follow_symlinks=False)
        except FileNotFoundError: return dict(path='.cache', absent=True, removed=False)
        if not (stat.S_ISDIR(before.st_mode) and before.st_uid == uid and before.st_gid == gid and stat.S_IMODE(before.st_mode) == 0o755):
            return dict(path='.cache', absent=False, removed=False, reason='unreviewed metadata')
        child = os.open('.cache', os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent)
        if generation(os.fstat(child)) != generation(before): raise RuntimeError('Cache generation changed')
        entries = sorted(os.listdir(child))
        if entries not in ([], ['.gitkeep']):
            return dict(path='.cache', absent=False, removed=False, entries=entries[:32])
        if entries:
            item = os.stat('.gitkeep', dir_fd=child, follow_symlinks=False)
            if not (stat.S_ISREG(item.st_mode) and item.st_uid == uid and item.st_gid == gid and item.st_nlink == 1 and item.st_size == 0 and stat.S_IMODE(item.st_mode) == 0o644):
                return dict(path='.cache', absent=False, removed=False, reason='unreviewed placeholder')
            # Empty .cache/.gitkeep is already an approved stockHomeHashes entry
            # in runtime/private_home_linux.go. No nonempty cache is discarded.
            if generation(os.stat('.gitkeep', dir_fd=child, follow_symlinks=False)) != generation(item): raise RuntimeError('Placeholder changed')
            os.unlink('.gitkeep', dir_fd=child); os.fsync(child)
        if os.listdir(child): raise RuntimeError('Cache changed during reconciliation')
        current = os.stat('.cache', dir_fd=parent, follow_symlinks=False)
        if (current.st_dev, current.st_ino) != (before.st_dev, before.st_ino): raise RuntimeError('Cache replaced')
        os.rmdir('.cache', dir_fd=parent); os.fsync(parent)
        return dict(path='.cache', absent=True, removed=True, empty_stock_placeholder=bool(entries))
    finally:
        if child is not None: os.close(child)
        os.close(parent)
