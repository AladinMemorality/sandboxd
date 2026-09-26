#!/usr/bin/env python3
"""Migrate a reviewed cohort of stopped Docker projects; retain every source.

Uses the existing traffic/task fence and the journaled native migration CLI.
No worker power operation, source retirement, database rewind or default change.
"""
import contextlib
import copy
import base64
import http.client
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import sqlite3
import stat
import subprocess

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('cohort_planned', ROOT / 'worker-lifecycle/planned.py')
p = importlib.util.module_from_spec(spec); spec.loader.exec_module(p)
m, b, x, need = p.m, p.b, p.x, p.need
CONFIG = Path(__file__).resolve().with_name('cohort.PRIVATE.json')
DATABASE = Path('/var/lib/sandboxd/state/sandboxd.db')
WORKSPACES = Path('/var/lib/sandboxd/workspaces')
MOTION_APP = '01M3CKN983PFRGMD711PCEPDFD'
SOCKET = Path('/run/baarcha-motion-studio/worker.sock')
KIND = 'current-generation-project-cohort'
DEFAULTS = Path(__file__).resolve().with_name('template_defaults.py')
VERIFY = Path(__file__).resolve().with_name('verify_cohort.mjs')
PAUSE = Path(__file__).resolve().with_name('pause_existing.mjs')
NATIVE_TOOLS = Path(__file__).resolve().with_name('native_tool_probe.py')
native_spec = importlib.util.spec_from_file_location('cohort_native_tools', NATIVE_TOOLS)
native_tools = importlib.util.module_from_spec(native_spec); native_spec.loader.exec_module(native_tools)
PLATFORM_ENV = Path('/opt/baarcha/landing.env')
FILES = tuple(dict.fromkeys((*p.FILES, Path(__file__).resolve(), CONFIG, DEFAULTS, VERIFY, PAUSE, NATIVE_TOOLS, PLATFORM_ENV,
    *(ROOT / ('worker-lifecycle/' + name) for name in
      ('planned.py', 'maintenance.py', 'boot_transition.py', 'external_clean.py')))))


class NativeMigrationFailed(RuntimeError):
    """The foreground CLI exited; no provider operation is still running in it."""


def preview_snapshot_counts(actual, expected):
    candidate = copy.deepcopy(expected)
    snapshots = actual.get('t_cube_pause_snapshot', {})
    count = snapshots.get('READY', 0)
    need(type(count) is int and count >= 0, 'Invalid ready pause snapshot count')
    candidate['t_cube_pause_snapshot'].pop('READY', None)
    if 'READY' in snapshots: candidate['t_cube_pause_snapshot']['READY'] = count
    # Normal preview resumes consume pause snapshots. No job, template or
    # non-READY snapshot transition is authorized by this reconciliation.
    m.verify_counts(actual, candidate)
    return candidate


def validate_plan(plan):
    m.validate_plan(plan, kind=KIND, files=FILES)


