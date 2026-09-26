#!/usr/bin/env python3
"""Narrow, fenced Motion listener release. No fence creation/reopen or data restore."""
import argparse
import contextlib
import fcntl
import hashlib
import http.client
import itertools
import json
import os
from pathlib import Path
import re
import signal
import socket
import stat
import subprocess
import tarfile
import time

ROOT = Path('/opt/baarcha/motion-studio')
APP = ROOT / 'app'
UNIT = 'baarcha-motion-worker.service'
UNITFILE = Path('/etc/systemd/system') / UNIT
DROP = Path(str(UNITFILE) + '.d/20-cube-uds.conf')
SOCKET = Path('/run/baarcha-motion-studio/worker.sock')
LOCKS = ('/opt/baarcha/deploy-release.lock', '/opt/sandboxd/deploy-state/deploy.lock',
         '/run/lock/cube-operator-acceptance.lock', '/opt/baarcha-bench/cube-workload-operator.lock')
FILES = ('server/index.mjs', 'server/listen.mjs', 'server/multipart.mjs')
SHA = re.compile(r'[0-9a-f]{64}')
MAX_JSON = 8 * 1024 * 1024


def need(ok, message):
    if not ok:
        raise RuntimeError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def canonical(value):
    return sha(json.dumps(value, sort_keys=True, separators=(',', ':')).encode())


def regular(path, private=False, maximum=MAX_JSON):
    p = Path(path)
    need(p.is_absolute() and p.resolve(strict=True) == p, 'noncanonical file')
    s = p.stat()
    need(stat.S_ISREG(s.st_mode) and s.st_nlink == 1 and s.st_size <= maximum, 'unsafe file type/size/links')
    if private:
        need(s.st_uid == 0 and stat.S_IMODE(s.st_mode) == 0o600, 'private root0600 required')
    return p


def read_json(path, private=False):
    def pairs(rows):
        out = {}
        for k, v in rows:
            need(k not in out, 'duplicate JSON key')
            out[k] = v
        return out
    return json.loads(regular(path, private).read_bytes(), object_pairs_hook=pairs)


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def exclusive(path, body):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as f:
        f.write(body)
        f.flush()
        os.fsync(f.fileno())
    sync_dir(Path(path).parent)


def snapshot(path):
    p = Path(path)
    if not p.exists() and not p.is_symlink():
        return None
    regular(p)
    s = p.stat()
    return {'sha256': digest(p), 'uid': s.st_uid, 'gid': s.st_gid,
            'mode': stat.S_IMODE(s.st_mode), 'inode': s.st_ino, 'device': s.st_dev}


def atomic(path, body, uid, gid, mode):
    p = Path(path)
    need(p.parent.resolve(strict=True) == p.parent, 'unsafe install parent')
    temp = p.with_name('.' + p.name + '.motion-uds-' + str(os.getpid()))
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        with os.fdopen(fd, 'wb') as f:
            os.fchown(f.fileno(), uid, gid)
            os.fchmod(f.fileno(), mode)
            f.write(body)
            f.flush()
            os.fsync(f.fileno())
        os.replace(temp, p)
        sync_dir(p.parent)
    finally:
        if temp.exists():
            temp.unlink()


def candidate(tar_path, manifest):
    regular(tar_path, maximum=1024 * 1024)
    need(digest(tar_path) == manifest['source_tar_sha256'], 'candidate tar drift')
    out = {}
    with tarfile.open(tar_path, 'r:') as tar:
        for m in tar:
            need(m.name in FILES and m.name not in out and m.isfile() and not m.pax_headers,
                 'candidate member invalid')
            need(m.size <= 64 * 1024 and m.mode == 0o644, 'candidate bounds/mode')
            out[m.name] = tar.extractfile(m).read(64 * 1024 + 1)
    need(set(out) == set(FILES), 'candidate file set')
    for row in manifest['files']:
        need(sha(out[row['path']]) == row['candidate_sha256'] and len(out[row['path']]) == row['bytes'], 'candidate content drift')
    return out


