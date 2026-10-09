"""Serialize short provider mutations while archive transfers run independently."""
import contextlib, fcntl, os, stat

@contextlib.contextmanager
def lifecycle():
    path = '/opt/baarcha/operations/vps-50-profiles-20261008/migration-lifecycle.lock'
    fd = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        info = os.fstat(fd)
        assert stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_nlink == 1
        assert info.st_mode & 0o077 == 0
        fcntl.flock(fd, fcntl.LOCK_EX)
        yield
    finally:
        os.close(fd)
