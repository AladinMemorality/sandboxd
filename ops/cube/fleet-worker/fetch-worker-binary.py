#!/usr/bin/env python3
"""Fetch a reviewed compressed worker executable directly from S3; never install."""
import base64, concurrent.futures, gzip, hashlib, json, os, re, subprocess, sys
from pathlib import Path
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

def main():
    os.umask(0o077)
    assert os.geteuid() == 0
    raw = sys.stdin.buffer.read(16385)
    assert len(raw) <= 16384
    receipt = json.loads(raw)
    assert receipt['kind'] == 'worker-binary-v1'
    digest = receipt['sha256']
    assert re.fullmatch('[a-f0-9]{64}', digest)
    assert receipt['url'].startswith('https://punicas.s3.eu-central-1.amazonaws.com/baarcha/cube/runtime-cache/'+digest+'/')
    assert 0 < receipt['compressed_bytes'] <= 256*1024**2 and 0 < receipt['size_bytes'] <= 512*1024**2
    root = Path('/root/cube-worker-binaries') / digest
    root.mkdir(parents=True, mode=0o700, exist_ok=True)
    assert not root.is_symlink()
    target = root/'cubelet'
    if target.exists():
        assert hashlib.sha256(target.read_bytes()).hexdigest() == digest
        print(json.dumps({'sha256':digest,'verified':True,'cached':True}));return
    starts = range(0,receipt['compressed_bytes'],4*1024**2)
    def fetch(start):
        end=min(start+4*1024**2,receipt['compressed_bytes'])-1
        part=root/str(start)
        p=subprocess.run(['curl','--config','-','--noproxy','*','--fail','--silent','--retry','3','--retry-all-errors','--connect-timeout','10','--max-time','40','--speed-limit','131072','--speed-time','10','--range',f'{start}-{end}','--max-filesize',str(end-start+1),'-o',str(part)],input=('url = '+json.dumps(receipt['url'])+'\n').encode(),stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=180)
        assert p.returncode==0 and part.stat().st_size==end-start+1
        return part
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool: parts=list(pool.map(fetch,starts))
    decrypt=Cipher(algorithms.AES(base64.b64decode(receipt['key'])),modes.GCM(base64.b64decode(receipt['iv']),base64.b64decode(receipt['tag']))).decryptor()
    decrypt.authenticate_additional_data(digest.encode())
    compressed=root/'binary.gz'
    with compressed.open('xb') as out:
        for part in parts:
            with part.open('rb') as src:
                while data:=src.read(1024**2):out.write(decrypt.update(data))
        out.write(decrypt.finalize());out.flush();os.fsync(out.fileno())
    total=0;sha=hashlib.sha256()
    pending=root/'cubelet.pending'
    with gzip.open(compressed,'rb') as src,pending.open('wb') as out:
        while data:=src.read(1024**2):
            total+=len(data);assert total<=receipt['size_bytes'];sha.update(data);out.write(data)
        out.flush();os.fsync(out.fileno())
    assert total==receipt['size_bytes'] and sha.hexdigest()==digest
    os.replace(pending,target)
    for part in parts:part.unlink()
    compressed.unlink()
    print(json.dumps({'sha256':digest,'verified':True,'bytes':total,'installed':False}))

if __name__=='__main__':
    try:main()
    except Exception as error:raise SystemExit('Worker binary fetch failed: '+type(error).__name__)
