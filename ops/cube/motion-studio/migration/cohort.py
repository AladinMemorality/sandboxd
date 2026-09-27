#!/usr/bin/env python3
"""Migrate exactly the accepted Motion project, then activate its worker adapter."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
c = load('motion_base_cohort', ROOT / 'cutover/cohort.py')
profile = load('motion_profile', HERE / 'profile.py')
b, m, x, need = c.b, c.m, c.x, c.need
CONFIG = HERE / 'controller.PRIVATE.json'
FILES = (*c.FILES, Path(__file__).resolve(), HERE / 'profile.py', CONFIG)
KIND = 'current-generation-motion-customer-migration'


def validate_plan(plan): m.validate_plan(plan, kind=KIND, files=FILES)


def validate_controller_config(value):
    need(set(value) == {'version', 'files', 'acceptance', 'runtime', 'active', 'stop', 'rendered', 'originals'}, 'Exact Motion controller configuration required')
    need(value['version'] == 1, 'Unsupported Motion controller configuration')
    need(set(value['originals']) == {str(b.COMPOSE), str(b.ACTIVE), str(b.STOP)}, 'Exact mutable originals required')
    for name in ('runtime', 'active', 'stop', 'rendered'):
        need(value[name] in value['files'], 'Unpinned controller candidate')
    for name, digest in {**value['files'], **value['originals']}.items():
        need(Path(name).is_absolute() and '..' not in Path(name).parts and b.SHA.fullmatch(digest), 'Invalid controller pin')
    need(Path(value['acceptance']).name == 'cube-motion-acceptance-owned-20260926-02', 'Actual successful guest acceptance required')
    for name in ('guest-acceptance-complete', 'guest-cleanup', 'film-cleanup', 'media-verified', 'upload-50mib', 'oversize-refused', 'sibling-denied', 'stale-generation-denied', 'channel-revoked'):
        need(str(Path(value['acceptance']) / (name + '.json')) in value['files'], 'Missing pinned live acceptance receipt')


class Host(c.Host):
    def __init__(self, *args):
        super().__init__(*args)
        self.controller = b.strict(b.trusted(CONFIG)); validate_controller_config(self.controller)
        self.replaced = False
        self.original_environment = None
        self.original_mounts = None
    def validate_plan(self): validate_plan(self.plan)
    def validate_config(self): c.validate_config(self.c, motion=True)
    def controller_inputs(self):
        for name, digest in self.controller['files'].items(): need(b.digest(name) == digest, 'Reviewed Motion controller input changed')
    def acceptance_proof(self):
        root = Path(self.controller['acceptance'])
        values = {name: b.strict(b.trusted(root / (name + '.json'))) for name in
                  ('guest-acceptance-complete', 'guest-cleanup', 'film-cleanup', 'media-verified', 'upload-50mib', 'oversize-refused', 'sibling-denied', 'stale-generation-denied', 'channel-revoked')}
        need(values['guest-acceptance-complete'] == {'paid_jobs_submitted': 0, 'success': True, 'template_id': profile.TEMPLATE}, 'Live guest acceptance failed')
        need(values['guest-cleanup'] == {'complete': True, 'guests': 2} and values['film-cleanup']['complete'], 'Acceptance fixtures not cleaned')
        need(all(values['media-verified'][k] is True for k in ('head', 'prefix_range', 'suffix_range', 'decoded'))
             and values['upload-50mib']['bytes'] == 50 * 1024**2 and values['oversize-refused'] == {'status': 413, 'project_unchanged': True}, 'Actual media boundary acceptance missing')
        need(values['sibling-denied']['status'] == 502 and values['stale-generation-denied']['status'] == 403
             and values['channel-revoked']['denied'], 'Actual channel isolation acceptance missing')
    def preflight(self, defer_busy=False):
        self.inputs(); self.controller_inputs(); self.acceptance_proof()
        for name, digest in self.controller['originals'].items(): need(b.digest(name) == digest, 'Original controller configuration changed')
        result = c.p.Host.preflight(self, defer_busy=defer_busy)
        self.source_fence(allow_running=True)
        cp = self.cp()
        need(cp['Image'] == profile.IMAGE, 'Reviewed Motion-capable controller image required')
        self.original_environment = dict(v.split('=', 1) for v in cp['Config']['Env'])
        self.original_mounts = copy.deepcopy(cp['Mounts'])
        self.bridge.plan = {'controller_image': self.e['controller_image']}
        need(self.bridge.recreated(self.original_environment) == self.e['controller_id'], 'Controller or relay baseline changed')
        expected = profile.prepare(*(b.strict(b.trusted(path)) for path in (b.COMPOSE, b.ACTIVE, b.STOP)))
        need(expected == [b.strict(b.trusted(self.controller[name])) for name in ('runtime', 'active', 'stop')], 'Controller candidates differ from exact Motion change')
        current = b.strict(self.bridge.compose('config', '--format', 'json'))
        candidate = b.strict(self.command(['/usr/bin/docker', 'compose', '-p', 'src', '--env-file', str(b.SOURCE / '.env'),
                    '-f', str(b.SOURCE / 'docker-compose.yml'), '-f', self.controller['runtime'], '-f', self.controller['active'], 'config', '--format', 'json']))
        profile.validate_rendered(current, candidate)
        need(candidate == b.strict(b.trusted(self.controller['rendered'])), 'Rendered controller candidate changed')
        self.environment = profile.environment(self.original_environment)
        self.templates = b.strict(b.trusted(self.c['templates']))
        # Only this native CLI process changes the selected preset's template.
        original_templates = b.strict(self.original_environment['SANDBOXD_CUBE_TEMPLATES'])
        wanted_templates = {**original_templates, 'react-vite': profile.TEMPLATE}
        need(self.templates == wanted_templates, 'Scoped Motion template override changed other presets')
        row = self.c['projects'][0]
        with self.db() as db:
            actual = db.execute('SELECT app_id,runtime_provider,container_id FROM sandbox WHERE id=?', (profile.SANDBOX,)).fetchone()
            need(c.source_binding_matches(actual, row), 'Motion Docker binding changed')
            need(db.execute('SELECT phase FROM runtime_migration WHERE sandbox_id=?', (profile.SANDBOX,)).fetchone() is None, 'Motion migration already has a journal')
        self.inventory('online-preflight')
        return result
    def run_cli(self, arguments, name, timeout):
        env = dict(self.environment)
        # Retain only the explicitly captured daemon environment and its pinned
        # exact Motion changes; no inherited operator secrets enter the guest.
        env['PATH'] = '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'
        env['SANDBOXD_CUBE_TEMPLATES'] = json.dumps(self.templates, separators=(',', ':'))
        out, err = self.job / (name + '.PRIVATE.json'), self.job / (name + '.PRIVATE.log')
        with out.open('xb') as stdout, err.open('xb') as stderr:
            os.fchmod(stdout.fileno(), 0o600); os.fchmod(stderr.fileno(), 0o600)
            child = subprocess.Popen(self.cli_args() + arguments, env=env, stdout=stdout, stderr=stderr, pass_fds=self.fds)
            self.event('motion-native-process-started', {'pid': child.pid, 'operation': name})
            code = child.wait()  # Native CLI owns its deadline; never kill on an observation timeout.
        self.event('motion-native-process-exited', {'operation': name, 'returncode': code})
        if code != 0: raise c.NativeMigrationFailed('Motion native command exited; inspect private journal')
        return b.strict(b.trusted(out))
    def restore_controller(self):
        self.restoration_scope()
        if not self.accepted:
            self.environment = self.original_environment
            return super().restore_controller()
        need(len(self.accepted) == 1 and self.accepted[0]['app_id'] == profile.APP, 'Exact accepted Motion binding required')
        self.fence(); self.source_fence(); self.controller_inputs()
        originals = {str(path): b.trusted(path) for path in (b.COMPOSE, b.ACTIVE, b.STOP)}
        need(all(b.sha(raw) == self.controller['originals'][name] for name, raw in originals.items()), 'Controller configuration changed before activation')
        x.publish(self.job / 'controller-originals.PRIVATE.json', {name: raw.decode() for name, raw in originals.items()})
        wanted = {str(path): b.trusted(self.controller[name]) for path, name in ((b.COMPOSE, 'runtime'), (b.ACTIVE, 'active'), (b.STOP, 'stop'))}
        self.event('motion-controller-config-intent', {'files': {name: b.sha(raw) for name, raw in wanted.items()}})
        for name, raw in wanted.items():
            need(b.trusted(name) == originals[name], 'Controller configuration CAS refused')
            b.atomic(Path(name), raw)
        self.event('motion-controller-recreate-intent')
        # One recorded attempt. An ambiguous Compose result retains the fence;
        # it is never treated as permission to replay replacement.
        self.bridge.activate()
        ident = self.wait(lambda: self.bridge.recreated(self.environment), 120)
        need(ident != self.e['controller_id'], 'Controller was not recreated with its new mount')
        cp = self.inspect(ident); profile.verify_mounts(self.original_mounts, cp['Mounts'])
        stop = b.strict(b.trusted(b.STOP)); need(stop == b.strict(wanted[str(b.STOP)]), 'Stop contract changed before controller pin')
        old_id = self.e['controller_id']; stop['controller_id'] = ident
        self.event('motion-controller-pin-intent', {'old_controller_id': old_id, 'controller_id': ident})
        b.atomic(b.STOP, b.encoded(stop))
        self.plan = copy.deepcopy(self.plan); self.plan['expected']['controller_id'] = ident; self.e = self.plan['expected']
        for name in wanted: self.plan['files'][name] = b.digest(name)
        # The old Docker source remains stopped after the provider commit.
        # Retain the original baseline on disk for explicit rollback review.
        self.baseline['motion_running'] = False
        self.baseline['motion_restart'] = {'Name': 'no', 'MaximumRetryCount': 0}
        self.replaced = True
        self.event('motion-controller-active', {'controller_id': ident, 'image': cp['Image'], 'source_retained_stopped': True})
        self.ready_fence()
    def controller_ready(self):
        super().controller_ready()
        if self.replaced:
            profile.verify_mounts(self.original_mounts, self.cp()['Mounts'])
            stop = b.strict(b.trusted(b.STOP))
            need(stop['controller_id'] == self.e['controller_id'] and stop['admission'] == b.strict(self.environment['SANDBOXD_CUBE_ADMISSION']), 'Controller/stop admission contract diverged')
    def verify_apis(self):
        super().verify_apis()
        if self.accepted:
            report = b.strict(b.trusted(self.job / 'application-acceptance.json'))
            need(report['projects'][0].get('motion_worker') == {'shared_workspace': True, 'projects': self.motion_baseline['projects']}, 'Production preview did not read the existing Motion projects')
            need(self.motion_jobs() == self.motion_baseline, 'Worker films changed during customer migration')


if __name__ == '__main__': m.run_cli(Host, c.Sequence, validate_plan, __doc__)