def validate_config(c):
    need(set(c) == {'version', 'fleet_sha256', 'files', 'cli', 'migrations', 'homes',
                    'presets', 'resources', 'templates', 'archives', 'projects'} | ({'parallelism'} if c.get('version') == 2 else set()), 'Exact cohort configuration required')
    need(c['version'] in (1, 2) and b.SHA.fullmatch(c['fleet_sha256']), 'Reviewed fleet identity required')
    need(isinstance(c['projects'], list) and 1 <= len(c['projects']) <= (12 if c['version'] == 2 else 4), 'Bounded reviewed cohort required')
    if c['version'] == 2:
        need(type(c['parallelism']) is int and 1 <= c['parallelism'] <= 4, 'One to four parallel migrations required')
    ids, apps, containers = set(), set(), set()
    for row in c['projects']:
        need(set(row) == {'app_id', 'sandbox_id', 'preset', 'template_id', 'container_id', 'recorded_container_id', 'image'} | ({'retry_from_runtime_id'} if 'retry_from_runtime_id' in row else set()), 'Exact source identity required')
        if 'retry_from_runtime_id' in row:
            need(c['version'] == 2 and re.fullmatch(r'[a-f0-9]{32}', row['retry_from_runtime_id']), 'Exact aborted target identity required')
        need(all(re.fullmatch(r'[0-9A-HJKMNP-TV-Z]{26}', row[k]) for k in ('app_id', 'sandbox_id')), 'Invalid project identity')
        need(row['app_id'] != MOTION_APP, 'Motion requires its separate guest and worker acceptance')
        need(row['preset'] in ('react-vite', 'react-pro', 'nextjs', 'node-express'), 'Database and special presets require separate closure')
        need(re.fullmatch(r'tpl-[a-f0-9]+', row['template_id']) and b.SHA.fullmatch(row['container_id'])
             and re.fullmatch(r'sha256:[a-f0-9]{64}', row['image']), 'Immutable source and target required')
        need(re.fullmatch(r'(?:[a-f0-9]{12}|[a-f0-9]{64})', row['recorded_container_id'])
             and row['container_id'].startswith(row['recorded_container_id']), 'Recorded Docker ID does not match inspected full identity')
        need(row['sandbox_id'] not in ids and row['app_id'] not in apps and row['container_id'] not in containers, 'Duplicate cohort identity')
        ids.add(row['sandbox_id']); apps.add(row['app_id']); containers.add(row['container_id'])
    if any('retry_from_runtime_id' in row for row in c['projects']):
        need(len(c['projects']) <= 4 and all('retry_from_runtime_id' in row for row in c['projects']), 'Retries require a separate bounded cohort')
    need(c['archives'] == '/mnt/nvme/baarcha-cube/migration-archives', 'Private NVMe migration archive root required')
    for name in ('cli', 'homes', 'presets', 'resources', 'templates'):
        need(c[name] in c['files'], 'Unpinned cohort input')
    need(any(str(Path(name).parent) == c['migrations'] for name in c['files']), 'Pinned schema migrations required')
    for path, digest in c['files'].items():
        need(Path(path).is_absolute() and '..' not in Path(path).parts and b.SHA.fullmatch(digest), 'Invalid cohort input pin')


def source_binding_matches(actual, row):
    # A normal preview wake expands a stored Docker short ID to its full ID.
    # The source fence still inspects the exact pinned container/image/mount.
    return actual in ((row['app_id'], 'docker', row['recorded_container_id']),
                      (row['app_id'], 'docker', row['container_id']))


def stopped_source(value, row, allow_running=False):
    state = value['State']
    need(value['Id'] == row['container_id'] and value['Image'] == row['image'], 'Docker source generation changed')
    need((allow_running or not state['Running']) and not state['Restarting'] and not state['Paused']
         and ((state['Running'] and allow_running and state['Pid'] > 0) or (not state['Running'] and state['Pid'] == 0))
         and not state['OOMKilled'] and state['ExitCode'] == 0, 'Cohort accepts only cleanly stopped sources')
    need(value['HostConfig']['RestartPolicy'] == {'Name': 'no', 'MaximumRetryCount': 0}, 'Source restart must already be disabled')
    mounts = value['Mounts']
    need(len(mounts) == 1 and mounts[0]['Type'] == 'bind' and mounts[0]['Destination'] == '/home/sandbox'
         and mounts[0]['Source'] == str(WORKSPACES / row['sandbox_id']), 'Canonical source home required')


def accepted_binding(db, row):
    value = db.execute('''SELECT s.app_id,s.runtime_provider,b.provider,b.runtime_id,b.template_id,b.config_revision,
                         r.phase,r.runtime_id,r.template_id,r.archive_sha256,r.home_sha256
                         FROM sandbox s JOIN runtime_binding b ON b.sandbox_id=s.id
                         JOIN runtime_migration r ON r.sandbox_id=s.id WHERE s.id=?''', (row['sandbox_id'],)).fetchone()
    need(value is not None, 'Missing migration binding and journal')
    app, source_provider, provider, runtime, template, revision, phase, journal_runtime, journal_template, archive, home = value
    need(app == row['app_id'] and source_provider == provider == 'cube' and phase == 'complete'
         and runtime and runtime == journal_runtime and template == journal_template == row['template_id']
         and b.SHA.fullmatch(archive) and b.SHA.fullmatch(home), 'Migration did not verify and commit the exact target')
    return dict(sandbox_id=row['sandbox_id'], app_id=app, runtime_id=runtime, template_id=template, config_revision=revision)