def validate_config(c, selected):
    keys = {'version', 'boot_id', 'controller_id', 'controller_image', 'platform_revision',
            'source_before', 'unit_sha256', 'env_sha256', 'dropins', 'service_uid', 'service_gid',
            'service_properties', 'project_count', 'projects_sha256', 'fence'}
    need(set(c) == keys and c['version'] == 1, 'configuration shape')
    need(set(c['source_before']) == set(selected), 'full selected source baseline required')
    for rel, state in c['source_before'].items():
        need(isinstance(state, dict) and set(state) == {'sha256', 'uid', 'gid', 'mode', 'inode', 'device'} and
             SHA.fullmatch(state['sha256']) and state['mode'] == 0o644 and
             state['uid'] == c['service_uid'] and state['gid'] == c['service_gid'] and state['inode'] > 0,
             'source ownership/hash pin required')
    need(all(re.fullmatch(r'[a-zA-Z0-9_.-]+\.conf', k) and SHA.fullmatch(v) for k, v in c['dropins'].items()), 'drop-in pins')
    for value in (c['unit_sha256'], c['env_sha256'], c['projects_sha256']):
        need(isinstance(value, str) and SHA.fullmatch(value), 'invalid content pin')
    need(SHA.fullmatch(c['controller_id']) and re.fullmatch(r'sha256:[0-9a-f]{64}', c['controller_image']), 'controller pin')
    need(re.fullmatch(r'[0-9a-f]{40}', c['platform_revision']), 'platform pin')
    need(c['project_count'] >= 7 and c['service_uid'] > 0 and c['service_gid'] > 0, 'reviewed identities/count required')
    need(c['service_properties'].get('MemoryMax') == '6442450944' and
         c['service_properties'].get('CPUQuotaPerSecUSec') == '2.500000s' and
         c['service_properties'].get('KillMode') == 'control-group', 'resource/stop limits required')
    f = c['fence']
    need(set(f) == {'caddy_sha256', 'writer_containers', 'stopped_units', 'expires_boottime'}, 'fence shape')
    need(SHA.fullmatch(f['caddy_sha256']) and 1 <= len(f['writer_containers']) <= 4 and
         all(SHA.fullmatch(x) for x in f['writer_containers']), 'explicit complete writer identities required')
    need(set(f['stopped_units']) == {'baarcha-motion-access.timer', 'baarcha-motion-access.service'} and all(re.fullmatch(r'baarcha-motion-[a-z0-9-]+\.(timer|service)', x) and x != UNIT for x in f['stopped_units']), 'explicit refresh writer units required')
    return c


@contextlib.contextmanager
def locks(inherited=None):
    held = []
    try:
        need(inherited is None or len(inherited) == 4, 'four inherited locks required')
        for i, name in enumerate(LOCKS):
            p = Path(name)
            need(p.resolve(strict=True) == p, 'lock path')
            fd = os.dup(inherited[i]) if inherited is not None else os.open(p, os.O_RDWR | os.O_NOFOLLOW)
            held.append(fd)
            s = os.fstat(fd)
            need(stat.S_ISREG(s.st_mode) and s.st_uid == 0 and s.st_nlink == 1 and
                 (s.st_dev, s.st_ino) == (p.stat().st_dev, p.stat().st_ino), 'lock identity')
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield
    finally:
        for fd in reversed(held):
            os.close(fd)  # Do not unlock the caller's shared open-file description.


class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(str(SOCKET))


