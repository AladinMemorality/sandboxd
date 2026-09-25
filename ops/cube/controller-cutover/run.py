#!/usr/bin/env python3
"""Reviewed single-app controller enrollment; default is read-only preflight.

No worker power operation, guest creation/deletion, database rewind or build.
Every mutation requires --execute plus exact script/config hashes and four locks.
"""
import argparse
import contextlib
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import sqlite3
import stat
import subprocess
import sys
import time

SOURCE = Path('/opt/sandboxd/src')
STATE = Path('/opt/sandboxd/deploy-state')
STOP = Path('/etc/baarcha-cube/worker-stop.json')
KEY = Path('/var/lib/sandboxd/secrets.key')
ROUTING = Path('/opt/baarcha-cube/worker-01/cutover-routing')
OBSERVER_SHA = '06dd5e379fa594ec6fbfa37d36971fa5703ff5969b8766bac98196ee10c0f92e'
SERVICES = ('sandboxd', 'cube-management-api', 'cube-management-proxy')
HOST_RELAYS = ('cube-management-api.service', 'cube-management-proxy.service')
SHA = re.compile(r'[0-9a-f]{64}')


def need(value, message):
    if not value:
        raise RuntimeError(message)


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for part in iter(lambda: f.read(1024 * 1024), b''):
            h.update(part)
    return h.hexdigest()


