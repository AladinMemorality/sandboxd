#!/usr/bin/env python3
"""Inert off-host GPG streaming. No restore parser, boot or service action."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import time

ROOT = Path('/opt/baarcha-cube/backup-generations')
REMOTE_SCRIPT = '/opt/baarcha-cube/backup-tools/stream_restore.py'
REMOTE = 'root@65.108.225.153'
RECIPIENT = '25017F865BE80AA7E7E8C8925C595C2637931730'
MAX_BYTES = 1 << 40

def need(value, message):
    if not value: raise RuntimeError(message)

def private(path, directory=False):
    path = Path(path)
    need(path.is_absolute() and path.resolve(strict=True) == path, 'canonical private path required')
    info = path.stat()
    need(info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == (0o700 if directory else 0o600), 'private ownership/mode required')
    need(path.is_dir() if directory else path.is_file(), 'wrong file type')
    return path

def job_path(value, create=False):
    value = Path(value)
    private(ROOT, True)
    need(value.parent == ROOT and re.fullmatch(r'[A-Za-z0-9_-]{1,96}', value.name), 'new fixed-root job required')
    if create: value.mkdir(mode=0o700)
    return private(value, True)

def write_json(path, value):
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump(value, f, sort_keys=True); f.write('\n'); f.flush(); os.fsync(f.fileno())
    sync_dir(path.parent)

def read_json(path):
    path = private(path); need(path.stat().st_size <= 65536, 'bounded JSON required')
    return json.loads(path.read_text())

def sync_dir(path):
    fd = os.open(path, os.O_RDONLY)
    try: os.fsync(fd)
    finally: os.close(fd)

def sha_file(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024*1024), b''): h.update(block)
    return h.hexdigest()

def identity(path):
    x = path.stat()
    return x.st_dev, x.st_ino, x.st_size, x.st_mtime_ns, x.st_ctime_ns

def send(path, size, checksum, output):
    path = private(path)
    need(path.is_relative_to(ROOT) and path.name == 'readback.gpg', 'verified readback file required')
    need(0 < size <= MAX_BYTES and re.fullmatch('[a-f0-9]{64}', checksum), 'bounded expected ciphertext required')
    before = identity(path)
    need(before[2] == size and sha_file(path) == checksum, 'readback digest mismatch')
    with path.open('rb') as f: shutil.copyfileobj(f, output, 1024*1024)
    output.flush()
    need(identity(path) == before, 'ciphertext changed during send')

def receive(job, token, limit, source):
    need(re.fullmatch('[a-f0-9]{32}', token) and 0 < limit <= MAX_BYTES, 'bounded nonce/job required')
    job = job_path(job, True)
    need(shutil.disk_usage(job).free >= limit + (64 << 20), 'insufficient plaintext staging space')
    fd = os.open(job/'decrypted.UNVERIFIED.tar', os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    h = hashlib.sha256(); size = 0
    with os.fdopen(fd, 'wb') as out:
        for block in iter(lambda: source.read(1024*1024), b''):
            size += len(block); need(size <= limit, 'plaintext exceeds reviewed bound')
            h.update(block); out.write(block)
        out.flush(); os.fsync(out.fileno())
    need(size > 0, 'empty plaintext')
    receipt = {'version': 1, 'token': token, 'bytes': size, 'sha256': h.hexdigest(), 'gpg_verified': False}
    write_json(job/'receiver.json', receipt)
    return receipt

def publish(job, token, size, checksum, ciphertext_hash):
    job = job_path(job); receipt = read_json(job/'receiver.json')
    need(re.fullmatch('[a-f0-9]{32}', token) and re.fullmatch('[a-f0-9]{64}', checksum) and re.fullmatch('[a-f0-9]{64}', ciphertext_hash), 'exact transfer identity required')
    need(receipt == {'version': 1, 'token': token, 'bytes': size, 'sha256': checksum, 'gpg_verified': False}, 'receiver generation differs')
    source = private(job/'decrypted.UNVERIFIED.tar'); before = identity(source)
    need(before[2] == size and sha_file(source) == checksum and identity(source) == before, 'plaintext changed after receive')
    # Only the local orchestrator invokes this after every process exit and the
    # independently pinned ciphertext hash succeeds. Root is the trust boundary.
    os.link(source, job/'decrypted.tar', follow_symlinks=False); sync_dir(job)
    result = {'version': 1, 'kind': 'gpg-stream-transport', 'token': token, 'bytes': size, 'sha256': checksum, 'ciphertext_sha256': ciphertext_hash, 'gpg_exit_zero_verified_by_offhost_operator': True, 'application_restore_verified': False}
    write_json(job/'transport.json', result)
    return result

def pipeline(reader_args, gpg_args, receiver_args, expected_size, expected_hash, timeout, pass_fds=()):
    """Bounded pipes; successful plaintext output is not enough: all exits count."""
    processes = []
    errors = [tempfile.TemporaryFile() for _ in range(3)]
    timed_out = threading.Event()
    def terminate():
        timed_out.set()
        for p in processes:
            if p.poll() is None: p.kill()
    timer = threading.Timer(timeout, terminate); timer.daemon = True
    try:
        receiver = subprocess.Popen(receiver_args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=errors[0]); processes.append(receiver)
        gpg = subprocess.Popen(gpg_args, stdin=subprocess.PIPE, stdout=receiver.stdin, stderr=errors[1], pass_fds=pass_fds); processes.append(gpg)
        receiver.stdin.close()
        reader = subprocess.Popen(reader_args, stdout=subprocess.PIPE, stderr=errors[2]); processes.append(reader)
        timer.start(); h = hashlib.sha256(); size = 0
        try:
            for block in iter(lambda: reader.stdout.read(1024*1024), b''):
                size += len(block); need(size <= expected_size, 'ciphertext stream exceeds trusted size')
                h.update(block); gpg.stdin.write(block)
            gpg.stdin.close()
        finally: reader.stdout.close()
        reader_code = reader.wait(); gpg_code = gpg.wait()
        result = receiver.stdout.read(65537); receiver.stdout.close(); receiver_code = receiver.wait()
        need(not timed_out.is_set() and reader_code == gpg_code == receiver_code == 0, 'SSH/GPG transfer did not complete successfully')
        need(size == expected_size and h.hexdigest() == expected_hash, 'independent ciphertext stream hash mismatch')
        need(len(result) <= 65536, 'oversized receiver response')
        return json.loads(result)
    finally:
        timer.cancel()
        for p in processes:
            if p.poll() is None: p.kill()
            p.wait(timeout=10)
        for f in errors: f.close()

def ssh_args(socket, remote_args):
    # ProxyCommand=false prevents a missing/expired shared master from silently
    # falling back to a new direct network connection.
    return ['/usr/bin/ssh', '-S', socket, '-oControlMaster=no', '-oBatchMode=yes', '-oProxyCommand=false', '-oServerAliveInterval=15', '-oServerAliveCountMax=3', REMOTE, shlex.join(['/usr/bin/python3', REMOTE_SCRIPT, *remote_args])]

def local(config_path):
    need(sys.platform == 'darwin' and os.geteuid() != 0, 'off-host operator Mac required; private key must never run on backup host')
    c = read_json(config_path)
    need(set(c) == {'version','ssh_control_path','source_ciphertext','ciphertext_sha256','ciphertext_bytes','remote_job','max_plaintext_bytes','gpg_home','passphrase_file','evidence','timeout_seconds'} and c['version'] == 1, 'exact stream configuration required')
    need(re.fullmatch('[a-f0-9]{64}', c['ciphertext_sha256']) and type(c['ciphertext_bytes']) is int and 0 < c['ciphertext_bytes'] <= MAX_BYTES, 'trusted ciphertext identity required')
    need(type(c['max_plaintext_bytes']) is int and 0 < c['max_plaintext_bytes'] <= MAX_BYTES and type(c['timeout_seconds']) is int and 1 <= c['timeout_seconds'] <= 86400, 'bounded transfer required')
    need(stat.S_ISSOCK(Path(c['ssh_control_path']).stat().st_mode), 'existing SSH master required')
    keyring = private(c['gpg_home'], True); secret = private(c['passphrase_file']); evidence = Path(c['evidence'])
    private(evidence.parent, True); need(not evidence.exists(), 'new local evidence required')
    gpg = shutil.which('gpg'); need(gpg, 'standard GPG required')
    base = [gpg, '--no-options', '--homedir', str(keyring), '--batch']
    listing = subprocess.run(base+['--with-colons','--list-secret-keys',RECIPIENT], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
    fingerprints = [x.split(':')[9] for x in listing.stdout.decode().splitlines() if x.startswith('fpr:')]
    need(listing.returncode == 0 and fingerprints and fingerprints[0] == RECIPIENT, 'reviewed off-host recipient key required')
    token = os.urandom(16).hex(); socket = c['ssh_control_path']
    with secret.open('rb') as passphrase:
        receipt = pipeline(ssh_args(socket, ['send', c['source_ciphertext'], str(c['ciphertext_bytes']), c['ciphertext_sha256']]), base+['--pinentry-mode','loopback','--passphrase-fd',str(passphrase.fileno()),'--decrypt'], ssh_args(socket, ['receive',c['remote_job'],token,str(c['max_plaintext_bytes'])]), c['ciphertext_bytes'],c['ciphertext_sha256'],c['timeout_seconds'], (passphrase.fileno(),))
    need(receipt.get('token') == token and receipt.get('gpg_verified') is False and type(receipt.get('bytes')) is int and 0 < receipt['bytes'] <= c['max_plaintext_bytes'] and re.fullmatch('[a-f0-9]{64}',receipt.get('sha256','')), 'receiver receipt mismatch')
    command = ssh_args(socket, ['publish',c['remote_job'],token,str(receipt['bytes']),receipt['sha256'],c['ciphertext_sha256']])
    completed = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=c['timeout_seconds'])
    need(completed.returncode == 0 and len(completed.stdout) <= 65536, 'remote verified publication failed; retain untrusted staging')
    result = json.loads(completed.stdout)
    need(result.get('token') == token and result.get('sha256') == receipt['sha256'] and result.get('ciphertext_sha256') == c['ciphertext_sha256'] and result.get('gpg_exit_zero_verified_by_offhost_operator') is True, 'publication identity differs')
    result.update({'verified_at':time.time(),'recipient_fingerprint':RECIPIENT,'private_key_uploaded':False,'local_archive_bytes_stored':0})
    write_json(evidence, result)
    return {'transport_verified':True,'bytes':receipt['bytes'],'private_key_uploaded':False,'application_restore_verified':False}

def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__); sub = parser.add_subparsers(dest='action',required=True)
    p=sub.add_parser('local');p.add_argument('--config',required=True)
    p=sub.add_parser('send');p.add_argument('path');p.add_argument('size',type=int);p.add_argument('sha256')
    p=sub.add_parser('receive');p.add_argument('job');p.add_argument('token');p.add_argument('limit',type=int)
    p=sub.add_parser('publish');p.add_argument('job');p.add_argument('token');p.add_argument('size',type=int);p.add_argument('sha256');p.add_argument('ciphertext_sha256')
    a=parser.parse_args()
    if a.action=='local': result=local(a.config)
    else:
        need(sys.platform=='linux' and os.geteuid()==0,'native backup-host root required')
        if a.action=='send':send(a.path,a.size,a.sha256,sys.stdout.buffer);return
        result=receive(a.job,a.token,a.limit,sys.stdin.buffer) if a.action=='receive' else publish(a.job,a.token,a.size,a.sha256,a.ciphertext_sha256)
    print(json.dumps(result))

if __name__=='__main__':
    try:main()
    except (RuntimeError,OSError,ValueError,KeyError,TypeError,subprocess.SubprocessError):
        print('Encrypted restore transport refused; retain private partial evidence. No restore or boot was attempted.',file=sys.stderr);sys.exit(1)