class Release:
    def __init__(self, config, stage, sources, drop):
        self.c, self.stage, self.sources, self.drop = config, Path(stage), sources, drop
        self.key = None
        self.before = None
        self.mutated = False
        self.installed = []
        self.drop_dir_created = False

    def event(self, name, **detail):
        row = {'event': name, 'utc_ns': time.time_ns(), 'boottime': time.clock_gettime(time.CLOCK_BOOTTIME), **detail}
        p = self.stage / 'journal.jsonl'
        fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'ab') as f:
            f.write(json.dumps(row, sort_keys=True).encode() + b'\n')
            f.flush()
            os.fsync(f.fileno())
        sync_dir(self.stage)

    def cmd(self, args, timeout=30):
        result = subprocess.run(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=timeout, check=False)
        need(result.returncode == 0, 'reviewed command failed: ' + args[0])
        need(len(result.stdout) <= MAX_JSON, 'command output bound')
        return result.stdout

    def service(self):
        raw = self.cmd(['systemctl', 'show', UNIT, '--property=ActiveState,SubState,MainPID,ControlPID,ControlGroup,MemoryMax,CPUQuotaPerSecUSec,KillMode,User,Group,FragmentPath,DropInPaths'])
        return dict(line.split('=', 1) for line in raw.decode().splitlines() if '=' in line)

    def http(self, path, transport='tcp', auth='valid'):
        conn = UnixHTTP('localhost', timeout=5) if transport == 'uds' else http.client.HTTPConnection('172.19.0.1', 8332, timeout=5)
        headers = {'Connection': 'close'}
        if auth != 'missing':
            headers['Authorization'] = 'Bearer ' + (self.key if auth == 'valid' else 'deliberately-invalid-motion-acceptance')
        try:
            conn.request('GET', path, headers=headers)
            response = conn.getresponse()
            body = response.read(MAX_JSON + 1)
            need(len(body) <= MAX_JSON, 'HTTP body limit')
            return response.status, body
        finally:
            conn.close()

    def fence(self):
        c, f = self.c, self.c['fence']
        need(Path('/proc/sys/kernel/random/boot_id').read_text().strip() == c['boot_id'], 'host boot drift')
        now = time.clock_gettime(time.CLOCK_BOOTTIME)
        need(now < f['expires_boottime'] <= now + 3600, 'fence expired/unbounded')
        h = http.client.HTTPConnection('127.0.0.1', 2019, timeout=5)
        try:
            h.request('GET', '/config/')
            r = h.getresponse(); raw = r.read(MAX_JSON + 1)
            need(r.status == 200 and len(raw) <= MAX_JSON and canonical(json.loads(raw)) == f['caddy_sha256'], 'live Caddy fence drift')
        finally:
            h.close()
        controller = json.loads(self.cmd(['docker', 'inspect', c['controller_id']]))
        need(len(controller) == 1 and controller[0]['Id'] == c['controller_id'] and
             controller[0]['Image'] == c['controller_image'] and not controller[0]['State']['Running'] and
             not controller[0]['State']['Restarting'], 'controller admission not stopped')
        for identity in f['writer_containers']:
            rows = json.loads(self.cmd(['docker', 'inspect', identity]))
            need(len(rows) == 1 and rows[0]['Id'] == identity and not rows[0]['State']['Running'] and not rows[0]['State']['Restarting'], 'Motion writer not stopped')
        for unit in f['stopped_units']:
            state = self.cmd(['systemctl', 'show', unit, '--property=ActiveState,MainPID,ControlPID'])
            props = dict(line.split('=', 1) for line in state.decode().splitlines())
            need(props['ActiveState'] == 'inactive' and props.get('MainPID', '0') == '0' and props.get('ControlPID', '0') == '0', 'Motion refresh writer active')
        need(not self.cmd(['ss', '-Htn', 'state', 'established', '( sport = :8332 or dport = :8332 )']).strip(), 'worker TCP requests not drained')

    def projects(self, transport='tcp'):
        status, raw = self.http('/api/projects', transport)
        need(status == 200, 'authenticated projects failed')
        body = json.loads(raw)
        need(set(body) == {'projects'} and isinstance(body['projects'], list), 'projects schema')
        projects = body['projects']
        need(len(projects) == self.c['project_count'] and canonical(body) == self.c['projects_sha256'], 'project state changed')
        need(all(j.get('status') not in ('queued', 'running') for p in projects for j in p.get('jobs', [])), 'jobs not drained')
        return body

    def preflight(self):
        self.fence()
        c = self.c
        row = json.loads(self.cmd(['docker', 'inspect', c['controller_id']]))[0]
        need(row['Id'] == c['controller_id'] and row['Image'] == c['controller_image'] and not row['State']['Running'] and not row['State']['Restarting'], 'controller must be fenced/stopped')
        need(self.cmd(['git', '-C', '/opt/baarcha/app', 'rev-parse', 'HEAD']).decode().strip() == c['platform_revision'], 'platform drift')
        for rel, state in c['source_before'].items():
            need(snapshot(APP / rel) == state, 'source identity drift')
        need(digest(regular(UNITFILE)) == c['unit_sha256'], 'unit drift')
        need(digest(regular(ROOT / 'worker.env', private=True)) == c['env_sha256'], 'worker env drift')
        existing = {p.name: digest(regular(p)) for p in DROP.parent.glob('*')} if DROP.parent.exists() else {}
        need(existing == c['dropins'] and DROP.name not in existing, 'drop-in drift')
        need(all(not (APP / p).exists() and not (APP / p).is_symlink() for p in FILES[1:]), 'helper already present')
        props = self.service()
        need(props['ActiveState'] == 'active' and props['SubState'] == 'running' and int(props['MainPID']) > 1, 'worker not active')
        need(all(props.get(k) == v for k, v in c['service_properties'].items()), 'service properties drift')
        need(props.get('FragmentPath') == str(UNITFILE) and
             set(props.get('DropInPaths', '').split()) == {str(DROP.parent / x) for x in c['dropins']}, 'loaded service/drop-in drift')
        env = dict(x.split(b'=', 1) for x in Path('/proc/' + props['MainPID'] + '/environ').read_bytes().split(b'\0') if b'=' in x)
        need(env.get(b'STUDIO_BIND') == b'172.19.0.1' and env.get(b'PORT', env.get(b'STUDIO_PORT')) == b'8332', 'TCP environment drift')
        need(not env.get(b'STUDIO_WORKER_URL') and not env.get(b'STUDIO_WORKER_SOCKET'), 'unexpected worker transport')
        self.key = env.get(b'STUDIO_WORKER_KEY', b'').decode()
        need(bool(self.key) and '\r' not in self.key and '\n' not in self.key, 'missing/invalid worker credential')
        self.before = self.projects()
        self.fence()
        self.event('preflight_passed', projects=c['project_count'])

    def stop(self):
        self.cmd(['systemctl', 'stop', UNIT], timeout=120)
        p = self.service()
        need(p['ActiveState'] == 'inactive' and p['SubState'] == 'dead' and p['MainPID'] == '0' and p['ControlPID'] == '0', 'worker not fully stopped')
        group = p.get('ControlGroup')
        if group:
            need(group.startswith('/system.slice/') and '..' not in group, 'unexpected worker cgroup')
            root = Path('/sys/fs/cgroup') / group.lstrip('/')
            need(all(not f.read_text().strip() for f in root.rglob('cgroup.procs')), 'worker descendants remain')
        # Dedicated service UID must have no surviving process outside its cgroup either.
        for proc in Path('/proc').iterdir():
            if not proc.name.isdecimal(): continue
            try:
                status = (proc / 'status').read_text()
            except FileNotFoundError:
                continue
            uid = re.search(r'^Uid:\s+(\d+)\s+(\d+)', status, re.M)
            need(not uid or all(int(x) != self.c['service_uid'] for x in uid.groups()), 'service UID still has a live process')
        self.event('worker_stopped')

    def backup(self):
        # No extraction, dereference or data restoration. GNU tar preserves links/xattrs/ACLs.
        paths = [ROOT / 'data', ROOT / 'home', ROOT / 'worker.env', UNITFILE]
        if DROP.parent.exists():
            paths.append(DROP.parent)
        entries, total = 0, 0
        hardlinks = {}
        inventory = self.stage / 'closed-inventory.private.jsonl'
        inventory_fd = os.open(inventory, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(inventory_fd, 'wb') as listing:
            for base in paths:
                need(base.resolve(strict=True) == base, 'noncanonical backup root')
                for p in itertools.chain((base,), base.rglob('*') if base.is_dir() else ()):
                    st = p.lstat(); entries += 1
                    need(stat.S_ISREG(st.st_mode) or stat.S_ISDIR(st.st_mode) or stat.S_ISLNK(st.st_mode), 'special backup entry requires review')
                    total += st.st_size if stat.S_ISREG(st.st_mode) else 0
                    if stat.S_ISREG(st.st_mode) and st.st_nlink > 1:
                        key = (st.st_dev, st.st_ino)
                        prior = hardlinks.setdefault(key, [st.st_nlink, 0])
                        need(prior[0] == st.st_nlink, 'backup hardlink changed')
                        prior[1] += 1
                    row = {'path': str(p), 'mode': st.st_mode, 'uid': st.st_uid, 'gid': st.st_gid, 'bytes': st.st_size, 'inode': st.st_ino, 'device': st.st_dev, 'nlink': st.st_nlink}
                    if stat.S_ISLNK(st.st_mode): row['link'] = os.readlink(p)
                    listing.write(json.dumps(row, sort_keys=True).encode() + b'\n')
                    need(entries <= 500000 and total <= 20 * 1024 ** 3, 'backup bounds exceeded')
            listing.flush(); os.fsync(listing.fileno())
        sync_dir(self.stage)
        need(all(expected == seen for expected, seen in hardlinks.values()), 'hardlink crosses backup boundary')
        need(os.statvfs(self.stage).f_bavail * os.statvfs(self.stage).f_frsize > total * 2 + 1024 ** 3, 'backup disk headroom')
        archive = self.stage / 'closed-data-home-config.tar'
        need(not archive.exists(), 'backup already exists')
        self.cmd(['tar', '--format=pax', '--numeric-owner', '--acls', '--xattrs', '--xattrs-include=*',
                  '-cpf', str(archive), '-C', '/', *[str(p).lstrip('/') for p in paths]], timeout=600)
        os.chmod(archive, 0o600)
        with archive.open('rb') as f: os.fsync(f.fileno())
        sync_dir(self.stage)
        self.event('closed_backup_complete', archive_sha256=digest(archive), inventory_sha256=digest(inventory), entries=entries, logical_bytes=total)
        originals = {}
        for rel in FILES:
            state = snapshot(APP / rel)
            originals[rel] = state
            if state:
                exclusive(self.stage / ('original-' + Path(rel).name), (APP / rel).read_bytes())
        exclusive(self.stage / 'source-originals.json', json.dumps(originals, sort_keys=True).encode())

    def install(self):
        self.fence()
        for rel in FILES:
            expected = self.c['source_before'].get(rel)
            need(snapshot(APP / rel) == expected, 'pre-install source drift')
            self.event('install_intent', path=rel)
            self.installed.append(rel)  # Restore even if rename succeeded but fsync raised.
            atomic(APP / rel, self.sources[rel], self.c['service_uid'], self.c['service_gid'], 0o644)
        if not DROP.parent.exists():
            DROP.parent.mkdir(mode=0o755); self.drop_dir_created = True; sync_dir(DROP.parent.parent)
        need(not DROP.exists() and not DROP.is_symlink(), 'drop-in appeared')
        self.event('dropin_intent')
        self.installed.append('dropin')
        atomic(DROP, self.drop, 0, 0, 0o644)
        self.cmd(['systemctl', 'daemon-reload'])

    def start_verify(self, uds):
        self.cmd(['systemctl', 'start', UNIT], timeout=60)
        deadline = time.monotonic() + 30
        while True:
            try:
                need(self.http('/api/health')[0] == 200, 'TCP health')
                break
            except (OSError, http.client.HTTPException, RuntimeError):
                if time.monotonic() >= deadline: raise RuntimeError('worker startup deadline')
                time.sleep(0.25)
        p = self.service()
        need(p['ActiveState'] == 'active' and int(p['MainPID']) > 1 and
             all(p.get(k) == v for k, v in self.c['service_properties'].items()), 'post-start service properties')
        expected_dropins = {str(DROP.parent / x) for x in self.c['dropins']}
        if uds: expected_dropins.add(str(DROP))
        need(p.get('FragmentPath') == str(UNITFILE) and set(p.get('DropInPaths', '').split()) == expected_dropins, 'post-start loaded unit drift')
        uid = Path('/proc/' + p['MainPID'] + '/status').read_text()
        m = re.search(r'^Uid:\s+(\d+)\s+(\d+)', uid, re.M)
        need(m and all(int(x) == self.c['service_uid'] for x in m.groups()), 'worker process UID')
        env = dict(x.split(b'=', 1) for x in Path('/proc/' + p['MainPID'] + '/environ').read_bytes().split(b'\0') if b'=' in x)
        need(env.get(b'STUDIO_WORKER_KEY', b'').decode() == self.key and
             env.get(b'STUDIO_BIND') == b'172.19.0.1' and env.get(b'PORT', env.get(b'STUDIO_PORT')) == b'8332' and
             not env.get(b'STUDIO_WORKER_URL'), 'post-start credential/TCP drift')
        need(env.get(b'STUDIO_WORKER_SOCKET', b'') == (str(SOCKET).encode() if uds else b''), 'post-start socket config')
        for rel, before in self.c['source_before'].items():
            expected = sha(self.sources[rel]) if uds and rel in FILES else before['sha256']
            need(digest(regular(APP / rel)) == expected, 'post-start selected source drift')
        need(digest(regular(UNITFILE)) == self.c['unit_sha256'] and
             digest(regular(ROOT / 'worker.env', private=True)) == self.c['env_sha256'], 'post-start base configuration drift')
        if uds:
            d, s = SOCKET.parent.lstat(), SOCKET.lstat()
            need(SOCKET.parent.resolve() == SOCKET.parent and stat.S_ISDIR(d.st_mode) and d.st_uid == self.c['service_uid'] and stat.S_IMODE(d.st_mode) == 0o750, 'socket directory DAC')
            need(stat.S_ISSOCK(s.st_mode) and s.st_uid == self.c['service_uid'] and s.st_gid == self.c['service_gid'] and stat.S_IMODE(s.st_mode) == 0o660, 'socket DAC')
        for transport in (('tcp', 'uds') if uds else ('tcp',)):
            need(self.http('/api/health', transport)[0] == 200, 'transport health failed')
            self.projects(transport)
            for auth in ('missing', 'wrong'):
                need(self.http('/api/projects', transport, auth)[0] == 401, 'authorization regression')
        self.fence()
        self.event('validation_passed', uds=uds, project_state_sha256=self.c['projects_sha256'])

    def rollback(self):
        # First stop any ambiguous/partially started candidate. Never overwrite data.
        self.stop()
        self.fence()
        for rel in reversed(self.installed):
            p = DROP if rel == 'dropin' else APP / rel
            expected_new = sha(self.drop) if rel == 'dropin' else sha(self.sources[rel])
            state = snapshot(p)
            old = self.c['source_before'].get(rel)
            need(state is None or state['sha256'] == expected_new or state == old, 'rollback refuses unreviewed file drift')
            if old:
                raw = regular(self.stage / ('original-' + Path(rel).name), private=True).read_bytes()
                need(sha(raw) == old['sha256'], 'backup source hash changed')
                atomic(p, raw, old['uid'], old['gid'], old['mode'])
            elif state is not None:
                p.unlink(); sync_dir(p.parent)
        if self.drop_dir_created:
            DROP.parent.rmdir(); sync_dir(DROP.parent.parent)
        self.cmd(['systemctl', 'daemon-reload'])
        self.start_verify(False)
        self.event('source_only_rollback_passed', data_restored=False, fence_reopened=False)

    def execute(self):
        self.preflight()
        try:
            self.event('stop_intent')
            self.mutated = True
            self.stop()
            self.fence()
            self.backup()
            self.install()
            self.start_verify(True)
            self.event('complete', fence_reopened=False, data_restored=False)
        except BaseException:
            self.event('failed', mutation_started=self.mutated)
            if self.mutated:
                try:
                    self.rollback()
                except BaseException:
                    self.event('rollback_incomplete', manual_review_required=True, fence_reopened=False)
            raise


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--config', required=True); p.add_argument('--stage', required=True)
    p.add_argument('--tar', required=True); p.add_argument('--execute', action='store_true')
    p.add_argument('--source-sha256', required=True); p.add_argument('--config-sha256', required=True)
    p.add_argument('--lock-fds', help='four inherited caller-held descriptors, comma separated')
    a = p.parse_args()
    def interrupted(signum, frame): raise InterruptedError('release interrupted')
    for sig in (signal.SIGTERM, signal.SIGINT): signal.signal(sig, interrupted)
    need(os.geteuid() == 0 and digest(__file__) == a.source_sha256, 'root/exact source required')
    need(digest(regular(a.config, private=True)) == a.config_sha256, 'config hash drift')
    here = Path(__file__).resolve().parent
    need(digest(here / 'manifest.json') == 'fa6f831a4d5edc3a5ae48ad3b5a9df39030935bf3881dddcd1ff3b0a1be6a1e7', 'manifest drift')
    need(digest(here / 'source-comparison.json') == 'eecf6dd5d90d752f9ba0d28682c29bae7533ea3bf2284058e8e4b43f853a560f', 'comparison drift')
    manifest = read_json(here / 'manifest.json')
    selected = read_json(here / 'source-comparison.json')['843e9f9']['matching']
    c = validate_config(read_json(a.config, True), selected)
    need(c['source_before']['server/index.mjs']['sha256'] == manifest['files'][0]['old_sha256'], 'reviewed original entrypoint drift')
    src = candidate(a.tar, manifest)
    drop = regular(here / '20-cube-uds.conf').read_bytes()
    need(sha(drop) == 'cfb3e196638145854f4b8f0d0905faf90f711bec38579a1e3b4853ae829d44a4', 'drop-in source drift')
    stage = Path(a.stage)
    need(stage.is_absolute() and stage.parent.resolve(strict=True) == stage.parent, 'stage parent')
    stage.mkdir(mode=0o700)  # Never reuse an uncertain run; no automatic replay.
    exclusive(stage / 'inputs.json', json.dumps({'config_sha256': a.config_sha256, 'source_sha256': a.source_sha256,
                                              'candidate_sha256': manifest['source_tar_sha256']}).encode())
    fds = [int(x) for x in a.lock_fds.split(',')] if a.lock_fds else None
    with locks(fds):
        r = Release(c, stage, src, drop)
        if a.execute: r.execute()
        else: r.preflight()
    print(json.dumps({'passed': True, 'executed': a.execute, 'fence_reopened': False}))


if __name__ == '__main__':
    main()
