#!/usr/bin/env python3
"""Replace only a reviewed gateway binary; preserve config and roll back on failure."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import time
import urllib.request


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def ready():
    for _ in range(20):
        try:
            with urllib.request.urlopen('http://127.0.0.1:8095/healthz', timeout=2) as response:
                if response.status == 200:
                    return
        except OSError:
            time.sleep(.5)
    raise RuntimeError('Gateway readiness failed')


parser = argparse.ArgumentParser()
parser.add_argument('--candidate', required=True, type=Path)
parser.add_argument('--baseline', required=True)
parser.add_argument('--sha256', required=True)
parser.add_argument('--commit', required=True)
args = parser.parse_args()
os.umask(0o077)
live = Path('/opt/baarcha-preview/preview-gateway')
config = Path('/etc/baarcha-preview/gateway.json')
root = args.candidate.parent
backup = root / 'gateway.before'
with open('/run/lock/baarcha-preview-gateway-deploy.lock', 'a') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    assert digest(live) == args.baseline, 'Live gateway changed'
    assert digest(args.candidate) == args.sha256, 'Candidate digest mismatch'
    assert not backup.exists(), 'Deployment already attempted; inspect receipt first'
    original_config = config.read_bytes()
    shutil.copy2(live, backup)
    replacement = live.with_suffix('.new')
    shutil.copy2(args.candidate, replacement)
    replacement.chmod(0o755)
    try:
        os.replace(replacement, live)
        subprocess.run(['systemctl', 'restart', 'baarcha-preview-gateway'], check=True)
        ready()
        assert config.read_bytes() == original_config, 'Gateway configuration changed'
        assert digest(live) == args.sha256
        receipt = {'deployed': True, 'commit': args.commit, 'sha256': args.sha256,
                   'previous_sha256': args.baseline, 'config_preserved': True}
        (root / 'deployed.json').write_text(json.dumps(receipt))
        print(json.dumps(receipt))
    except BaseException:
        shutil.copy2(backup, replacement)
        os.replace(replacement, live)
        subprocess.run(['systemctl', 'restart', 'baarcha-preview-gateway'], check=True)
        ready()
        raise
