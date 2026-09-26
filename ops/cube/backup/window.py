#!/usr/bin/env python3
"""Fenced full-pair capture using normal worker shutdown, then restore service.

The existing maintenance CLI owns all four operation locks and retains them on
failure. This captures a recovery point; encryption and independent restore are
separate. No global migration acceptance is implied.
"""
import copy
import importlib.util
import json
from pathlib import Path
import stat

source = (Path(__file__).parents[1] / 'worker-lifecycle/planned.py' if Path(__file__).name == 'window.py'
          else Path('/usr/local/libexec/baarcha-cube-planned.py'))
spec = importlib.util.spec_from_file_location('backup_planned', source)
p = importlib.util.module_from_spec(spec); spec.loader.exec_module(p)
m, b, x, need = p.m, p.b, p.x, p.need
INSTALLED = Path('/usr/local/libexec/baarcha-cube-backup-window.py')
TOOLS = Path('/opt/baarcha-cube/backup-tools')
CONFIG = TOOLS / 'window-config.PRIVATE.json'
MOTION = Path('/opt/baarcha-cube/motion-release-20260926')
FILES = (*p.FILES, INSTALLED, CONFIG, *(TOOLS / v for v in ('capture_roles.py', 'finalize_roles.py', 'cold_pair.py')),
         *(MOTION / v for v in ('release.py', 'natural_stop.py', 'natural_drain.mjs')))
KIND = 'current-generation-full-pair-capture'


def validate_plan(plan):
    m.validate_plan(plan, kind=KIND, files=FILES)


def stopped_container(value, expected):
    need(value['Id'] == expected['container_id'] and value['Image'] == expected['image'], 'Docker source generation changed')
    s = value['State']
    need(not s['Running'] and not s['Restarting'] and not s['Paused'] and s['Pid'] == 0 and
         value['HostConfig']['RestartPolicy']['Name'] == 'no', 'Docker source remains writable or restartable')


def final_roles(before, start, digest):
    """Select only the genuine newly installed start receipt; preserve heavy roles."""
    after = copy.deepcopy(before)
    after['reviewed_files'][str(b.START)] = digest(b.START)
    for name in ('pause_proof', 'clean_receipt'):
        path = start[name]
        need(path not in after['reviewed_files'] and path not in after['role_paths']['worker-config'], 'fresh stop evidence required')
        after['reviewed_files'][path] = digest(path)
        after['role_paths']['worker-config'].append(path)
    return after


