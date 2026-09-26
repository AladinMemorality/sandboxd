#!/usr/bin/env python3
"""Fenced Motion release with controller/relay restoration; no worker power action."""
import copy
import http.client
import json
from pathlib import Path

import importlib.util

source = (Path(__file__).parents[2] / 'worker-lifecycle/planned.py' if Path(__file__).name == 'window.py'
          else Path('/usr/local/libexec/baarcha-cube-planned.py'))
spec = importlib.util.spec_from_file_location('motion_planned', source)
p = importlib.util.module_from_spec(spec); spec.loader.exec_module(p)
m, b, x, need = p.m, p.b, p.x, p.need
INSTALLED = Path('/usr/local/libexec/baarcha-cube-motion-window.py')
RELEASE = Path('/opt/baarcha-cube/motion-release-20260926')
CONFIG = RELEASE / 'release-plan.PRIVATE.json'
INPUTS = ('release.py', 'natural_stop.py', 'natural_drain.mjs', 'manifest.json',
          'source-comparison.json', '20-cube-uds.conf', 'worker-uds-source.tar')
FILES = (*p.FILES, INSTALLED, CONFIG, *(RELEASE / name for name in INPUTS))
KIND = 'current-generation-motion-worker-release'


def validate_plan(plan):
    m.validate_plan(plan, kind=KIND, files=FILES)


class Sequence:
    def __init__(self, host, event):
        self.host, self.event = host, event

    def stop(self):
        h = self.host
        h.preflight(); self.event('preflight-passed')
        self.event('drain-intent'); h.drain(); self.event('drained')
        self.event('motion-release-intent'); receipt = h.release_motion()
        self.event('motion-release-complete', receipt)
        return receipt

    def start(self, receipt):
        h = self.host
        h.accepted_release(receipt)
        self.event('controller-start-intent'); h.restore_controller()
        self.event('controller-ready'); h.ready_fence()
        self.event('reopen-intent'); h.reopen()
        h.close_nested()
        result = {'version': 1, 'motion_uds_ready': True, 'online_restored': True,
                  'controller_id': h.e['controller_id'], 'worker_boot_id': h.e['worker_boot_id'],
                  'worker_power_operations': 0, 'customer_projects_migrated': 0, 'full_backup': False}
        self.event('complete', result)
        return result