class Sequence:
    def __init__(self, host, event): self.host, self.event = host, event
    def stop(self):
        h = self.host
        h.preflight(); self.event('preflight-passed')
        self.event('drain-intent'); h.drain(); self.event('drained')
        h.migrate_cohort(); self.event('cohort-verified')
        return {'cohort_verified': True}
    def start(self, receipt):
        need(receipt == {'cohort_verified': True}, 'Verified cohort receipt required')
        h = self.host
        self.event('controller-start-intent'); h.restore_controller(); h.ready_fence()
        self.event('application-api-verification-intent'); h.verify_apis(); h.ready_fence()
        self.event('reopen-intent'); h.reopen(); h.close_nested()
        result = {'version': 1, 'online_restored': True, 'customer_projects_migrated': len(h.accepted),
                  'selected_projects': len(h.c['projects']), 'partial_cohort': h.partial,
                  'worker_power_operations': 0, 'original_sources_retained': True, 'full_backup': False,
                  'global_migration_complete': False}
        self.event('complete', result)
        return result


class Host(p.Host):
    def __init__(self, *args):
        super().__init__(*args)
        self.c = b.strict(b.trusted(CONFIG)); validate_config(self.c)
        self.accepted = []
        self.partial = False
        self.allow_preview_snapshot_changes = self.c['version'] == 2
    def validate_plan(self): validate_plan(self.plan)
    def db(self):
        db = sqlite3.connect('file:' + str(DATABASE) + '?mode=ro', uri=True, timeout=2)
        db.execute('PRAGMA query_only=ON'); db.execute('BEGIN')
        return contextlib.closing(db)
    def motion_jobs(self):
        # Same bounded authenticated observation as maintenance.py, accepting
        # only the already deployed fixed additional Unix listener.
        raw = b.trusted('/opt/baarcha/motion-studio/worker.env')
        need(b.sha(raw) == self.plan['files']['/opt/baarcha/motion-studio/worker.env'], 'Motion credentials changed')
        key = m.motion_environment_key(raw); identity = self.plan['motion']
        need(x.ticks(identity['worker_pid']) == identity['worker_start_time'], 'Motion process changed')
        env = dict(v.split(b'=', 1) for v in Path('/proc/' + str(identity['worker_pid']) + '/environ').read_bytes().split(b'\0') if b'=' in v)
        need(not env.get(b'STUDIO_WORKER_URL') and env.get(b'STUDIO_WORKER_KEY') == key
             and env.get(b'STUDIO_WORKER_SOCKET') == str(SOCKET).encode(), 'Motion transport changed')
        sock, directory = SOCKET.lstat(), SOCKET.parent.lstat()
        need(stat.S_ISSOCK(sock.st_mode) and (sock.st_uid, sock.st_gid, stat.S_IMODE(sock.st_mode)) == (985, 980, 0o660)
             and stat.S_ISDIR(directory.st_mode) and (directory.st_uid, directory.st_gid, stat.S_IMODE(directory.st_mode)) == (985, 980, 0o750), 'Motion socket identity changed')
        conn = http.client.HTTPConnection('172.19.0.1', 8332, timeout=5)
        try:
            conn.request('GET', '/api/projects', headers={'Authorization': 'Bearer ' + key.decode(), 'Connection': 'close'})
            response = conn.getresponse(); raw = response.read(8 * 1024**2 + 1)
            need(response.status == 200 and len(raw) <= 8 * 1024**2, 'Motion observation unavailable')
            return m.motion_job_summary(b.strict(raw))
        finally: conn.close()
    def inputs(self):
        for path, digest in self.c['files'].items(): need(b.digest(path) == digest, 'Reviewed migration input changed')
    def source_fence(self, allow_running=False):
        for row in self.c['projects']: stopped_source(self.inspect(row['container_id']), row, allow_running)
    def preflight(self, defer_busy=False):
        self.inputs()
        result = super().preflight(defer_busy=defer_busy)
        self.source_fence(allow_running=self.c['version'] == 2)
        self.environment = dict(v.split('=', 1) for v in self.cp()['Config']['Env'])
        self.bridge.plan = {'controller_image': self.e['controller_image']}
        need(self.bridge.recreated(self.environment) == self.e['controller_id'], 'Controller or relay baseline differs')
        self.templates = b.strict(b.trusted(self.c['templates']))
        need(b.strict(self.environment['SANDBOXD_CUBE_TEMPLATES']) == self.templates, 'Current template map differs')
        with self.db() as db:
            for row in self.c['projects']:
                actual = db.execute('SELECT app_id,runtime_provider,container_id FROM sandbox WHERE id=?', (row['sandbox_id'],)).fetchone()
                need(source_binding_matches(actual, row), 'Current Docker binding differs')
                journal = db.execute('SELECT phase,runtime_id FROM runtime_migration WHERE sandbox_id=?', (row['sandbox_id'],)).fetchone()
                if 'retry_from_runtime_id' in row:
                    need(journal == ('aborted', row['retry_from_runtime_id']), 'Reviewed aborted journal changed')
                else:
                    need(journal is None, 'Existing journal requires explicit continuation')
                need(self.templates.get(row['preset']) == row['template_id'], 'Target template differs')
        self.inventory('online-preflight')
        if self.c['version'] == 2:
            observation = self.bridge.observe()
            need(observation.get('consistent') is True and 0 <= observation.get('active', -1) <= 4,
                 'Parallel migration requires a consistent bounded active inventory')
        return result
    def provider(self):
        actual = self.provider_counts()
        expected = self.plan['provider_terminal_counts']
        if self.allow_preview_snapshot_changes and actual != expected:
            candidate = preview_snapshot_counts(actual, expected)
            self.bindings_readonly()
            observation = self.bridge.observe()
            need(observation.get('consistent') is True and observation.get('bindings') == len(self.plan['bindings'])
                 and observation.get('worker_boot_id') == self.e['worker_boot_id'], 'Preview snapshot change requires reconciled bindings')
            self.event('online-preview-snapshots-observed', {'before': expected['t_cube_pause_snapshot'],
                       'after': candidate['t_cube_pause_snapshot']})
            self.plan = copy.deepcopy(self.plan)
            self.plan['provider_terminal_counts'] = candidate
        return m.verify_counts(actual, self.plan['provider_terminal_counts'])
    def before_controller_stop(self):
        if self.c['version'] != 2: return
        # The shared drain has fenced all traffic and drained incoming requests.
        # Pause through the live controller so its supervisor-task checks apply.
        need(b.strict(b.http('/config/', 2019)) == self.routes['offline'], 'Preview traffic must be fenced before pausing')
        self.quiet_tasks(); self.source_fence(allow_running=True); self.bindings_readonly()
        # Traffic is now fenced; account for previews that woke since preflight.
        # From this point only our acknowledged pauses may advance the baseline.
        self.provider()
        self.allow_preview_snapshot_changes = False
        need(b.digest(PAUSE) == self.plan['files'][str(PAUSE)], 'Reviewed pause helper changed')
        path = self.job / 'existing-bindings.PRIVATE.json'
        expected = [dict(sandbox_id=v['sandbox_id'], provider='cube') for v in self.plan['bindings']]
        expected.extend(dict(sandbox_id=v['sandbox_id'], provider='docker') for v in self.c['projects'])
        x.publish(path, expected)
        proof = b.strict(self.command(['/opt/baarcha/node22/bin/node', '--env-file=' + str(PLATFORM_ENV), str(PAUSE), str(path)], 600))
        need(proof.get('success') is True and set(proof.get('paused', [])) == {v['sandbox_id'] for v in expected}, 'Task-aware guest pause incomplete')
        self.source_fence()
        newly = proof.get('newly_paused_cube')
        need(isinstance(newly, list) and len(newly) == len(set(newly))
             and set(newly) <= {v['sandbox_id'] for v in self.plan['bindings']}, 'Invalid acknowledged pause set')
        # Each newly paused Cube guest adds one completed pause snapshot. Keep
        # every other provider table and state pinned to the original baseline.
        self.plan = copy.deepcopy(self.plan)
        counts = self.plan['provider_terminal_counts']['t_cube_pause_snapshot']
        if newly: counts['READY'] = counts.get('READY', 0) + len(newly)
        self.provider()
        observation = self.bridge.observe()
        need(observation.get('consistent') is True and observation.get('active') == 0, 'Active guests remain; do not start imports')
        self.event('existing-guests-paused', proof)
    def cli_args(self):
        return [self.c['cli'], '--database', str(DATABASE), '--workspaces', str(WORKSPACES),
                '--migrations', self.c['migrations'], '--archives', self.c['archives'],
                '--keyfile', '/var/lib/sandboxd/secrets.key', '--home-manifests', self.c['homes'],
                '--fleet-presets', self.c['presets'], '--template-resources', self.c['resources']]
    def run_cli(self, arguments, name, timeout):
        env = dict(os.environ); env.update(self.environment)
        env['SANDBOXD_CUBE_TEMPLATES'] = json.dumps(self.templates, separators=(',', ':'))
        out, err = self.job / (name + '.PRIVATE.json'), self.job / (name + '.PRIVATE.log')
        with out.open('xb') as stdout, err.open('xb') as stderr:
            os.chmod(out, 0o600); os.chmod(err, 0o600)
            result = subprocess.run(self.cli_args() + arguments, env=env, stdout=stdout, stderr=stderr,
                                    pass_fds=self.fds, timeout=timeout)
        if result.returncode != 0:
            raise NativeMigrationFailed('Native command exited; inspect retained private output and journal')
        return b.strict(b.trusted(out))
    def inventory(self, name):
        report = self.run_cli(['fleet-preflight'], name, 600)
        need(report['identity_sha256'] == self.c['fleet_sha256'], 'Canonical fleet identity changed')
        projects = {v['sandbox_id']: v for v in report['projects']}
        for row in self.c['projects']:
            value = projects[row['sandbox_id']]
            need(value['state'] == 'preflight_passed' and value['target_preset'] == row['preset']
                 and value['template_id'] == row['template_id'], 'Selected project no longer eligible')
    def reconcile_template_default(self, row):
        self.fence(); self.source_fence()
        with self.db() as db:
            target = db.execute('''SELECT s.app_id,s.runtime_provider,r.phase,r.runtime_id,r.template_id,
                                  r.archive_sha256,r.home_sha256 FROM sandbox s JOIN runtime_migration r
                                  ON r.sandbox_id=s.id WHERE s.id=?''', (row['sandbox_id'],)).fetchone()
        need(target is not None and target[:3] == (row['app_id'], 'docker', 'imported')
             and re.fullmatch(r'[a-f0-9]{32}', target[3]) and target[4] == row['template_id']
             and all(b.SHA.fullmatch(v) for v in target[5:]), 'Exact imported target and verified source archives required')
        source = WORKSPACES / row['sandbox_id'] / '.bash_logout'
        if source.exists() or source.is_symlink():
            self.event('template-default-source-owned', {'sandbox_id': row['sandbox_id']})
            return
        defaults = b.load_module(DEFAULTS)
        need(b.digest(DEFAULTS) == self.plan['files'][str(DEFAULTS)], 'Reviewed template helper changed')
        self.event('template-default-review-intent', {'sandbox_id': row['sandbox_id'], 'runtime_id': target[3],
                   'source_absent': True, 'stock_sha256': defaults.STOCK_SHA256,
                   'stock_base64': base64.b64encode(defaults.STOCK).decode()})
        cache = WORKSPACES / row['sandbox_id'] / '.cache'
        cache_absent = not cache.exists() and not cache.is_symlink()
        code = b.trusted(DEFAULTS, False).decode() + '\nimport json\nproof=reconcile()\n'
        if cache_absent:
            self.event('empty-stock-cache-review-intent', {'sandbox_id': row['sandbox_id'], 'source_absent': True})
            code += 'proof["cache"]=reconcile_empty_cache()\n'
        code += 'print("CUBE_DEFAULT="+json.dumps(proof))\n'
        command = ['ctr', '--address', '/data/cubelet/cubelet.sock', '--namespace', 'default', 'tasks', 'exec',
                   '--tty', '--exec-id', 'cohort-default-' + row['sandbox_id'].lower(), target[3], '/usr/bin/python3', '-c', code]
        result = subprocess.run(b.SSH[:1] + ['-tt'] + b.SSH[1:] + [shlex.join(command)],
                                capture_output=True, timeout=30, pass_fds=self.fds)
        lines = [v for v in result.stdout.decode().splitlines() if v.startswith('CUBE_DEFAULT=')]
        need(result.returncode == 0 and len(lines) == 1, 'Target default reconciliation refused; retain journal for review')
        proof = b.strict(lines[0].split('=', 1)[1])
        need(proof.get('path') == '.bash_logout' and proof.get('absent') is True and
             (proof.get('removed') is False or (proof.get('removed') is True and
              proof.get('sha256') == defaults.STOCK_SHA256 and proof.get('bytes') == 220)), 'Invalid target default receipt')
        self.event('template-default-reconciled', {'sandbox_id': row['sandbox_id'], 'runtime_id': target[3], **proof})
        self.fence()
    def verify_native_tools(self, row):
        if row['sandbox_id'] not in native_tools.SUPPORTED: return
        self.fence(); self.source_fence(); self.inputs()
        need(b.digest(NATIVE_TOOLS) == self.plan['files'][str(NATIVE_TOOLS)], 'Reviewed native tool probe changed')
        with self.db() as db:
            target = db.execute('''SELECT s.app_id,s.runtime_provider,r.phase,r.runtime_id,r.template_id
                                   FROM sandbox s JOIN runtime_migration r ON r.sandbox_id=s.id WHERE s.id=?''',
                                (row['sandbox_id'],)).fetchone()
        need(target is not None and target[:3] == (row['app_id'], 'docker', 'imported')
             and re.fullmatch(r'[a-f0-9]{32}', target[3]) and target[4] == row['template_id'],
             'Native tools require the exact imported, uncommitted target')
        self.event('native-tool-verification-intent', {'sandbox_id': row['sandbox_id'], 'runtime_id': target[3]})
        command = ['ctr', '--address', '/data/cubelet/cubelet.sock', '--namespace', 'default', 'tasks', 'exec',
                   '--tty', '--exec-id', 'cohort-tools-' + row['sandbox_id'].lower(), target[3],
                   '/usr/bin/python3', '-c', b.trusted(NATIVE_TOOLS, False).decode(), row['sandbox_id']]
        result = subprocess.run(b.SSH[:1] + ['-tt'] + b.SSH[1:] + [shlex.join(command)],
                                capture_output=True, timeout=45, pass_fds=self.fds)
        # A lost SSH connection or observation timeout is not a completed probe.
        need(result.returncode != 255, 'Native tool transport lost; inspect actual guest process')
        if result.returncode != 0:
            self.event('native-tool-verification-failed', {'sandbox_id': row['sandbox_id'], 'exit_code': result.returncode})
            raise NativeMigrationFailed('Native tool failed before commit; settle the imported target')
        lines = [v for v in result.stdout.decode().splitlines() if v.startswith('CUBE_NATIVE_TOOL=')]
        need(len(lines) == 1, 'Native tool proof missing')
        proof = b.strict(lines[0].split('=', 1)[1])
        need(proof.get('sandbox_id') == row['sandbox_id'] and proof.get('success') is True, 'Native tool acceptance failed')
        self.event('native-tools-verified', proof)

    def migrate_cohort(self):
        self.fence(); self.inputs(); self.source_fence(); self.inventory('frozen-preflight')
        root = Path(self.c['archives'])
        need(root.parent.resolve(strict=True) == root.parent, 'Archive parent changed')
        need(root.parent.stat().st_dev == Path('/mnt/nvme').stat().st_dev != Path('/').stat().st_dev,
             'NVMe filesystem must be mounted before archive creation')
        if not root.exists(): root.mkdir(mode=0o700)
        need(root.resolve() == root and root.stat().st_uid == 0 and stat.S_IMODE(root.stat().st_mode) == 0o700, 'Private archive root required')
        # Consistent diagnostic checkpoint only; recovery uses per-project
        # journals and current Cube writes, never restoring this whole database.
        backup = self.job / 'before-migration.PRIVATE.sqlite'
        fd = os.open(backup, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600); os.close(fd)
        with self.db() as source, contextlib.closing(sqlite3.connect(backup)) as target: source.backup(target)
        backup.chmod(0o600)
        if self.c['version'] == 2:
            width = self.c['parallelism']
            for start in range(0, len(self.c['projects']), width):
                try:
                    self.migrate_wave(self.c['projects'][start:start + width], start // width)
                except NativeMigrationFailed:
                    self.settle_failed_cohort()
                    break
            self.bindings_readonly(); self.fence()
            return
        for row in self.c['projects']:
            self.fence(); self.source_fence(); self.inputs()
            self.event('project-migration-intent', {'sandbox_id': row['sandbox_id']})
            result = self.run_cli(['--sandbox', row['sandbox_id'], '--preset', row['preset'],
                                   '--expected-fleet', self.c['fleet_sha256'], '--stop-after-import', 'migrate'], row['sandbox_id'] + '-import', 1900)
            need(result['sandbox_id'] == row['sandbox_id'] and result['phase'] == 'imported', 'CLI did not stop at the import boundary')
            self.reconcile_template_default(row)
            self.verify_native_tools(row)
            result = self.run_cli(['--sandbox', row['sandbox_id'], '--expected-fleet', self.c['fleet_sha256'], 'resume'], row['sandbox_id'] + '-verify', 1900)
            need(result['sandbox_id'] == row['sandbox_id'] and result['phase'] == 'complete', 'CLI did not complete project migration')
            with self.db() as db: binding = accepted_binding(db, row)
            self.source_fence()
            self.accepted.append(binding)
            self.plan = copy.deepcopy(self.plan)
            self.plan['bindings'].append(binding)
            self.event('project-migration-verified', binding)
        self.bindings_readonly(); self.fence()
    def migrate_wave(self, rows, index):
        self.fence(); self.source_fence(); self.inputs()
        retry = all('retry_from_runtime_id' in row for row in rows)
        if retry:
            for row in rows:
                self.event('aborted-project-replan-intent', {'sandbox_id': row['sandbox_id'], 'former_runtime_id': row['retry_from_runtime_id']})
                result = self.run_cli(['--sandbox', row['sandbox_id'], '--preset', row['preset'], '--expected-fleet', self.c['fleet_sha256'], 'replan'],
                                      'replan-' + row['sandbox_id'], 600)
                need(result.get('sandbox_id') == row['sandbox_id'] and result.get('phase') == 'planned', 'Replan did not create a fresh journal')
        path = self.job / ('wave-%03d.PRIVATE.json' % index)
        x.publish(path, {'version': 1, 'projects': [dict(sandbox_id=row['sandbox_id'], preset=row['preset']) for row in rows]})
        self.event('parallel-wave-intent', {'index': index, 'sandboxes': [row['sandbox_id'] for row in rows]})
        arguments = ['--batch', str(path), '--expected-fleet', self.c['fleet_sha256']]
        for action, phase in (('migrate', 'imported'), ('resume', 'complete')):
            args = arguments + (['--stop-after-import'] if action == 'migrate' else []) + [('resume' if retry else 'migrate') if action == 'migrate' else action]
            result = self.run_cli(args, 'wave-%03d-%s' % (index, action), 1900)
            values = result.get('projects', [])
            need(result.get('success') is True and len(values) == len(rows)
                 and {v['sandbox_id'] for v in values} == {r['sandbox_id'] for r in rows}
                 and all(v['phase'] == phase for v in values), 'Parallel wave did not reach the exact verified boundary')
            if action == 'migrate':
                for row in rows:
                    self.reconcile_template_default(row)
                    self.verify_native_tools(row)
        for row in rows:
            with self.db() as db: binding = accepted_binding(db, row)
            self.accepted.append(binding)
            self.plan = copy.deepcopy(self.plan); self.plan['bindings'].append(binding)
            self.event('project-migration-verified', binding)
        self.source_fence(); self.bindings_readonly(); self.fence()

    def settle_failed_cohort(self):
        # Only reached after a foreground CLI has actually exited. Timeouts,
        # uncertain creates and failed fences retain maintenance for review.
        self.fence(); self.source_fence(); self.inputs()
        self.event('partial-cohort-settlement-intent')
        accepted_ids = {v['sandbox_id'] for v in self.accepted}
        for row in self.c['projects']:
            sid = row['sandbox_id']
            with self.db() as db:
                journal = db.execute('SELECT phase FROM runtime_migration WHERE sandbox_id=?', (sid,)).fetchone()
            if journal and journal[0] not in ('complete', 'aborted'):
                need(journal[0] in ('planned', 'quiesced', 'archived', 'staged', 'imported', 'verified'),
                     'Uncertain or committed migration requires explicit recovery')
                self.fence(); self.source_fence()
                self.run_cli(['--sandbox', sid, '--expected-fleet', self.c['fleet_sha256'], 'abort'],
                             'settle-' + sid, 600)
            with self.db() as db:
                journal = db.execute('SELECT phase FROM runtime_migration WHERE sandbox_id=?', (sid,)).fetchone()
                if journal and journal[0] == 'complete':
                    binding = accepted_binding(db, row)
                    if sid not in accepted_ids:
                        self.accepted.append(binding); accepted_ids.add(sid)
                        self.plan = copy.deepcopy(self.plan); self.plan['bindings'].append(binding)
                else:
                    need(journal is None or journal[0] == 'aborted', 'Native abort did not settle target')
                    actual = db.execute('SELECT app_id,runtime_provider,container_id FROM sandbox WHERE id=?', (sid,)).fetchone()
                    need(source_binding_matches(actual, row), 'Original source binding changed')
        self.partial = True
        self.restoration_scope()
        self.source_fence(); self.bindings_readonly(); self.fence()
        self.event('partial-cohort-settled', {'accepted': sorted(accepted_ids)})

    def restoration_scope(self):
        accepted_ids = {v['sandbox_id'] for v in self.accepted}
        need(len(accepted_ids) == len(self.accepted), 'Duplicate accepted project')
        with self.db() as db:
            need(db.execute("SELECT count(*) FROM runtime_migration WHERE phase NOT IN ('complete','rolled_back','aborted')").fetchone()[0] == 0,
                 'Incomplete journal prevents controller restoration')
            for row in self.c['projects']:
                if row['sandbox_id'] in accepted_ids:
                    need(accepted_binding(db, row) in self.accepted, 'Accepted project changed')
                else:
                    need(self.partial, 'Cohort incomplete without settlement')
                    value = db.execute('SELECT app_id,runtime_provider,container_id FROM sandbox WHERE id=?', (row['sandbox_id'],)).fetchone()
                    need(source_binding_matches(value, row), 'Unaccepted source changed')

    def restore_controller(self):
        self.restoration_scope()
        self.fence(); self.source_fence()
        self.command(['/usr/bin/docker', 'start', self.e['controller_id']], 60)
        self.bridge.compose('up', '-d', '--no-deps', '--no-build', '--pull', 'never', '--force-recreate', *b.SERVICES[1:])
        self.command(['/usr/bin/docker', 'update', '--restart=unless-stopped', self.e['controller_id']])
        self.wait(lambda: self.bridge.recreated(self.environment), 120)
    def controller_ready(self):
        self.same()
        need(self.bridge.recreated(self.environment) == self.e['controller_id'], 'Controller or relay contract changed')
        need(not Path(str(DATABASE) + '.worker-stop.json').exists(), 'Unexpected worker stop marker')
        observed = self.bridge.observe()
        need(observed.get('consistent') is True and observed.get('worker_boot_id') == self.e['worker_boot_id']
             and observed.get('bindings') == len(self.plan['bindings']), 'Canonical Cube bindings not reconciled')
        self.bridge.fresh(b.strict(b.trusted(b.GUARD)), 0)
        self.bindings_readonly(); self.source_fence()
    def verify_apis(self):
        self.ready_fence()
        need(b.digest(VERIFY) == self.plan['files'][str(VERIFY)] and b.digest(PLATFORM_ENV) == self.plan['files'][str(PLATFORM_ENV)],
             'Reviewed API verifier or credentials changed')
        accepted_ids = {v['sandbox_id'] for v in self.accepted}
        selected = copy.deepcopy(self.c)
        selected['projects'] = [v for v in self.c['projects'] if v['sandbox_id'] in accepted_ids]
        path = self.job / 'accepted-api-input.PRIVATE.json'
        x.publish(path, selected)
        if not selected['projects']:
            self.event('application-apis-verified', {'projects': [], 'ai_tasks_submitted': 0, 'success': True})
            return
        result = b.strict(self.command(['/opt/baarcha/node22/bin/node', '--env-file=' + str(PLATFORM_ENV), str(VERIFY),
                                        str(path), str(self.job / 'application-acceptance.json')], 600))
        need(result.get('success') is True and result.get('ai_tasks_submitted') == 0
             and {v['sandbox_id'] for v in result['projects']} == accepted_ids
             and all(v.get('success') and v.get('paused_after_verification') for v in result['projects']), 'Application API acceptance incomplete')
        self.event('application-apis-verified', result)
    def ready_fence(self):
        self.controller_ready()
        need(b.strict(b.http('/config/', 2019)) == self.routes['offline'], 'Routing reopened before verification')
        with b.locked(list(self.fds)): pass
    def ready_after_reopen(self): self.controller_ready()
    def close_nested(self):
        if self.nested_context is not None:
            self.nested_context.__exit__(None, None, None); self.nested_context = None


if __name__ == '__main__': m.run_cli(Host, Sequence, validate_plan, __doc__)