class Host(p.Host):
    def __init__(self, *args):
        super().__init__(*args)
        self.backup = b.strict(b.trusted(CONFIG))
        need(set(self.backup) == {'version', 'roles', 'motion', 'docker_original'} and self.backup['version'] == 1,
             'exact backup window configuration required')
        self.roles = copy.deepcopy(self.backup['roles'])
        self.r = b.load_module(MOTION / 'release.py')
        parent = self
        class Motion(self.r.Release):
            def fence(inner): parent.motion_fence()
        self.motion_stage = self.job / 'motion-drain'
        self.motion = Motion(self.backup['motion'], self.motion_stage, {}, b'')
        self.motion.key = m.motion_environment_key(b.trusted(self.r.ROOT / 'worker.env')).decode()
        self.motion_closed = False
        self.capture_result = None

    def validate_plan(self): validate_plan(self.plan)

    def motion_source(self):
        c = self.motion.c
        for name, expected in c['source_before'].items():
            need(self.r.snapshot(self.r.APP / name) == expected, 'reviewed Motion source generation changed')
        need(self.r.digest(self.r.UNITFILE) == c['unit_sha256'] and
             self.r.digest(self.r.ROOT / 'worker.env') == c['env_sha256'], 'Motion base config changed')
        need({v.name: self.r.digest(v) for v in self.r.DROP.parent.glob('*.conf')} == c['dropins'], 'Motion drop-ins changed')

    def motion_jobs(self):
        self.motion_source()
        identity = self.plan['motion']; state = self.motion.service()
        need(state['MainPID'] == str(identity['worker_pid']) and x.ticks(identity['worker_pid']) == identity['worker_start_time'],
             'reviewed Motion process changed')
        need(all(state.get(k) == v for k, v in self.motion.c['service_properties'].items()), 'Motion service contract changed')
        env = dict(v.split(b'=', 1) for v in Path('/proc/' + state['MainPID'] + '/environ').read_bytes().split(b'\0') if b'=' in v)
        need(not env.get(b'STUDIO_WORKER_URL') and env.get(b'STUDIO_WORKER_SOCKET') == str(self.r.SOCKET).encode() and
             env.get(b'STUDIO_WORKER_KEY') == self.motion.key.encode(), 'Motion transport/key differs')
        socket = self.r.SOCKET.lstat(); parent = self.r.SOCKET.parent.lstat()
        need(stat.S_ISSOCK(socket.st_mode) and (socket.st_uid, socket.st_gid, stat.S_IMODE(socket.st_mode)) == (985, 980, 0o660) and
             stat.S_ISDIR(parent.st_mode) and (parent.st_uid, parent.st_gid, stat.S_IMODE(parent.st_mode)) == (985, 980, 0o750), 'Motion socket DAC changed')
        values = []
        for transport in ('tcp', 'uds'):
            status, raw = self.motion.http('/api/projects', transport)
            need(status == 200, 'authenticated Motion read unavailable')
            values.append(m.motion_job_summary(b.strict(raw)))
        need(values[0] == values[1], 'Motion transports do not expose the same state')
        return values[0]

    def preflight(self, defer_busy=False):
        result = super().preflight(defer_busy=defer_busy)
        c = self.roles
        need(c['controller_id'] == self.e['controller_id'] and c['controller_image'] == self.e['controller_image'] and
             c['reviewed_caddy_sha256'] == self.r.canonical(self.routes['offline']), 'role closure scope differs')
        need(self.r.canonical(self.cp()['Config']['Env']) == c['controller_env_sha256'], 'controller recovery config changed')
        self.command(['/usr/bin/python3', str(TOOLS / 'capture_roles.py'), 'plan', '--config', str(self.write_roles('roles-initial.json', c))])
        # Hash every reviewed role input before fencing. Source-owned Motion files
        # are separately pinned with their reviewed UID layout above.
        for name, expected in c['reviewed_files'].items(): need(b.digest(name) == expected, 'recovery configuration drift')
        need(self.r.digest(c['image_archive']['path']) == c['image_archive']['sha256'], 'recovery image export changed')
        need(set(self.backup['docker_original']) == {v['container_id'] for v in c['docker_homes']}, 'Docker writer inventory incomplete')
        for row in c['docker_homes']:
            value = self.inspect(row['container_id']); original = self.backup['docker_original'][row['container_id']]
            need(value['Image'] == row['image'] == original['image'] and value['State']['Running'] == original['running'] and
                 value['HostConfig']['RestartPolicy'] == original['restart'] and not value['State']['Paused'] and not value['State']['Restarting'],
                 'Docker state changed; refresh the read-only window baseline')
            mounts = value['Mounts']
            need(len(mounts) == 1 and mounts[0]['Destination'] == '/home/sandbox' and mounts[0]['Type'] == 'bind' and mounts[0]['Source'] == row['source'], 'Docker home mapping differs')
            need(original['restart']['Name'] in ('no', 'always', 'unless-stopped') and original['restart'].get('MaximumRetryCount', 0) == 0, 'unreviewed Docker restart policy')
        inventory_code = "import sys,sqlite3,json;sys.path.insert(0," + repr(str(TOOLS)) + ");import capture_roles as r;db=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True);db.execute('BEGIN');print(json.dumps(r.database_inventory(db)));db.close()"
        observed = b.strict(self.command(['/usr/bin/python3', '-c', inventory_code]))
        need(observed == c['inventory'], 'canonical recovery inventory changed before drain')
        need(self.motion.c['service_uid'] == 985 and self.motion.c['service_gid'] == 980, 'reviewed Motion UID/GID required')
        self.motion.natural_module()
        self.motion.projects('tcp'); self.motion.projects('uds')
        self.motion_stage.mkdir(mode=0o700)
        return result

    def write_roles(self, name, config):
        path = self.job / name; x.publish(path, config); return path

    def motion_fence(self):
        need(Path('/proc/sys/kernel/random/boot_id').read_text().strip() == self.e['outer_boot_id'], 'outer boot changed')
        self.cp(True); self.scheduled_writers_inactive()
        proxy = self.inspect(self.plan['motion']['proxy_id'])
        need(proxy['Image'] == self.plan['motion']['proxy_image'] and not proxy['State']['Running'] and
             proxy['HostConfig']['RestartPolicy']['Name'] == 'no', 'Motion proxy unfenced')
        need(b.strict(b.http('/config/', 2019)) == self.routes['offline'], 'Motion routing fence changed')
        need(not self.command(['/usr/bin/ss', '-Htn', 'state', 'established', '( sport = :8332 or dport = :8332 )']).strip(), 'Motion TCP requests remain')
        with b.locked(list(self.fds)): pass

    def motion_stopped(self):
        need(self.motion_closed and self.motion.closed_identity is not None, 'this caller has no natural Motion exit proof')
        state = self.motion.service()
        need(state['ActiveState'] == 'inactive' and state['MainPID'] == '0' and state['ControlPID'] == '0' and
             self.motion.closed_generation(state) == self.motion.closed_identity, 'closed Motion generation changed')
        need(not Path('/proc/' + str(self.plan['motion']['worker_pid'])).exists(), 'old Motion process exists')
        self.motion_source()

    def writers(self):
        if not self.motion_closed: return super().writers()
        self.motion_stopped(); self.motion_fence()
        return {'motion_host_library_quiescent': True, 'natural_exit_zero': True, 'source_and_configuration_verified': True}

    def docker_stopped(self):
        for row in self.roles['docker_homes']: stopped_container(self.inspect(row['container_id']), row)

    def capture_before(self):
        self.fence(); self.quiet_tasks()
        self.event('motion-natural-stop-intent'); self.motion.stop()
        self.motion_closed = True; self.motion_stopped(); self.event('motion-naturally-stopped')
        # The existing drain already stopped the Motion proxy. Never wake an
        # originally stopped container to stop it again, nor retry a failed stop.
        for row in self.roles['docker_homes']:
            value = self.inspect(row['container_id']); original = self.backup['docker_original'][row['container_id']]
            need(value['Image'] == row['image'] and not value['State']['Paused'] and not value['State']['Restarting'], 'Docker generation changed before stop')
            need(original['running'] or not value['State']['Running'], 'originally stopped Docker source woke')
            if value['HostConfig']['RestartPolicy']['Name'] != 'no':
                self.command(['/usr/bin/docker', 'update', '--restart=no', row['container_id']])
            if value['State']['Running']:
                self.event('docker-source-stop-intent', {'container_id': row['container_id']})
                self.command(['/usr/bin/docker', 'stop', '--time=-1', row['container_id']], 180)
            stopped_container(self.inspect(row['container_id']), row)
        self.docker_stopped(); self.fence()
        # prepare_stop_config performed the sole reviewed STOP CAS. The loaded
        # offline file was similarly journaled by drain. Other pins cannot drift.
        for path, expected in self.roles['reviewed_files'].items():
            if path == str(b.STOP): need(b.strict(b.trusted(path)) == self.want_stop, 'unreviewed STOP replacement')
            elif path == str(b.OFFLINE): need(b.strict(b.trusted(path)) == self.routes['offline'], 'offline file drift')
            else: need(b.digest(path) == expected, 'recovery input changed before closure')
        for path in (b.STOP, b.OFFLINE):
            if str(path) in self.roles['reviewed_files']: self.roles['reviewed_files'][str(path)] = b.digest(path)
        config = self.write_roles('roles-before.json', self.roles)
        self.closed_roles = self.job / 'closed-roles'
        self.event('heavy-role-closure-intent')
        self.command(['/usr/bin/python3', str(TOOLS / 'capture_roles.py'), 'capture-frozen', '--config', str(config), '--output', str(self.closed_roles),
                      '--inherited-lock-fds', ','.join(map(str, self.fds[:3]))], 3600)
        self.closed_sha = b.digest(self.closed_roles / 'complete.json')
        self.docker_stopped(); self.fence(); self.event('heavy-roles-closed', {'complete_sha256': self.closed_sha})

    def authorize_start(self, receipt):
        super().authorize_start(receipt)
        self.docker_stopped(); self.motion_stopped()
        final = final_roles(self.roles, b.strict(b.trusted(b.START)), b.digest)
        config = self.write_roles('roles-final.json', final); directory = self.job / 'final-roles'
        self.event('configuration-role-finalization-intent')
        self.command(['/usr/bin/python3', str(TOOLS / 'finalize_roles.py'), '--config', str(config), '--closed-roles', str(self.closed_roles),
                      '--closed-complete-sha256', self.closed_sha, '--output', str(directory), '--inherited-lock-fds', ','.join(map(str, self.fds[:3]))], 600)
        complete = b.strict(b.trusted(directory / 'complete.json'))
        artifacts = {name: value['path'] for name, value in complete['roles'].items()}
        artifacts.update({'controller-key': '/var/lib/sandboxd/secrets.key', 'pause-receipt': complete['pause-receipt']['path']})
        capture = {'root_disk': str(m.ROOT / 'root.qcow2'), 'data_disk': '/mnt/nvme/baarcha-cube/worker-01/data.qcow2',
                   'database': '/var/lib/sandboxd/state/sandboxd.db', 'worker_unit': p.UNIT, 'worker_lock': str(m.ROOT / 'backup.lock'), 'artifacts': artifacts}
        config = self.write_roles('capture.json', capture); self.pair = self.job / 'cold-pair'
        self.event('cold-pair-capture-intent')
        self.command(['/usr/bin/python3', str(TOOLS / 'cold_pair.py'), 'capture', '--config', str(config), '--output', str(self.pair)], 7200)
        self.capture_result = {'version': 1, 'captured': True, 'manifest_sha256': b.digest(self.pair / 'manifest.json'),
                               'path': str(self.pair), 'offhost_verified': False, 'application_restore_verified': False}
        x.publish(self.job / 'capture-result.json', self.capture_result)
        self.event('cold-pair-captured', self.capture_result)
        self.stopped_fence(receipt); self.docker_stopped()

    def transition(self):
        # The original controller is still stopped here. Restart/verify Motion
        # before the boot transition replaces that controller identity.
        self.motion_stopped(); self.motion_fence()
        self.motion.sources = {name: (self.r.APP / name).read_bytes() for name in self.r.FILES}
        self.event('motion-restart-intent'); self.motion.start_verify(True)
        state = self.motion.service(); pid = int(state['MainPID'])
        need(pid > 1 and pid != self.plan['motion']['worker_pid'], 'fresh Motion generation required')
        self.plan = copy.deepcopy(self.plan)
        self.plan['motion'].update(worker_pid=pid, worker_start_time=x.ticks(pid))
        self.motion_closed = False; self.motion_jobs()
        self.event('motion-ready', {'pid': pid, 'invocation': state['InvocationID']})
        return super().transition()

    def capture_after(self):
        need(self.capture_result is not None, 'cold pair not captured')
        self.event('recovery-protection-incomplete', self.capture_result)

    def reopen(self):
        self.ready_fence(); self.docker_stopped()
        for row in self.roles['docker_homes']:
            if row['container_id'] == self.plan['motion']['proxy_id']: continue
            original = self.backup['docker_original'][row['container_id']]
            self.command(['/usr/bin/docker', 'update', '--restart=' + original['restart']['Name'], row['container_id']])
            if original['running']: self.command(['/usr/bin/docker', 'start', row['container_id']], 60)
            value = self.inspect(row['container_id'])
            need(value['State']['Running'] == original['running'] and value['HostConfig']['RestartPolicy'] == original['restart'], 'Docker state restoration differs')
        super().reopen()


class Sequence(p.Sequence):
    def start(self, receipt):
        result = super().start(receipt)
        return {**result, 'pair_capture': self.host.capture_result, 'full_recovery_verified': False}


def main(): m.run_cli(Host, Sequence, validate_plan, __doc__)

if __name__ == '__main__': main()
