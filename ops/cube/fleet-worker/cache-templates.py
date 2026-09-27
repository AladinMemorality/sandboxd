#!/usr/bin/env python3
"""Prewarm supplied private receipts; registration/admission are separate steps."""
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


def cache(receipt):
    m = json.loads(receipt.read_text())
    assert re.fullmatch(r'rfs-[a-f0-9-]+', m['artifact_id'])
    destination = Path('/data/cube-fleet-artifact-cache') / m['artifact_id'] / 'rootfs.ext4'
    if destination.exists():
        assert destination.stat().st_size == m['ext4_size_bytes']
        h = hashlib.sha256()
        with destination.open('rb') as f:
            while chunk := f.read(1024*1024): h.update(chunk)
        assert h.hexdigest() == m['ext4_sha256']
        info = destination.stat()
        marker = destination.parent / 'verified.pending'
        marker.write_text(json.dumps({'sha256':h.hexdigest(), 'generation':[info.st_ino, info.st_size, info.st_mtime_ns]}))
        os.replace(marker, destination.parent / 'verified.json')
        print(json.dumps({'artifact':m['artifact_id'], 'cache_verified':True}), flush=True)
        return
    subprocess.run(['/usr/bin/python3', '/root/cube-fleet-install/fetch-artifact.py', str(receipt)], check=True, timeout=1800)


def main():
    assert os.geteuid() == 0
    receipts = sorted(Path('/root/cube-fleet-install/artifact-receipts').glob('*.receipt.json'))
    assert 0 < len(receipts) <= 9
    with ThreadPoolExecutor(max_workers=2) as pool:
        list(pool.map(cache, receipts))
    print(json.dumps({'verified_templates':len(receipts)}), flush=True)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        raise SystemExit('Template cache batch failed: '+type(error).__name__)
