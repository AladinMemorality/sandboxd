#!/usr/bin/env python3
"""Authenticate and prewarm an immutable Cube rootfs cache from a private receipt."""
import base64
from concurrent.futures import ThreadPoolExecutor
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes


def main():
    assert os.geteuid() == 0
    assert subprocess.check_output(['hostname'], text=True).strip() == 'baarcha-cube-worker-b200-01'
    os.umask(0o077)
    receipt = Path(sys.argv[1])
    st = receipt.stat()
    assert st.st_uid == 0 and not st.st_mode & 0o077 and st.st_size < 16384
    m = json.loads(receipt.read_text())
    assert re.fullmatch(r'rfs-[a-f0-9-]+', m['artifact_id'])
    assert re.fullmatch(r'[a-f0-9]{64}', m['ext4_sha256'])
    assert 0 < m['ext4_size_bytes'] <= 8 * 1024**3
    assert m['url'].startswith('https://punicas.s3.eu-central-1.amazonaws.com/baarcha/cube/runtime-cache/')
    root = Path('/data/cube-fleet-artifact-cache')
    root.mkdir(mode=0o700, exist_ok=True)
    target = root / m['artifact_id']
    target.mkdir(mode=0o700, exist_ok=True)
    assert not target.is_symlink() and target.stat().st_uid == 0 and not target.stat().st_mode & 0o077
    compressed = target / 'rootfs.gz'
    decryptor = Cipher(algorithms.AES(base64.b64decode(m['key'])), modes.GCM(
        base64.b64decode(m['iv']), base64.b64decode(m['tag']))).decryptor()
    decryptor.authenticate_additional_data(m['ext4_sha256'].encode())
    encrypted = target / 'rootfs.gz.enc'
    chunk_size = 32 * 1024**2
    ranges = list(range(0, m['compressed_bytes'], chunk_size))
    def download(start):
        end = min(start + chunk_size, m['compressed_bytes']) - 1
        part = target / ('part-' + str(start))
        if part.exists() and not part.is_symlink() and part.stat().st_size == end-start+1:
            return part
        assert not part.is_symlink()
        subprocess.run(['curl', '--fail', '--silent', '--show-error', '--retry', '5',
                        '--retry-all-errors', '--max-time', '300', '--max-filesize',
                        str(end-start+1), '--range', str(start)+'-'+str(end),
                        '--output', str(part), m['url']], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        assert part.stat().st_size == end-start+1
        return part
    # Bound parallel transfers. The complete GCM tag and raw SHA are checked
    # before publishing, including if a server ignores or reorders ranges.
    with ThreadPoolExecutor(max_workers=8) as pool:
        parts = list(pool.map(download, ranges))
    with encrypted.open('wb') as out:
        for part in parts:
            with part.open('rb') as source:
                while chunk := source.read(1024 * 1024): out.write(chunk)
    assert encrypted.stat().st_size == m['compressed_bytes']
    count = 0
    with encrypted.open('rb') as response, compressed.open('wb') as out:
        while chunk := response.read(1024 * 1024):
            count += len(chunk)
            assert count <= m['compressed_bytes']
            out.write(decryptor.update(chunk))
        out.write(decryptor.finalize())
        assert count == m['compressed_bytes']
        out.flush(); os.fsync(out.fileno())
    raw = target / 'rootfs.ext4'
    digest, count = hashlib.sha256(), 0
    with gzip.open(compressed, 'rb') as source, raw.open('wb') as out:
        while chunk := source.read(1024 * 1024):
            count += len(chunk)
            assert count <= m['ext4_size_bytes']
            digest.update(chunk)
            out.write(chunk)
        out.flush(); os.fsync(out.fileno())
    assert count == m['ext4_size_bytes'] and digest.hexdigest() == m['ext4_sha256']
    # Cube owns registration and runtime companions. Only prewarm the exact
    # immutable artifact file it would download, never copy mutable metadata.
    cache = Path('/usr/local/services/cubetoolbox/cubebox_os_image') / m['artifact_id']
    cache.mkdir(parents=True, exist_ok=True)
    destination = cache / (m['artifact_id'] + '.ext4')
    assert not destination.exists()
    pending = cache / (m['artifact_id'] + '.prewarm')
    subprocess.run(['cp', '--reflink=auto', '--sparse=always', str(raw), str(pending)], check=True)
    with pending.open('rb') as f: os.fsync(f.fileno())
    os.rename(pending, destination)
    print(json.dumps({'artifact':m['artifact_id'], 'sha256':digest.hexdigest(), 'bytes':count, 'cache_ready':True}))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # Exceptions from HTTP libraries may embed the signed URL.
        raise SystemExit('Artifact cache transfer failed: ' + type(error).__name__)
