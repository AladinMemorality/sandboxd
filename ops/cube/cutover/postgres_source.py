"""Read-only checks for the existing MyHomeTroc embedded PostgreSQL source."""
import hashlib
import os
from pathlib import Path
import re
import stat

APP = '01M37PPK7VDKCQMNRYKW8CCD4W'
SANDBOX = '01M37PPK85JN1K0WEMP4ZYER6C'
SOCKET = '.myhometroc/socket/.s.PGSQL.5432'
PID = '.myhometroc/pgdata/postmaster.pid'
LOG = '.runtimed/postgres.log'


def need(ok, message):
    if not ok: raise RuntimeError(message)


def regular(home, name, limit, uid=1000):
    root = Path(home)
    need(root.resolve(strict=True) == root, 'Canonical owner home required')
    path = root / name
    need(path.resolve(strict=True) == path, 'PostgreSQL proof path contains a link')
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        info = os.fstat(fd)
        need(stat.S_ISREG(info.st_mode) and info.st_uid == uid and info.st_nlink == 1
             and info.st_size <= limit, 'Unexpected PostgreSQL proof file')
        with os.fdopen(fd, 'rb', closefd=False) as stream: data = stream.read(limit + 1)
        need(len(data) == info.st_size, 'PostgreSQL proof file changed while reading')
        return info, data
    finally:
        os.close(fd)


def version(home, uid=1000):
    _, data = regular(home, '.myhometroc/pgdata/PG_VERSION', 32, uid)
    need(data.strip() == b'18', 'Only the reviewed PostgreSQL 18 source is supported')


def live_socket_exception(home, manifest, uid=1000):
    """Online preflight only. The normal frozen manifest/export stays strict."""
    version(home, uid)
    specials = []
    root = Path(home)
    for entry in manifest['entries']:
        if entry['disposition'] not in ('preserve', 'stock'): continue
        start = root / entry['path']
        if not start.is_dir() or start.is_symlink(): continue
        for parent, directories, files in os.walk(start, followlinks=False):
            for name in directories + files:
                path = Path(parent) / name
                info = path.lstat()
                if not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode)):
                    need(stat.S_ISSOCK(info.st_mode) and info.st_uid == uid, 'Unexpected special home file')
                    specials.append(str(path.relative_to(root)))
    need(specials == [SOCKET], 'Only the exact live PostgreSQL socket may defer home validation')
    return {'path': SOCKET, 'online_only': True, 'frozen_validation_required': True}


def capture(home, running, uid=1000):
    version(home, uid)
    info, _ = regular(home, LOG, 16 * 1024 * 1024, uid)
    pid = None
    if running:
        _, raw = regular(home, PID, 8192, uid)
        first = raw.splitlines()[0].decode('ascii')
        need(re.fullmatch(r'[1-9][0-9]{0,7}', first), 'Invalid PostgreSQL PID marker')
        pid = int(first)
    return {'running': running, 'pid': pid, 'log_inode': info.st_ino, 'log_device': info.st_dev, 'log_size': info.st_size}


def stopped(home, before, uid=1000):
    """Caller must first prove the exact source container has stopped cleanly."""
    version(home, uid)
    root = Path(home)
    for name in (SOCKET, SOCKET + '.lock', PID):
        need(not os.path.lexists(root / name), 'PostgreSQL live marker remains after stop')
    info, data = regular(root, LOG, 16 * 1024 * 1024, uid)
    need((info.st_dev, info.st_ino) == (before['log_device'], before['log_inode']), 'PostgreSQL log generation changed')
    lines = data.decode('utf-8', errors='strict').strip().splitlines()
    need(bool(lines), 'PostgreSQL shutdown log missing')
    match = re.search(r'\[([0-9]+)\]\s+LOG:\s+database system is shut down$', lines[-1])
    need(match is not None, 'Clean PostgreSQL shutdown was not acknowledged')
    if before['running']:
        need(info.st_size > before['log_size'] and int(match[1]) == before['pid'], 'Shutdown does not acknowledge the current PostgreSQL process')
    _, control = regular(root, '.myhometroc/pgdata/global/pg_control', 8192, uid)
    need(len(control) == 8192, 'Unexpected PostgreSQL control-file size')
    return {'postgres_major': 18, 'clean_shutdown_log': True, 'live_markers_absent': True,
            'control_sha256': hashlib.sha256(control).hexdigest(), 'shutdown_log_sha256': hashlib.sha256(data).hexdigest()}