class Host(p.Host):
    def __init__(self, *args):
        super().__init__(*args)
        self.accepted = None

    def validate_plan(self):
        validate_plan(self.plan)

    def preflight(self, defer_busy=False):
        result = super().preflight(defer_busy=defer_busy)
        self.release = b.load_module(RELEASE / 'release.py')
        r = self.release
        self.config = b.strict(b.trusted(CONFIG))
        manifest = b.strict(b.trusted(RELEASE / 'manifest.json', False))
        comparison = b.strict(b.trusted(RELEASE / 'source-comparison.json', False))
        r.validate_config(self.config, comparison['843e9f9']['matching'])
        c = self.config
        need(c['controller_id'] == self.e['controller_id'] and c['controller_image'] == self.e['controller_image'] and
             c['boot_id'] == self.e['outer_boot_id'] and c['fence']['writer_containers'] == [self.plan['motion']['proxy_id']] and
             c['fence']['caddy_sha256'] == r.canonical(self.routes['offline']), 'release and maintenance scope differ')
        need(self.command(['/usr/bin/git', '-C', '/opt/baarcha/app', 'rev-parse', 'HEAD']).decode().strip() == c['platform_revision'],
             'platform revision changed')
        for rel, expected in c['source_before'].items():
            need(r.snapshot(r.APP / rel) == expected, 'Motion source baseline changed before drain')
        need(r.digest(r.UNITFILE) == c['unit_sha256'] and r.digest(r.ROOT / 'worker.env') == c['env_sha256'],
             'Motion unit or environment changed')
        self.sources = r.candidate(RELEASE / 'worker-uds-source.tar', manifest)
        self.drop = b.trusted(RELEASE / '20-cube-uds.conf', False)
        need(r.sha(self.drop) == 'cfb3e196638145854f4b8f0d0905faf90f711bec38579a1e3b4853ae829d44a4', 'reviewed socket drop-in differs')
        need(self.sources['server/index.mjs'] and c['source_before']['server/index.mjs']['sha256'] == manifest['files'][0]['old_sha256'],
             'Motion entrypoint baseline differs')
        stage = self.job / 'motion-release'; stage.mkdir(mode=0o700)
        self.release_run = r.Release(c, stage, self.sources, self.drop)
        self.release_run.natural_module()
        properties = self.release_run.service()
        need(properties['MainPID'] == str(self.plan['motion']['worker_pid']) and properties['Restart'] == 'on-failure' and
             all(properties.get(k) == v for k, v in c['service_properties'].items()), 'Motion service changed')
        self.release_run.key = m.motion_environment_key(b.trusted(r.ROOT / 'worker.env')).decode()
        self.release_run.projects()
        self.controller_environment = dict(v.split('=', 1) for v in self.cp()['Config']['Env'])
        x.publish(self.job / 'controller-environment.PRIVATE.json', self.controller_environment)
        self.bridge.plan = {'controller_image': self.e['controller_image']}
        need(self.bridge.recreated(self.controller_environment) == self.e['controller_id'], 'initial relay/controller contract differs')
        return result

    def release_motion(self):
        self.fence()
        need(not Path('/var/lib/sandboxd/state/sandboxd.db.worker-stop.json').exists(), 'worker stop marker appeared')
        self.release_run.execute()
        state = self.release_run.service(); pid = int(state['MainPID'])
        need(pid > 1 and pid != self.plan['motion']['worker_pid'], 'new verified Motion generation required')
        receipt = {'version': 1, 'worker_pid': pid, 'worker_start_time': x.ticks(pid),
                   'invocation': state['InvocationID'], 'socket': str(self.release.SOCKET),
                   'source_sha256': {name: self.release.sha(raw) for name, raw in self.sources.items()},
                   'projects_sha256': self.config['projects_sha256'], 'project_count': self.config['project_count'],
                   'closed_backup_sha256': self.release.digest(self.release_run.stage / 'closed-data-home-config.tar')}
        x.publish(self.job / 'motion-accepted.json', receipt)
        self.accepted = receipt
        # Adopt only the proven new process and the approved changed entrypoint.
        # The original immutable plan remains on disk for review/recovery.
        self.plan = copy.deepcopy(self.plan)
        self.plan['motion'].update(worker_pid=pid, worker_start_time=receipt['worker_start_time'])
        self.plan['files'][str(m.MOTION_SOURCE_ROOT / 'index.mjs')] = receipt['source_sha256']['server/index.mjs']
        self.accepted_release(receipt); self.fence()
        return receipt

    def accepted_release(self, receipt):
        need(self.accepted == receipt and b.strict(b.trusted(self.job / 'motion-accepted.json')) == receipt,
             'exact accepted Motion release required')
        state = self.release_run.service()
        need(state['MainPID'] == str(receipt['worker_pid']) and state['InvocationID'] == receipt['invocation'] and
             x.ticks(receipt['worker_pid']) == receipt['worker_start_time'], 'accepted Motion process changed')
        for name, digest in receipt['source_sha256'].items():
            need(self.release.digest(self.release.APP / name) == digest, 'accepted Motion source changed')
        for transport in ('tcp', 'uds'):
            self.release_run.projects(transport)

    def motion_jobs(self):
        if self.accepted is None:
            return super().motion_jobs()
        raw = b.trusted('/opt/baarcha/motion-studio/worker.env')
        need(b.sha(raw) == self.plan['files']['/opt/baarcha/motion-studio/worker.env'], 'Motion credential configuration changed')
        key = m.motion_environment_key(raw); identity = self.plan['motion']
        need(x.ticks(identity['worker_pid']) == identity['worker_start_time'], 'accepted Motion generation changed')
        env = dict(row.split(b'=', 1) for row in Path('/proc/' + str(identity['worker_pid']) + '/environ').read_bytes().split(b'\0') if b'=' in row)
        need(not env.get(b'STUDIO_WORKER_URL') and env.get(b'STUDIO_WORKER_KEY') == key and
             env.get(b'STUDIO_WORKER_SOCKET') == str(self.release.SOCKET).encode(), 'accepted worker credential/socket differs')
        status, raw = self.release_run.http('/api/projects')
        need(status == 200, 'accepted Motion project observation failed')
        return m.motion_job_summary(b.strict(raw))

    def restore_controller(self):
        self.accepted_release(self.accepted); self.fence()
        need(self.baseline['controller_restart'] == {'Name': 'unless-stopped', 'MaximumRetryCount': 0},
             'unreviewed controller restart policy')
        # Keep the same immutable controller ID/image/environment. Its restarted
        # network namespace needs newly attached management relay containers.
        self.command(['/usr/bin/docker', 'start', self.e['controller_id']], 60)
        self.bridge.compose('up', '-d', '--no-deps', '--no-build', '--pull', 'never', '--force-recreate', *b.SERVICES[1:])
        self.command(['/usr/bin/docker', 'update', '--restart=unless-stopped', self.e['controller_id']])
        self.wait(lambda: self.bridge.recreated(self.controller_environment), 120)
        self.ready_fence()

    def controller_ready(self):
        self.same()
        need(self.bridge.recreated(self.controller_environment) == self.e['controller_id'], 'restored controller/relays differ')
        need(not Path('/var/lib/sandboxd/state/sandboxd.db.worker-stop.json').exists() and
             b.digest(b.STOP) == self.plan['files'][str(b.STOP)], 'worker stop configuration changed during Motion release')
        observation = self.bridge.observe()
        need(observation.get('consistent') is True and observation.get('worker_boot_id') == self.e['worker_boot_id'] and
             observation.get('bindings') == len(self.plan['bindings']), 'Cube bindings changed during Motion release')
        self.bridge.fresh(b.strict(b.trusted(b.GUARD)), 0)

    def ready_fence(self):
        self.controller_ready(); self.accepted_release(self.accepted)
        need(b.strict(b.http('/config/', 2019)) == self.routes['offline'], 'routing reopened early')
        with b.locked(list(self.fds)):
            pass

    def ready_after_reopen(self):
        # Legitimate work can begin once online: do not require the old project
        # content hash or idle queues after reopening admission.
        self.controller_ready()
        need(self.release_run.service()['MainPID'] == str(self.accepted['worker_pid']), 'Motion generation changed after reopen')
        for transport in ('tcp', 'uds'):
            need(self.release_run.http('/api/health', transport)[0] == 200, 'Motion transport health failed after reopen')

    def close_nested(self):
        if self.nested_context is not None:
            self.nested_context.__exit__(None, None, None); self.nested_context = None


def main():
    m.run_cli(Host, Sequence, validate_plan, __doc__)


if __name__ == '__main__':
    main()
