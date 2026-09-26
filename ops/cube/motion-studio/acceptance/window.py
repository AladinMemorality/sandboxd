#!/usr/bin/env python3
"""Owned Motion guest acceptance under current production traffic/writer fences."""
import contextlib
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import sqlite3
import subprocess

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('motion_acceptance_cohort', ROOT / 'cutover/cohort.py')
c = importlib.util.module_from_spec(spec); spec.loader.exec_module(c)
p, m, b, x, need = c.p, c.m, c.b, c.x, c.need
HERE = Path(__file__).resolve().parent
CONFIG = HERE / 'acceptance.PRIVATE.json'
KIND = 'current-generation-motion-owned-guest-acceptance'
FILES = tuple(dict.fromkeys((*p.FILES, Path(c.__file__), c.PAUSE, c.NATIVE_TOOLS,
    c.PLATFORM_ENV, Path(__file__).resolve(), CONFIG, HERE / 'probe.mjs', HERE / 'cleanup.mjs',
    *(ROOT / ('worker-lifecycle/' + name) for name in
      ('planned.py', 'maintenance.py', 'boot_transition.py', 'external_clean.py')))))


def validate_config(value):
    need(set(value) == {'version', 'files', 'binary', 'migrations', 'proxy_source', 'video', 'run_directory'}, 'Exact acceptance configuration required')
    need(value['version'] == 1, 'Unsupported acceptance version')
    run = Path(value['run_directory'])
    need(run.parent == Path('/opt/baarcha-bench') and re.fullmatch(r'cube-motion-acceptance-[a-z0-9-]+', run.name), 'Fresh private fixture generation required')
    for key in ('binary', 'proxy_source', 'video'):
        need(value[key] in value['files'], 'Unpinned acceptance input')
    need(any(str(Path(name).parent) == value['migrations'] for name in value['files']), 'Pinned schema required')
    for name, digest in value['files'].items():
        need(Path(name).is_absolute() and '..' not in Path(name).parts and b.SHA.fullmatch(digest), 'Invalid input pin')


def validate_plan(plan): m.validate_plan(plan, kind=KIND, files=FILES)


def customer_state(db):
    # The two owned allocations can touch only admission/storage accounting.
    # Every other canonical table, including all app/config/task/journal rows,
    # must remain byte-for-byte equivalent after the native process exits.
    result = {}
    for name, in db.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"):
        need(re.fullmatch(r'[a-zA-Z0-9_]+', name), 'Unexpected table name')
        if name in {'cube_admission', 'cube_storage_policy', 'cube_storage_grant'}: continue
        rows = sorted(repr(row) for row in db.execute('SELECT * FROM "' + name + '"'))
        result[name] = hashlib.sha256('\n'.join(rows).encode()).hexdigest()
    return result


class Sequence:
    def __init__(self, host, event): self.host, self.event = host, event
    def stop(self):
        h = self.host
        h.preflight(); self.event('preflight-passed')
        self.event('drain-intent'); h.drain(); self.event('drained')
        self.event('owned-acceptance-intent'); result = h.acceptance()
        self.event('owned-acceptance-settled', result)
        return result
    def start(self, result):
        h = self.host
        h.restoration_scope()
        self.event('controller-start-intent'); h.restore_controller(); h.ready_fence()
        self.event('reopen-intent'); h.reopen(); h.close_nested()
        result = {**result, 'online_restored': True, 'customer_projects_migrated': 0,
                  'worker_power_operations': 0, 'global_migration_complete': False}
        self.event('complete', result)
        return result


