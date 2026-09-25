#!/usr/bin/env python3
"""Prepared operator-only fixture wrapper. Never installs or stops a service."""
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import time

LOCKS = ('/opt/baarcha/deploy-release.lock', '/opt/sandboxd/deploy-state/deploy.lock',
         '/run/lock/cube-operator-acceptance.lock', '/opt/baarcha-bench/cube-workload-operator.lock')
ACTIONS = ('prepare', 'resume-rejected-app', 'create', 'fund', 'task', 'complete-timed-out-task', 'verify', 'inspect')


def private(p):
    p = Path(p)
    if not p.is_absolute() or p.resolve(strict=True) != p:
        raise ValueError('noncanonical private path')
    s = p.stat()
    if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or stat.S_IMODE(s.st_mode) != 0o600:
        raise ValueError('unsafe private file')
    return p


def sha(p):
    return hashlib.sha256(Path(p).read_bytes()).hexdigest()


def acquire(paths=LOCKS):
    handles = []
    try:
        for raw in paths:
            p = Path(raw)
            if p.resolve(strict=True) != p:
                raise ValueError('unsafe lock path')
            fd = os.open(p, os.O_RDWR | os.O_NOFOLLOW)
            handles.append(fd)
            s = os.fstat(fd)
            if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_nlink != 1:
                raise ValueError('unsafe lock identity')
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return handles
    except BaseException:
        for fd in handles:
            os.close(fd)
        raise


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--config', required=True)
    p.add_argument('--action', choices=ACTIONS, required=True)
    p.add_argument('--execute-sha256', required=True, help='Exact fixture.mjs hash after review')
    a = p.parse_args()
    if sys.platform != 'linux' or os.getuid() != 0:
        raise ValueError('native Linux root required')
    config = json.loads(private(a.config).read_text())
    helper = Path(__file__).resolve().parents[2] / 'cutover/enrollment/execute.py'
    if sha(helper) != config['enrollment_runner_sha256']:
        raise ValueError('reviewed lock helper changed')
    js = Path(__file__).resolve().with_name('fixture.mjs')
    if sha(js) != a.execute_sha256 or not re.fullmatch('[a-f0-9]{64}', a.execute_sha256):
        raise ValueError('fixture code changed')
    handles = acquire()
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, lambda *_: print('Signal noted; bounded fixture retains operator locks until child exits.', file=sys.stderr))
    nested = None
    child = None
    try:
        spec = importlib.util.spec_from_file_location('cube_reviewed_lock', helper)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        nested = module.NestedLock(config['worker_boot_id'])
        env = dict(os.environ, CUBE_FIXTURE_LOCKED_PARENT=str(os.getpid()))
        child = subprocess.Popen(['/opt/baarcha/node22/bin/node', '--env-file=/opt/baarcha/landing.env',
                                  str(js), '--config', a.config, '--action', a.action,
                                  '--execute-sha256', a.execute_sha256], env=env)
        deadline = time.monotonic() + 600
        while child.poll() is None:
            nested.heartbeat()
            if time.monotonic() >= deadline:
                raise RuntimeError('fixture coordinator deadline; task has its own reviewed runtime limit')
            time.sleep(1)
        if child.returncode:
            raise RuntimeError('fixture failed; private mutation intent retained')
    finally:
        # Terminating only this exact child CLI cancels no worker process. The
        # submitted task's supervisor-enforced bound remains independent.
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                # Do not release fencing while a hung mutation-capable child
                # remains. Operator can inspect this exact PID and recover.
                print('Fixture child did not exit; retaining operator locks.', file=sys.stderr)
                while child.poll() is None:
                    time.sleep(1)
        if nested is not None:
            nested.close()
        for fd in handles:
            os.close(fd)


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('Fixture refused or stopped; inspect private journal. No retry/cleanup was performed.', file=sys.stderr)
        sys.exit(1)
