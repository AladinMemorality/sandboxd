#!/usr/bin/env python3
"""Destination-local import for disposable fleet test copies, never customer routing.

Only a small scoped job/receipt and digest proof cross the management link.
Encrypted archives download directly from S3 and all guest I/O stays loopback.
"""
import base64, concurrent.futures, fcntl, hashlib, http.client, json, os, re, socket, subprocess, sys, time, traceback, urllib.request, zipfile
from pathlib import Path
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

class LocalGuestError(Exception):
    def __init__(self, method, path, status):
        self.operation = method+" "+path
        self.status = status

def manifest(path):
    h = hashlib.sha256(); size = 0
    with zipfile.ZipFile(path) as z:
        names = z.namelist()
        assert len(names) == len(set(names)) and len(names) <= 200000
        for name in sorted(names):
            info = z.getinfo(name); digest = hashlib.sha256()
            with z.open(info) as src:
                while chunk := src.read(1024*1024): digest.update(chunk)
            size += info.file_size
            h.update(json.dumps([name, info.external_attr, info.file_size, digest.hexdigest()], separators=(',', ':')).encode())
    return dict(sha256=h.hexdigest(), files=len(names), expanded_bytes=size)

def main(job):
    os.umask(0o077)
    assert os.geteuid() == 0 and socket.gethostname() == 'baarcha-cube-worker-b200-01'
    assert re.fullmatch(r'[0-9A-HJKMNP-TV-Z]{26}', job['app_id'])
    assert job['test_owner'] == 'operator:fleet-100'
    receipt = job['receipt']; digest = receipt['workspace_sha256']
    assert re.fullmatch(r'[a-f0-9]{64}', digest)
    assert 0 < receipt['archive_bytes'] <= 4*1024**3
    assert receipt['url'].startswith('https://punicas.s3.eu-central-1.amazonaws.com/baarcha/cube/test-copies/')
    root = Path('/data/cube-fleet-test-copy-cache') / digest
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    archive = root/'workspace.zip'; hit = False; begin = time.monotonic()
    # Deduplicate equal revisions, including concurrent requests for the same cache.
    with (root/'cache.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if archive.exists():
            hit = manifest(archive)['sha256'] == digest
            assert hit, 'local cache digest differs'
        else:
            decrypt = Cipher(algorithms.AES(base64.b64decode(receipt['key'])), modes.GCM(base64.b64decode(receipt['iv']), base64.b64decode(receipt['tag']))).decryptor()
            decrypt.authenticate_additional_data(digest.encode())
            pending = root/'download.pending'; chunk_size = 4*1024*1024
            starts = list(range(0, receipt['archive_bytes'], chunk_size))
            def part(start):
                end = min(start+chunk_size, receipt['archive_bytes'])-1
                target = root/('encrypted-part-'+str(start))
                for attempt in range(3):
                    try:
                        header = root/('range-header-'+str(start))
                        # Bound total time and reject trickling connections. Socket
                        # inactivity timeouts alone never expire a slow response.
                        result = subprocess.run(['curl','--config','-','--noproxy','*',
                            '--fail','--silent','--connect-timeout','10','--max-time','40',
                            '--speed-limit','131072','--speed-time','10',
                            '--max-filesize',str(end-start+1),'--range',f'{start}-{end}',
                            '--dump-header',str(header),'--output',str(target)],
                            input=('url = '+json.dumps(receipt['url'])+'\n').encode(),
                            stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45)
                        assert result.returncode == 0
                        value = header.read_text().lower()
                        assert f'content-range: bytes {start}-{end}/{receipt["archive_bytes"]}' in value
                        assert target.stat().st_size == end-start+1
                        header.unlink(missing_ok=True)
                        return target
                    except Exception:
                        target.unlink(missing_ok=True)
                        if attempt == 2: raise
                        time.sleep(attempt+1)
            try:
                with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                    parts = list(pool.map(part, starts))
                with pending.open('wb') as out:
                    for target in parts:
                        with target.open('rb') as src:
                            while chunk := src.read(1024*1024): out.write(decrypt.update(chunk))
                    out.write(decrypt.finalize()); out.flush(); os.fsync(out.fileno())
                assert pending.stat().st_size == receipt['archive_bytes']
                assert manifest(pending)['sha256'] == digest
                os.replace(pending, archive)
            finally:
                pending.unlink(missing_ok=True)
                for start in starts:
                    (root/('encrypted-part-'+str(start))).unlink(missing_ok=True)
                    (root/('range-header-'+str(start))).unlink(missing_ok=True)
    fetched = time.monotonic()-begin
    if job.get('cache_only') is True:
        return dict(app_id=job['app_id'], workspace_sha256=digest, cache_ready=True,
                    cache_hit=hit, fetch_seconds=fetched, archive_bytes=receipt['archive_bytes'])
    route = json.loads(subprocess.check_output(['ip','-j','route','get','10.254.240.2']))
    assert route[0]['type'] == 'local' and route[0]['dev'] == 'lo', 'proxy must be on this worker'
    headers = job['headers']
    assert set(headers) == {'Host', 'Authorization', 'cube-traffic-access-token'}
    assert headers['Host'].startswith('3031-') and not any('\n' in str(v) or '\r' in str(v) for v in headers.values())
    def once(method, path, body=None):
        conn = http.client.HTTPConnection('10.254.240.2', 28080, timeout=600)
        try:
            extra = {} if body is None else {'Content-Length': str(archive.stat().st_size), 'Content-Type': 'application/zip'}
            conn.request(method, path, body, {**headers, **extra}); r = conn.getresponse()
            value = r.read(1024*1024+1); assert len(value) <= 1024*1024
            if r.status != 200: raise LocalGuestError(method, path, r.status)
            return json.loads(value) if value else None
        finally: conn.close()
    def request(method, path, body=None):
        until = time.monotonic()+60
        while True:
            try: return once(method, path, body)
            except LocalGuestError as error:
                # A newly-created VM can be running before its local supervisor
                # is reachable. Retry only idempotent control calls, never PUT.
                if body is not None or error.status not in (502,503,504) or time.monotonic()>=until: raise
                time.sleep(1)
    boot = request('GET', '/status')['runtimed']['booted_at']
    request('POST', '/workspace/quiesce')
    with archive.open('rb') as source: request('PUT', '/import/private-workspace-v2', source)
    until = time.monotonic()+90
    while True:
        try:
            if request('GET', '/status')['runtimed']['booted_at'] != boot: break
        except Exception: pass
        assert time.monotonic() < until, 'local supervisor restart timed out'
        time.sleep(.5)
    request('POST', '/workspace/quiesce')
    verification = root/(job['app_id']+'.verification.zip')
    conn = http.client.HTTPConnection('10.254.240.2', 28080, timeout=600)
    try:
        conn.request('GET', '/export/private-workspace-v2', headers=headers); r = conn.getresponse(); assert r.status == 200
        count = 0
        with verification.open('wb') as out:
            while chunk := r.read(1024*1024):
                count += len(chunk); assert count <= 4*1024**3; out.write(chunk)
        assert manifest(verification)['sha256'] == digest, 'imported workspace differs'
    finally:
        conn.close(); verification.unlink(missing_ok=True)
    request('POST', '/workspace/resume')
    return dict(app_id=job['app_id'], workspace_sha256=digest, workspace_verified=True,
                cache_hit=hit, fetch_seconds=fetched, copy_seconds=time.monotonic()-begin,
                archive_bytes=receipt['archive_bytes'], bulk_path='S3-to-worker; loopback-import-and-verification')

if __name__ == '__main__':
    try:
        raw = sys.stdin.buffer.read(32769); assert len(raw) <= 32768
        print(json.dumps(main(json.loads(raw))), flush=True)
    except Exception as error:
        # HTTP exceptions can contain presigned URLs. Never print them.
        print(json.dumps(dict(error=type(error).__name__, line=traceback.extract_tb(error.__traceback__)[-1].lineno, operation=getattr(error,'operation',None), status=getattr(error,'status',None))), flush=True)
        sys.exit(1)