class Host(p.Host):
    # Reuse the already exercised same-controller restoration and preview pause
    # operations, without enabling the customer cohort or its validator.
    db = c.Host.db
    motion_jobs = c.Host.motion_jobs
    provider = c.Host.provider
    before_controller_stop = c.Host.before_controller_stop
    restore_controller = c.Host.restore_controller
    controller_ready = c.Host.controller_ready
    ready_fence = c.Host.ready_fence
    ready_after_reopen = c.Host.ready_after_reopen
    close_nested = c.Host.close_nested

    def __init__(self, *args):
        super().__init__(*args)
        self.config = b.strict(b.trusted(CONFIG)); validate_config(self.config)
        self.c = {'version': 2, 'projects': []}
        self.allow_preview_snapshot_changes = True
        self.before = None
        self.admission_before = None
        self.settled = None
    def validate_plan(self): validate_plan(self.plan)
    def source_fence(self, allow_running=False):
        proxy = self.inspect(self.plan['motion']['proxy_id'])
        need(proxy['Image'] == self.plan['motion']['proxy_image'] and not proxy['State']['Restarting']
             and not proxy['State']['Paused'], 'Motion Docker source changed')
        # The shared writers() fence requires stopped while offline; reopen()
        # restores this exact source's initial running/stopped state afterward.
    def inputs(self):
        for name, digest in self.config['files'].items(): need(b.digest(name) == digest, 'Acceptance input changed')
    def preflight(self, defer_busy=False):
        self.inputs()
        need(not Path(self.config['run_directory']).exists(), 'Acceptance generation already consumed')
        result = super().preflight(defer_busy=defer_busy)
        self.environment = dict(v.split('=', 1) for v in self.cp()['Config']['Env'])
        self.bridge.plan = {'controller_image': self.e['controller_image']}
        need(self.bridge.recreated(self.environment) == self.e['controller_id'], 'Controller/relay baseline changed')
        admission = b.strict(self.environment['SANDBOXD_CUBE_ADMISSION'])
        need(admission['max_active'] == 4 and admission['cpu_count'] == 2 and admission['memory_mb'] == 2048
             and admission['writable_disk_mb'] == 10240 and admission.get('storage_guard'), 'Reviewed admission profile required')
        return result
    def acceptance(self):
        self.fence(); self.inputs(); self.provider(); self.bindings_readonly()
        with self.db() as db:
            self.before = customer_state(db)
            self.admission_before = dict(db.execute('SELECT admission_key,quote(runtime_id)||quote(template_id)||quote(operation)||quote(token)||quote(state)||quote(charged) FROM cube_admission'))
        x.publish(self.job / 'canonical-before.json', self.before)
        run = Path(self.config['run_directory'])
        native = {key: self.config[key] for key in ('migrations', 'proxy_source', 'video')}
        native.update(directory=str(run), probe=str(HERE / 'probe.mjs'), api_key=self.environment['SANDBOXD_CUBE_API_KEY'],
                      worker_key=m.motion_environment_key(b.trusted('/opt/baarcha/motion-studio/worker.env')).decode(),
                      admission=b.strict(self.environment['SANDBOXD_CUBE_ADMISSION']))
        native_path = self.job / 'native-config.PRIVATE.json'; x.publish(native_path, native)
        # No external canonical SQLite access until this exact process exits.
        # A timeout does not settle it: use Popen.wait without a kill deadline.
        with (self.job / 'native.stdout').open('xb') as out, (self.job / 'native.stderr').open('xb') as err:
            child = subprocess.Popen([self.config['binary'], str(native_path)], stdout=out, stderr=err, pass_fds=self.fds)
            self.event('native-process-started', {'pid': child.pid, 'run_directory': str(run)})
            code = child.wait()
        self.event('native-process-exited', {'returncode': code})
        need(b.strict(b.trusted(run / 'guest-cleanup.json')).get('complete') is True, 'Unsettled Cube fixture allocations')
        # The probe is now terminal and its channels are closed. Cleanup requires
        # an exact new UUID + owned marker and unchanged baseline worker projects.
        self.command(['/opt/baarcha/node22/bin/node', str(HERE / 'cleanup.mjs'), str(run)], 60)
        need(b.strict(b.trusted(run / 'film-cleanup.json')).get('complete') is True, 'Unsettled owned film')
        self.settled = {'version': 1, 'accepted': code == 0, 'native_returncode': code, 'fixtures_cleaned': True}
        self.restoration_scope()
        self.fence(); self.provider()
        x.publish(self.job / 'acceptance-settled.json', self.settled)
        return self.settled
    def restoration_scope(self):
        need(self.settled and self.settled['fixtures_cleaned'], 'Fixture cleanup not proven')
        run = Path(self.config['run_directory'])
        with self.db() as db:
            need(customer_state(db) == self.before, 'Customer canonical state changed')
            current = dict(db.execute('SELECT admission_key,quote(runtime_id)||quote(template_id)||quote(operation)||quote(token)||quote(state)||quote(charged) FROM cube_admission'))
            need(all(current.get(key) == value for key, value in self.admission_before.items()), 'Existing admission changed')
            extra = set(current) - set(self.admission_before)
            expected = set()
            for label in ('owner', 'sibling'):
                intent = run / (label + '-create-intent.json')
                if not intent.exists(): continue
                value = b.strict(b.trusted(intent)); key = 'app:' + value['app_id']
                if key not in extra: continue  # Create failed before admission.
                row = db.execute('SELECT template_id,state,charged FROM cube_admission WHERE admission_key=?', (key,)).fetchone()
                need(row == ('tpl-c0c9813b42db46898f7ddd9f', 'deleted', 0), 'Owned admission not released')
                expected.add(key)
            need(extra == expected, 'Unexpected new admission record')
        self.bindings_readonly()


if __name__ == '__main__': m.run_cli(Host, Sequence, validate_plan, __doc__)