def canonical_hash(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def private(path):
    p = Path(path)
    need(p.is_absolute() and p.resolve(strict=True) == p, 'noncanonical private input')
    s = p.stat()
    need(stat.S_ISREG(s.st_mode) and s.st_uid == 0 and s.st_nlink == 1 and stat.S_IMODE(s.st_mode) == 0o600, 'unsafe private input')
    need(s.st_size <= 4 * 1024 * 1024, 'oversized private metadata')
    return p


def read_json(path):
    def pairs(items):
        result = {}
        for k, v in items:
            need(k not in result, 'duplicate JSON key')
            result[k] = v
        return result
    return json.loads(private(path).read_text(), object_pairs_hook=pairs)


def routing_file(path):
    p = Path(path)
    need(p.parent == ROUTING and p.name in ('drain.json', 'offline.json'), 'unexpected maintenance routing path')
    need(p.resolve(strict=True) == p, 'noncanonical routing input')
    s = p.stat()
    need(stat.S_ISREG(s.st_mode) and s.st_uid == 0 and s.st_nlink == 1 and stat.S_IMODE(s.st_mode) in (0o600, 0o644), 'unsafe routing input')
    need(s.st_size <= 4 * 1024 * 1024, 'oversized routing input')
    return p


def install_bytes(path, raw, *, routing=False):
    p = Path(path)
    need(p.parent.resolve(strict=True) == p.parent and p.parent.stat().st_uid == 0 and p.parent.stat().st_mode & 0o022 == 0, 'unsafe installation directory')
    mode = 0o600
    if routing:
        mode = stat.S_IMODE(routing_file(p).stat().st_mode)
    elif p.exists() or p.is_symlink():
        private(p)
    temp = p.with_name('.' + p.name + '.cutover-' + str(os.getpid()))
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
    with os.fdopen(fd, 'wb') as f:
        os.fchmod(f.fileno(), mode)
        f.write(raw)
        f.flush()
        os.fsync(f.fileno())
    os.replace(temp, p)
    fd = os.open(p.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def load(path, expected, name):
    need(SHA.fullmatch(expected) and digest(path) == expected, 'reviewed helper hash changed')
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def validate_config(c):
    required = {'version', 'controller_id', 'controller_image', 'platform_revision', 'runtime_revision',
                'worker_boot_id', 'expected_counts', 'candidate_directory', 'candidate_hashes',
                'baseline_hashes', 'enrollment_helper', 'enrollment_helper_sha256',
                'worker_stop_sha256', 'observer_config_sha256', 'allowed_app_id', 'relay_image', 'host_relay_unit_hashes',
                'caddy_live_sha256', 'caddy_file_sha256', 'routing_directory', 'routing_hashes', 'routing_original_hashes'}
    need(set(c) == required and c['version'] == 1, 'unknown/incomplete cutover configuration')
    need(SHA.fullmatch(c['controller_id']) and re.fullmatch(r'sha256:[0-9a-f]{64}', c['controller_image']), 'exact controller identity required')
    need(re.fullmatch(r'sha256:[0-9a-f]{64}', c['relay_image']), 'immutable relay image required')
    for k in ('platform_revision', 'runtime_revision'):
        need(re.fullmatch('[0-9a-f]{40}', c[k]), 'exact source revision required')
    need(re.fullmatch('[0-9A-HJKMNP-TV-Z]{26}', c['allowed_app_id']), 'exact synthetic app identity required')
    need(set(c['expected_counts']) == {'apps', 'bindings', 'admission', 'recovery', 'active'}, 'incomplete database counts')
    need(c['expected_counts']['apps'] > 0 and all(c['expected_counts'][k] == 0 for k in ('bindings', 'admission', 'recovery', 'active')), 'preallocation only')
    need(set(c['candidate_hashes']) == {'runtime-compose.json', 'active-images.json', 'merged-compose.private.json', 'review-manifest.json'}, 'candidate artifact set differs')
    need(set(c['baseline_hashes']) == {'docker-compose.yml', '.env', 'active-images.json', 'runtime-compose.json'}, 'baseline artifact set differs')
    for k, v in c['baseline_hashes'].items():
        need((v is None and k == 'runtime-compose.json') or (isinstance(v, str) and SHA.fullmatch(v)), 'invalid baseline hash')
    need(all(SHA.fullmatch(v) for v in c['candidate_hashes'].values()), 'invalid candidate hash')
    need(set(c['host_relay_unit_hashes']) == set(HOST_RELAYS) and all(SHA.fullmatch(v) for v in c['host_relay_unit_hashes'].values()), 'reviewed host relay unit hashes required')
    for k in ('routing_hashes', 'routing_original_hashes'):
        need(set(c[k]) == {'drain.json', 'offline.json'} and all(SHA.fullmatch(v) for v in c[k].values()), 'reviewed routing hashes required')
    need(SHA.fullmatch(c['caddy_live_sha256']) and SHA.fullmatch(c['caddy_file_sha256']), 'explicit live/file Caddy pins required')
    return c


def valid_preview_secrets(raw):
 if not isinstance(raw,str) or not raw or len(raw.encode())>8192:return False
 parts=raw.split(',');seen=set()
 if len(parts)>8:return False
 for part in parts:
  kid,sep,secret=part.strip().partition('=');kid=kid.strip();secret=secret.strip()
  if not sep or not re.fullmatch(r'[A-Za-z0-9_-]{1,64}',kid) or kid in seen or not 32<=len(secret.encode())<=1024 or any(ord(c)<33 or ord(c)>126 for c in secret):return False
  seen.add(kid)
 return True

def check_candidate(merged, c):
    services = merged['services']
    cp = services['sandboxd']
    env = cp['environment']
    need(valid_preview_secrets(env.get('SANDBOXD_PREVIEW_TOKEN_SECRETS')), 'valid Cube preview signing configuration required')
    need(env.get('SANDBOXD_CUBE_ENABLED') == 'true' and env.get('SANDBOXD_CUBE_REVERSE_EGRESS') == 'true', 'candidate reverse broker disabled')
    need(env.get('SANDBOXD_CUBE_ROLLOUT') == 'allowlist', 'global Cube routing forbidden')
    need(env.get('SANDBOXD_CUBE_APP_IDS') == c['allowed_app_id'], 'candidate must select exactly one synthetic app')
    need(env.get('SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS', '') == '', 'direct NIC domain allowances forbidden')
    need(env.get('SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED') == 'true', 'scoped reviewer decision absent')
    admission = json.loads(env['SANDBOXD_CUBE_ADMISSION'])
    need(admission['max_active'] == 4 and admission['cpu_count'] == 2 and admission['memory_mb'] == 2048 and admission['writable_disk_mb'] == 10240, 'candidate capacity contract differs')
    guard = admission['storage_guard']
    need(guard['expected_boot_id'] == c['worker_boot_id'], 'guard worker boot differs')
    need(guard['observation_path'] == '/run/sandboxd-cube-storage/observation.json', 'guard observation path differs')
    mounts = [v for v in cp.get('volumes', []) if v.get('target') == '/run/sandboxd-cube-storage']
    need(len(mounts) == 1 and mounts[0].get('source') == '/run/sandboxd-cube-storage' and mounts[0].get('read_only') is True, 'read-only observation directory mount missing')
    need(not any(v.get('target') == '/run/cube-management' for v in cp.get('volumes', [])), 'controller must not mount relay sockets')
    for name in SERVICES[1:]:
        relay = services[name]
        need(relay['image'] == c['relay_image'] and relay['network_mode'] == 'service:sandboxd', 'relay image/namespace differs')
        need(not relay.get('ports') and relay.get('read_only') is True and relay.get('pull_policy') == 'never', 'unsafe relay container')
        need(relay.get('user') == '65532:65532' and relay.get('userns_mode') == 'host' and [str(x) for x in relay.get('group_add', [])] == ['982'], 'relay identity differs')
        need(relay.get('cap_drop') == ['ALL'] and 'no-new-privileges:true' in relay.get('security_opt', []), 'relay privilege contract differs')
        mounts = relay.get('volumes', [])
        need(len(mounts) == 1, 'relay socket mount differs')
        mount = mounts[0]
        # Compose's normalized JSON omits false create_host_path inside bind.
        need(mount.get('type') == 'bind' and mount.get('source') == '/run/cube-management' and mount.get('target') == '/run/cube-management' and mount.get('read_only') is True and mount.get('bind', {}).get('create_host_path', False) is False, 'relay socket mount differs')
    return admission


def unrelated(inventory, project):
    excluded = {'/' + project + '-' + name + '-1' for name in SERVICES}
    return sorted((v for v in inventory if v['name'] not in excluded), key=lambda v: v['id'])


def validate_drain_transition(before, after, old_rows, new_rows):
    """Only finish an already accepted Docker app recreation during the drain."""
    a, b = ({v['name']: v for v in rows} for rows in (before, after))
    need(len(a) == len(before) and len(b) == len(after) and set(a) == set(b), 'container added/removed during drain')
    old = {r['id']: r for r in old_rows}
    new = {r['id']: r for r in new_rows}
    need(set(old) == set(new) and all(old[k]['app_id'] == new[k]['app_id'] for k in old), 'sandbox/app identity changed during drain')
    changes = []
    for name in a:
        if a[name] == b[name]:
            continue
        sid = name.removeprefix('/s-') if name.startswith('/s-') else None
        need(sid in old and a[name]['image'] == b[name]['image'], 'unrelated container/image changed during drain')
        need(bool(old[sid]['container_id']) and bool(new[sid]['container_id']) and
             a[name]['id'].startswith(old[sid]['container_id']) and b[name]['id'].startswith(new[sid]['container_id']), 'drained container lacks canonical old/new binding')
        changes.append({'before': a[name], 'after': b[name], 'sandbox_id': sid})
    return changes


class Cutover:
    def __init__(self, config, job):
        self.c = validate_config(config)
        self.job = job
        self.h = load(config['enrollment_helper'], config['enrollment_helper_sha256'], 'reviewed_cutover_helpers')
        # Only closed_backup uses these module pins; override historical values
        # with the independently reviewed current-generation inputs.
        self.h.CP = config['controller_id']
        self.h.IMAGE = config['controller_image']
        need(self.h.OBSERVER_SHA == OBSERVER_SHA, 'old drain observer helper forbidden')
        self.observer = load(Path(__file__).with_name('drain_observe.py'), OBSERVER_SHA, 'reviewed_drain_observer')
        self.candidate = Path(config['candidate_directory'])
        need(self.candidate.is_absolute() and self.candidate.resolve(strict=True) == self.candidate, 'candidate directory not canonical')
        need(self.candidate.stat().st_uid == 0 and stat.S_IMODE(self.candidate.stat().st_mode) == 0o700, 'candidate directory permissions differ')
        self.changed = False
        self.recreated = False
        self.installed = False
        self.routing_installed = False
        self.traffic_restore_attempted = False
        self.nested = None
        self.project = 'src'
        self.current = config['controller_id']
        self.phase = 'created'

    def event(self, name, value):
        self.h.atomic(self.job / (name + '.json'), value)

    def advance(self, phase):
        self.phase = phase
        self.event(phase, {'at': self.h.utc(), 'phase': phase})
        print(json.dumps({'phase': phase}), flush=True)

    def inspect(self, ident):
        return json.loads(self.h.run(['docker', 'inspect', ident]))[0]

    def sandbox_rows(self):
        with contextlib.closing(sqlite3.connect('file:' + str(self.h.DB) + '?mode=ro', uri=True, timeout=2)) as db:
            db.execute('PRAGMA query_only=ON')
            return [{'id': r[0], 'app_id': r[1], 'container_id': r[2]} for r in db.execute('SELECT id,app_id,container_id FROM sandbox ORDER BY id')]

    def compose(self, *args, candidate=False):
        command = ['docker', 'compose', '-p', self.project, '--env-file', str(SOURCE / '.env'), '-f', str(SOURCE / 'docker-compose.yml')]
        directory = self.candidate if candidate else STATE
        for name in ('runtime-compose.json', 'active-images.json'):
            if (directory / name).exists():
                command += ['-f', str(directory / name)]
        return self.h.run(command + list(args), 90)

    def empty(self):
        if self.nested is not None:
            self.nested.heartbeat()
        need(self.h.db_counts() == self.c['expected_counts'], 'database allocation/task inventory drift')
        need(json.loads(self.h.http('http://127.0.0.1:20300/sandboxes', self.stop['api_key'])) == [], 'provider guests present')
        inventory = self.h.worker('cubemastercli -a 127.0.0.1 list --all --wide')
        need(re.findall(r'^SANDBOX_COUNT\s+(\d+)\s*$', inventory, re.M) == ['0'] and re.search(r'^NODES_SCANNED\s+1/1\s*$', inventory, re.M), 'incomplete provider inventory')
        need(not self.h.worker('ctr --address /data/cubelet/cubelet.sock --namespace default tasks list --quiet'), 'provider tasks present')

    def check_files(self):
        for name, expected in self.c['candidate_hashes'].items():
            need(digest(private(self.candidate / name)) == expected, 'candidate input drift')
        for name, expected in self.c['baseline_hashes'].items():
            p = (SOURCE if name in ('docker-compose.yml', '.env') else STATE) / name
            if expected is None:
                need(not p.exists() and not p.is_symlink(), 'previously absent runtime override appeared')
            else:
                need(p.is_file() and not p.is_symlink() and digest(p) == expected, 'baseline input drift')
        need(digest(private(STOP)) == self.c['worker_stop_sha256'], 'worker stop configuration drift')
        need(digest(private('/etc/baarcha-cube/storage-guard.json')) == self.c['observer_config_sha256'], 'observer configuration drift')
        need(digest('/etc/caddy/Caddyfile') == self.c['caddy_file_sha256'], 'pending disk Caddyfile changed')
        for name in ('drain.json', 'offline.json'):
            need(digest(private(Path(self.c['routing_directory']) / name)) == self.c['routing_hashes'][name], 'reviewed routing candidate changed')
            expected = self.c['routing_hashes'][name] if self.routing_installed else self.c['routing_original_hashes'][name]
            need(digest(routing_file(self.h.ROUTING / name)) == expected, 'installed maintenance routing changed')

    def preflight(self):
        self.check_files()
        self.stop = read_json(STOP)
        need(self.stop['controller_id'] == self.c['controller_id'] and self.stop['worker_boot_id'] == self.c['worker_boot_id'], 'stale worker-stop controller/boot pin')
        need(not self.h.MARKER.exists(), 'worker stop marker present')
        for path, revision in (('/opt/baarcha/app', self.c['platform_revision']), (str(SOURCE), self.c['runtime_revision'])):
            need(self.h.run(['git', '-C', path, 'rev-parse', 'HEAD']).decode().strip() == revision, 'deployment revision drift')
            need(not self.h.run(['git', '-C', path, 'status', '--porcelain', '--untracked-files=no']).strip(), 'tracked deployment files dirty')
        cp = self.inspect(self.current)
        need(cp['Image'] == self.c['controller_image'] and cp['State']['Running'] and cp['HostConfig']['RestartPolicy']['Name'] == 'unless-stopped', 'controller baseline differs')
        need(cp['Config']['Labels']['com.docker.compose.project'] == self.project, 'unexpected compose project')
        env = dict(x.split('=', 1) for x in cp['Config']['Env'] if '=' in x)
        need(env.get('SANDBOXD_CUBE_ENABLED', 'false') == 'false', 'initial controller Cube-enabled')
        manifest = read_json(self.candidate / 'review-manifest.json')
        need(manifest['controller_before'] == self.current and manifest['controller_image'] == self.c['controller_image'] and manifest['app_id'] == self.c['allowed_app_id'] and manifest['worker_boot_id'] == self.c['worker_boot_id'], 'candidate manifest identity differs')
        need(manifest['scope'] == 'one-owned-synthetic-app' and manifest['global_rollout'] is False and manifest['installed'] is False, 'candidate scope differs')
        need(manifest['controller_env_sha256'] == canonical_hash(env), 'controller environment changed since render')
        need(manifest['platform_revision'] == self.c['platform_revision'] and manifest['runtime_revision'] == self.c['runtime_revision'], 'candidate revision differs')
        need(manifest['files'] == {k: v for k, v in self.c['candidate_hashes'].items() if k != 'review-manifest.json'}, 'candidate manifest file hashes differ')
        self.merged = read_json(self.candidate / 'merged-compose.private.json')
        actual = json.loads(self.compose('config', '--format', 'json', candidate=True))
        need(actual == self.merged, 'candidate Compose no longer renders reviewed configuration')
        self.admission = check_candidate(actual, self.c)
        need(self.admission == self.stop['admission'] and self.admission['storage_guard'] == read_json('/etc/baarcha-cube/storage-guard.json'), 'candidate admission differs from installed reviewed guard')
        image = json.loads(self.h.run(['docker', 'image', 'inspect', actual['services']['sandboxd']['image']]))[0]
        need(image['Id'] == self.c['controller_image'], 'candidate changes controller image')
        need(self.h.worker('cat /proc/sys/kernel/random/boot_id') == self.c['worker_boot_id'], 'worker boot changed')
        self.nested = self.h.NestedLock(self.c['worker_boot_id'])
        self.empty()
        self.h.provider_jobs()
        self.baseline = {'containers': self.h.container_identities(), 'timers': {t: self.h.unit(t) for t in self.h.TIMERS},
                         'host_relays': {t: self.h.unit(t) for t in HOST_RELAYS}, 'controller': cp['Id'], 'controller_image': cp['Image']}
        self.baseline['sandbox_rows'] = self.sandbox_rows()
        need(not any(v['name'] in ('/src-cube-management-api-1', '/src-cube-management-proxy-1') for v in self.baseline['containers']), 'existing sidecars require separate reviewed transition')
        need(all(v['ActiveState'] in ('active', 'inactive') for v in self.baseline['host_relays'].values()), 'relay unit state ambiguous')
        for name, state in self.baseline['host_relays'].items():
            need(state['FragmentPath'] == '/etc/systemd/system/' + name and not state['DropInPaths'] and digest(state['FragmentPath']) == self.c['host_relay_unit_hashes'][name], 'host relay unit changed')
        self.online = json.loads(self.h.http('http://127.0.0.1:2019/config/'))
        # Preserve the exact live alias even though it is absent from the disk
        # Caddyfile. Both independently reviewed versions are pinned above.
        need(canonical_hash(self.online) == self.c['caddy_live_sha256'], 'live Caddy configuration drift')
        self.h.atomic(self.job / 'online-caddy.json', self.online)
        for name in ('drain', 'offline'):
            self.h.run(['caddy', 'validate', '--config', str(Path(self.c['routing_directory']) / (name + '.json'))])
        self.h.reviewed_preview_host()
        self.fresh_observation()
        self.event('baseline', self.baseline)
        self.advance('preflight-passed')

    def fresh_observation(self):
        g = self.admission['storage_guard']
        o = read_json(g['observation_path'])
        now = time.clock_gettime_ns(time.CLOCK_BOOTTIME)
        need(o['version'] == 1 and o['generation'] > 0 and 0 < o['started_boottime_ns'] <= o['completed_boottime_ns'] <= now and now - o['started_boottime_ns'] <= 30_000_000_000, 'storage observation stale')
        need(Path('/proc/sys/kernel/random/boot_id').read_text().strip() == g['outer_boot_id'], 'outer boot pin differs')
        for k in ('observer_id', 'worker_machine_id', 'outer_boot_id', 'inner_fs_uuid', 'outer_fs_uuid'):
            need(o[k] == g[k], 'storage observation identity differs')
        need(o['worker_boot_id'] == g['expected_boot_id'] and min(o['inner_free_bytes'], o['outer_free_bytes']) >= 96 * 1024**3, 'storage baseline unavailable')

    def fence(self):
        self.changed = True
        self.routing_installed = True
        for name in ('drain.json', 'offline.json'):
            original = self.h.ROUTING / name
            need(digest(original) == self.c['routing_original_hashes'][name], 'routing drift before installation')
            install_bytes(self.job / ('routing-' + name + '.before'), original.read_bytes())
        for name in ('drain.json', 'offline.json'):
            install_bytes(self.h.ROUTING / name, private(Path(self.c['routing_directory']) / name).read_bytes(), routing=True)
        self.h.run(['caddy', 'reload', '--config', str(self.h.ROUTING / 'drain.json')])
        self.h.run(['systemctl', 'stop', *self.h.TIMERS])
        self.h.wait_for(lambda: all(self.h.unit(t.replace('.timer', '.service'))['ActiveState'] == 'inactive' for t in self.h.TIMERS), 120)
        self.h.wait_for(lambda: self.h.db_counts()['active'] == 0 and self.h.pg_counts()['thumbnail'] == 0, 120)
        self.h.run(['caddy', 'reload', '--config', str(self.h.ROUTING / 'offline.json')])
        offline = json.loads(self.h.http('http://127.0.0.1:2019/config/'))
        need(offline == json.loads((self.h.ROUTING / 'offline.json').read_text()), 'offline Caddy differs')
        scopes = self.h.caddy_fence_scopes(offline)
        need(scopes and self.h.model_routes_unfenced(scopes), 'unrelated inference might be fenced')
        statuses = {p: self.h.route_status(p) for p in ('/api/projects', '/api/bridge', '/api/apps/not-an-app/preview', '/api/tools/call')}
        statuses['preview'] = self.h.route_status('/', True)
        need(all(v == 503 for v in statuses.values()) and self.h.route_status('/') == 200, 'actual offline route checks failed')
        self.event('offline-statuses', statuses)
        self.h.wait_for(lambda: self.observer.process_sockets(self.inspect(self.current)['State']['Pid'])['tcp_non_listen'] == 0, 120)
        before = self.observer.collect(self.current)
        self.event('drain-before-stop', before)
        need(not before['errors'] and before['scheduled_runtime_writers_inactive'] and before['caddy']['equals_reviewed_offline'] and before['controller_sockets']['stable_socket_set'] and before['controller_sockets']['tcp_non_listen'] == 0, 'controller drain not proven')
        self.empty()
        need(self.h.pg_counts()['thumbnail'] == 0, 'thumbnail write in progress')
        since = self.h.utc()
        self.h.run(['docker', 'update', '--restart=no', self.current])
        self.h.run(['docker', 'stop', '--time=-1', self.current], 60)
        after = self.observer.collect(self.current, since)
        self.event('drain-after-stop', after)
        need(not after['errors'] and after['controller_shutdown']['regular_http_shutdown_success_observed'], 'graceful HTTP shutdown unproven')
        self.empty()
        # Reuse the reviewed closed-descriptor scan and SQLite backup only.
        holder = object.__new__(self.h.Enrollment)
        holder.job = self.job
        holder.event = self.event
        self.h.Enrollment.closed_backup(holder)
        backups = {}
        for p in (KEY, STOP, SOURCE / '.env', SOURCE / 'docker-compose.yml', STATE / 'runtime-compose.json', STATE / 'active-images.json'):
            name = ('source-env' if p.name == '.env' else p.name) + '.before'
            if p.exists():
                raw = p.read_bytes()
                install_bytes(self.job / name, raw)
                backups[str(p)] = {'backup': name, 'sha256': digest(p)}
            else:
                backups[str(p)] = None
        self.backups = backups
        self.event('closed-config-backups', backups)
        closed = self.h.container_identities()
        changes = validate_drain_transition(self.baseline['containers'], closed, self.baseline['sandbox_rows'], self.sandbox_rows())
        self.event('closed-container-baseline', {'containers': closed, 'completed_drain_recreations': changes})
        self.closed_containers = closed
        self.advance('controller-fenced')

    def activate(self):
        self.check_files()
        self.empty()
        self.installed = True  # First rename may succeed before the second fails.
        for name in ('runtime-compose.json', 'active-images.json'):
            install_bytes(STATE / name, private(self.candidate / name).read_bytes())
        self.h.run(['systemctl', 'start', *HOST_RELAYS])
        self.recreated = True  # CLI failures can still create a controller.
        self.compose('up', '-d', '--no-deps', '--no-build', '--pull', 'never', '--force-recreate', *SERVICES)
        self.current = self.compose('ps', '-q', 'sandboxd').decode().strip()
        need(SHA.fullmatch(self.current), 'controller creation outcome unknown')
        self.verify_ready(True)
        self.refresh_stop_pin()
        self.empty()
        self.advance('candidate-ready')

    def verify_ready(self, candidate):
        def ready():
            try:
                cp = self.inspect(self.current)
                need(cp['Image'] == self.c['controller_image'] and cp['State']['Running'] and cp['HostConfig']['RestartPolicy']['Name'] == 'unless-stopped', 'controller wrong/stopped')
                if candidate:
                    need(cp['Config'].get('User', '') in ('', '0', 'root', '0:0', 'root:root') and cp['HostConfig'].get('UsernsMode') == 'host', 'controller observer UID namespace differs')
                    env = dict(x.split('=', 1) for x in cp['Config']['Env'] if '=' in x)
                    expected = self.merged['services']['sandboxd']['environment']
                    need(all(env.get(k) == str(v) for k, v in expected.items()), 'controller environment differs')
                    mounts = [v for v in cp['Mounts'] if v['Destination'] == '/run/sandboxd-cube-storage']
                    need(len(mounts) == 1 and mounts[0]['Source'] == '/run/sandboxd-cube-storage' and mounts[0]['RW'] is False, 'observer mount differs')
                    for name in SERVICES[1:]:
                        ident = self.compose('ps', '-q', name).decode().strip()
                        relay = self.inspect(ident)
                        need(relay['Image'] == self.c['relay_image'] and relay['State']['Running'] and relay['State'].get('Health', {}).get('Status') == 'healthy', 'relay not healthy')
                        need(relay['HostConfig']['NetworkMode'] == 'container:' + cp['Id'] and not relay['HostConfig'].get('PortBindings'), 'relay namespace/exposure differs')
                        need(relay['Config']['User'] == '65532:65532' and relay['HostConfig'].get('UsernsMode') == 'host' and relay['HostConfig'].get('GroupAdd') == ['982'], 'live relay identity differs')
                        need(relay['HostConfig'].get('ReadonlyRootfs') is True and relay['HostConfig'].get('CapDrop') == ['ALL'] and 'no-new-privileges:true' in relay['HostConfig'].get('SecurityOpt', []), 'live relay privileges differ')
                        socket_mounts = [v for v in relay['Mounts'] if v['Destination'] == '/run/cube-management']
                        need(len(socket_mounts) == 1 and socket_mounts[0]['Source'] == '/run/cube-management' and socket_mounts[0]['RW'] is False, 'live relay socket mount differs')
                        need(os.readlink('/proc/' + str(relay['State']['Pid']) + '/ns/net') == os.readlink('/proc/' + str(cp['State']['Pid']) + '/ns/net'), 'actual relay namespace differs')
                    self.fresh_observation()
                return self.h.http('http://127.0.0.1:9090/healthz').strip() == b'ok' and self.h.http('http://127.0.0.1:9090/readyz').strip() == b'ready'
            except Exception:
                return False
        self.h.wait_for(ready, 60)

    def refresh_stop_pin(self):
        value = read_json(STOP)
        need(value['controller_id'] in (self.c['controller_id'], getattr(self, 'pinned_controller', self.c['controller_id'])), 'unexpected stop controller pin changed')
        value['controller_id'] = self.current
        install_bytes(STOP, (json.dumps(value, sort_keys=True, indent=2) + '\n').encode())
        self.pinned_controller = self.current
        self.event('stop-pin-' + self.current[:12], {'controller_id': self.current, 'config_sha256': digest(STOP)})

    def reopen(self):
        need(not self.h.MARKER.exists(), 'stop marker appeared')
        need(unrelated(self.h.container_identities(), self.project) == unrelated(getattr(self, 'closed_containers', self.baseline['containers']), self.project), 'unrelated container identities changed')
        self.traffic_restore_attempted = True  # Reload may succeed even if its CLI result is lost.
        self.h.run(['caddy', 'reload', '--config', str(self.job / 'online-caddy.json')])
        need(json.loads(self.h.http('http://127.0.0.1:2019/config/')) == self.online, 'Caddy restore differs')
        for timer, old in self.baseline['timers'].items():
            if old['ActiveState'] == 'active':
                self.h.run(['systemctl', 'start', timer])

    def refence_after_reopen(self):
        # Traffic may have entered after a successful Caddy reload even when a
        # later timer/evidence operation failed. Re-drain before rollback.
        self.h.run(['caddy', 'reload', '--config', str(self.h.ROUTING / 'drain.json')])
        self.h.run(['systemctl', 'stop', *self.h.TIMERS])
        self.h.wait_for(lambda: all(self.h.unit(t.replace('.timer', '.service'))['ActiveState'] == 'inactive' for t in self.h.TIMERS), 120)
        self.h.wait_for(lambda: self.h.db_counts()['active'] == 0 and self.h.pg_counts()['thumbnail'] == 0, 120)
        self.h.run(['caddy', 'reload', '--config', str(self.h.ROUTING / 'offline.json')])
        need(self.offline_verified(), 'failed to re-establish offline routing')
        self.h.wait_for(lambda: self.observer.process_sockets(self.inspect(self.current)['State']['Pid'])['tcp_non_listen'] == 0, 120)
        self.event('failure-traffic-refenced', {'offline_verified': True, 'active_tasks': 0, 'thumbnail_captures': 0})
        self.traffic_restore_attempted = False

    def offline_verified(self):
        try:
            return json.loads(self.h.http('http://127.0.0.1:2019/config/')) == json.loads((self.h.ROUTING / 'offline.json').read_text())
        except Exception:
            return False

    def rollback(self):
        self.empty()  # Never roll back after any provider allocation/binding.
        if self.recreated:
            self.compose('stop', '--timeout', '-1', *SERVICES)
        if self.installed:
            for name in ('runtime-compose.json', 'active-images.json'):
                p = STATE / name
                old = self.backups[str(p)]
                current = digest(p) if p.exists() else None
                before = old['sha256'] if old is not None else None
                need(current in (self.c['candidate_hashes'][name], before), 'override drift prevents rollback')
                if current == before:
                    continue  # Partial activation left this file untouched.
                if old is None:
                    p.unlink()
                    fd = os.open(p.parent, os.O_RDONLY | os.O_DIRECTORY)
                    os.fsync(fd)
                    os.close(fd)
                else:
                    backup = self.job / old['backup']
                    need(digest(backup) == old['sha256'], 'backup changed')
                    install_bytes(p, backup.read_bytes())
        if self.recreated:
            # Remove only our stopped sidecars; never compose down / prune / -v.
            for name in SERVICES[1:]:
                ident = self.h.run(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + self.project, '--filter', 'label=com.docker.compose.service=' + name]).decode().strip()
                if ident:
                    r = self.inspect(ident)
                    need(not r['State']['Running'] and r['Image'] == self.c['relay_image'], 'rollback sidecar identity uncertain')
                    self.h.run(['docker', 'rm', ident])
            self.compose('up', '-d', '--no-deps', '--no-build', '--pull', 'never', '--force-recreate', 'sandboxd')
            self.current = self.compose('ps', '-q', 'sandboxd').decode().strip()
        else:
            self.h.run(['docker', 'update', '--restart=unless-stopped', self.current])
            self.h.run(['docker', 'start', self.current])
        self.verify_ready(False)
        if self.current != self.c['controller_id']:
            self.refresh_stop_pin()
        for name, old in self.baseline['host_relays'].items():
            if old['ActiveState'] == 'inactive':
                self.h.run(['systemctl', 'stop', name])
        self.empty()
        self.reopen()
        if self.routing_installed:
            for name in ('drain.json', 'offline.json'):
                current = digest(self.h.ROUTING / name)
                need(current in (self.c['routing_hashes'][name], self.c['routing_original_hashes'][name]), 'routing drift during rollback')
                if current != self.c['routing_original_hashes'][name]:
                    backup = self.job / ('routing-' + name + '.before')
                    need(digest(backup) == self.c['routing_original_hashes'][name], 'routing backup changed')
                    install_bytes(self.h.ROUTING / name, backup.read_bytes(), routing=True)
        self.advance('rolled-back-without-database-rewind')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--config', required=True)
    p.add_argument('--config-sha256', required=True)
    p.add_argument('--script-sha256', required=True)
    p.add_argument('--job', required=True)
    p.add_argument('--execute', action='store_true')
    a = p.parse_args()
    need(sys.platform == 'linux' and os.geteuid() == 0, 'native Linux root required')
    need(digest(__file__) == a.script_sha256 and digest(private(a.config)) == a.config_sha256, 'reviewed source/config hash mismatch')
    os.umask(0o077)
    job = Path(a.job)
    need(job.parent == Path('/opt/baarcha-bench') and re.fullmatch(r'cube-controller-cutover-[a-z0-9-]+', job.name), 'fresh fixed private job required')
    job.mkdir(mode=0o700)
    task = Cutover(read_json(a.config), job)
    # The existing reviewed fixture wrapper owns exactly these four flock paths.
    handles = []
    import fcntl
    try:
        for name in task.h.LOCKS:
            path = Path(name)
            need(path.resolve(strict=True) == path, 'lock symlink')
            fd = os.open(path, os.O_RDWR | os.O_NOFOLLOW)
            handles.append(fd)
            s = os.fstat(fd)
            need(stat.S_ISREG(s.st_mode) and s.st_uid == 0 and s.st_nlink == 1, 'unsafe operator lock')
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            signal.signal(sig, lambda *_: print('Signal noted; retaining locks through rollback.', flush=True))
        try:
            task.preflight()
            if not a.execute:
                task.advance('check-only-complete')
                return
            task.fence()
            task.activate()
            task.reopen()
            task.advance('complete-single-app-controller-enrollment')
        except Exception as error:
            task.event('failure', {'at': task.h.utc(), 'phase': task.phase, 'error_class': type(error).__name__, 'message': str(error) if isinstance(error, RuntimeError) else 'bounded operation failed'})
            if task.changed:
                try:
                    if task.traffic_restore_attempted:
                        task.refence_after_reopen()
                    task.rollback()
                except Exception:
                    task.event('rollback-refused', {'locks_retained': True, 'offline_routing_verified': task.offline_verified(), 'database_rewound': False, 'guest_mutation': False})
                    print('Rollback incomplete: locks retained; consult recorded routing state before operator recovery.', flush=True)
                    while True:
                        release = job / 'release-after-manual-recovery.json'
                        if release.exists():
                            v = read_json(release)
                            if v == {'job': str(job), 'operator_owns_remaining_fence': True}:
                                break
                        time.sleep(1)
            raise SystemExit(1)
    finally:
        if task.nested is not None:
            task.nested.close()
        for fd in handles:
            os.close(fd)


if __name__ == '__main__':
    main()
