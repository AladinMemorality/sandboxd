#!/usr/bin/env python3
"""Migrate a reviewed cohort of stopped Docker projects; retain every source.

Uses the existing traffic/task fence and the journaled native migration CLI.
No worker power operation, source retirement, database rewind or default change.
"""
import contextlib
import copy
import http.client
import importlib.util
import json
import os
from pathlib import Path
import re
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
FILES = tuple(dict.fromkeys((*p.FILES, Path(__file__).resolve(), CONFIG,
    *(ROOT / ('worker-lifecycle/' + name) for name in
      ('planned.py', 'maintenance.py', 'boot_transition.py', 'external_clean.py')))))


def validate_plan(plan):
    m.validate_plan(plan, kind=KIND, files=FILES)


def validate_config(c):
    need(set(c) == {'version', 'fleet_sha256', 'files', 'cli', 'migrations', 'homes',
                    'presets', 'resources', 'templates', 'archives', 'projects'}, 'Exact cohort configuration required')
    need(c['version'] == 1 and b.SHA.fullmatch(c['fleet_sha256']), 'Reviewed fleet identity required')
    need(isinstance(c['projects'], list) and 1 <= len(c['projects']) <= 4, 'Cohort must contain one to four projects')
    ids, apps, containers = set(), set(), set()
    for row in c['projects']:
        need(set(row) == {'app_id', 'sandbox_id', 'preset', 'template_id', 'container_id', 'recorded_container_id', 'image'}, 'Exact source identity required')
        need(all(re.fullmatch(r'[0-9A-HJKMNP-TV-Z]{26}', row[k]) for k in ('app_id', 'sandbox_id')), 'Invalid project identity')
        need(row['app_id'] != MOTION_APP, 'Motion requires its separate guest and worker acceptance')
        need(row['preset'] in ('react-vite', 'react-pro', 'nextjs', 'node-express'), 'Database and special presets require separate closure')
        need(re.fullmatch(r'tpl-[a-f0-9]+', row['template_id']) and b.SHA.fullmatch(row['container_id'])
             and re.fullmatch(r'sha256:[a-f0-9]{64}', row['image']), 'Immutable source and target required')
        need(re.fullmatch(r'(?:[a-f0-9]{12}|[a-f0-9]{64})', row['recorded_container_id'])
             and row['container_id'].startswith(row['recorded_container_id']), 'Recorded Docker ID does not match inspected full identity')
        need(row['sandbox_id'] not in ids and row['app_id'] not in apps and row['container_id'] not in containers, 'Duplicate cohort identity')
        ids.add(row['sandbox_id']); apps.add(row['app_id']); containers.add(row['container_id'])
    need(c['archives'] == '/mnt/nvme/baarcha-cube/migration-archives', 'Private NVMe migration archive root required')
    for name in ('cli', 'homes', 'presets', 'resources', 'templates'):
        need(c[name] in c['files'], 'Unpinned cohort input')
    need(any(str(Path(name).parent) == c['migrations'] for name in c['files']), 'Pinned schema migrations required')
    for path, digest in c['files'].items():
        need(Path(path).is_absolute() and '..' not in Path(path).parts and b.SHA.fullmatch(digest), 'Invalid cohort input pin')


def stopped_source(value, row):
    state = value['State']
    need(value['Id'] == row['container_id'] and value['Image'] == row['image'], 'Docker source generation changed')
    need(not state['Running'] and not state['Restarting'] and not state['Paused'] and state['Pid'] == 0
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
        self.event('reopen-intent'); h.reopen(); h.close_nested()
        result = {'version': 1, 'online_restored': True, 'customer_projects_migrated': len(h.c['projects']),
                  'worker_power_operations': 0, 'original_sources_retained': True, 'full_backup': False,
                  'global_migration_complete': False}
        self.event('complete', result)
        return result


class Host(p.Host):
    def __init__(self, *args):
        super().__init__(*args)
        self.c = b.strict(b.trusted(CONFIG)); validate_config(self.c)
        self.accepted = []
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
    def source_fence(self):
        for row in self.c['projects']: stopped_source(self.inspect(row['container_id']), row)
    def preflight(self, defer_busy=False):
        self.inputs()
        result = super().preflight(defer_busy=defer_busy)
        self.source_fence()
        self.environment = dict(v.split('=', 1) for v in self.cp()['Config']['Env'])
        self.bridge.plan = {'controller_image': self.e['controller_image']}
        need(self.bridge.recreated(self.environment) == self.e['controller_id'], 'Controller or relay baseline differs')
        self.templates = b.strict(b.trusted(self.c['templates']))
        need(b.strict(self.environment['SANDBOXD_CUBE_TEMPLATES']) == self.templates, 'Current template map differs')
        with self.db() as db:
            for row in self.c['projects']:
                actual = db.execute('SELECT app_id,runtime_provider,container_id FROM sandbox WHERE id=?', (row['sandbox_id'],)).fetchone()
                need(actual == (row['app_id'], 'docker', row['recorded_container_id']), 'Current Docker binding differs')
                need(db.execute('SELECT count(*) FROM runtime_migration WHERE sandbox_id=?', (row['sandbox_id'],)).fetchone()[0] == 0, 'Existing journal requires explicit continuation')
                need(self.templates.get(row['preset']) == row['template_id'], 'Target template differs')
        self.inventory('online-preflight')
        return result
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
        need(result.returncode == 0, 'Native migration command failed; inspect retained private output and journal')
        return b.strict(b.trusted(out))
    def inventory(self, name):
        report = self.run_cli(['fleet-preflight'], name, 600)
        need(report['identity_sha256'] == self.c['fleet_sha256'], 'Canonical fleet identity changed')
        projects = {v['sandbox_id']: v for v in report['projects']}
        for row in self.c['projects']:
            value = projects[row['sandbox_id']]
            need(value['state'] == 'preflight_passed' and value['target_preset'] == row['preset']
                 and value['template_id'] == row['template_id'], 'Selected project no longer eligible')
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
        for row in self.c['projects']:
            self.fence(); self.source_fence(); self.inputs()
            self.event('project-migration-intent', {'sandbox_id': row['sandbox_id']})
            result = self.run_cli(['--sandbox', row['sandbox_id'], '--preset', row['preset'],
                                   '--expected-fleet', self.c['fleet_sha256'], 'migrate'], row['sandbox_id'], 1900)
            need(result['sandbox_id'] == row['sandbox_id'] and result['phase'] == 'complete', 'CLI did not complete project migration')
            with self.db() as db: binding = accepted_binding(db, row)
            self.source_fence()
            self.accepted.append(binding)
            self.plan = copy.deepcopy(self.plan)
            self.plan['bindings'].append(binding)
            self.event('project-migration-verified', binding)
        self.bindings_readonly(); self.fence()
    def restore_controller(self):
        need(len(self.accepted) == len(self.c['projects']), 'Cohort incomplete; retain fence for reviewed recovery')
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
    def ready_fence(self):
        self.controller_ready()
        need(b.strict(b.http('/config/', 2019)) == self.routes['offline'], 'Routing reopened before verification')
        with b.locked(list(self.fds)): pass
    def ready_after_reopen(self): self.controller_ready()
    def close_nested(self):
        if self.nested_context is not None:
            self.nested_context.__exit__(None, None, None); self.nested_context = None


if __name__ == '__main__': m.run_cli(Host, Sequence, validate_plan, __doc__)
