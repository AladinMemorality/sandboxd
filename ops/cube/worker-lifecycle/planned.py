#!/usr/bin/env python3
"""Planned nonempty worker cycle using the supervisor's own clean-stop receipt.

Shares the reviewed traffic/task fence and journal with incident maintenance.
Never changes a lifecycle status, invokes external power recovery, or forces a
process down. Failure after fencing retains the parent locks for reconciliation.
"""
import datetime
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import shlex
import stat
import struct
import time

source = (Path(__file__).with_name('maintenance.py') if Path(__file__).name == 'planned.py'
          else Path('/usr/local/libexec/baarcha-cube-maintenance.py'))
spec = importlib.util.spec_from_file_location('planned_maintenance', source)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
b, x, need = m.b, m.x, m.need
INSTALLED = Path('/usr/local/libexec/baarcha-cube-planned.py')
FILES = (*m.FILES, INSTALLED)
KIND = 'current-generation-planned-cycle'
UNIT = 'baarcha-cube-worker-01.service'


def validate_plan(plan):
    m.validate_plan(plan, kind=KIND, files=FILES)


def drain_hash(receipt):
    # workerstop.hash marshals the concrete Go DrainReceipt, including field
    # order and time.Time's RFC3339Nano normalization, not the input file bytes.
    keys = ('version', 'generated_at', 'controller_id', 'worker_boot_id', 'qemu_pid',
            'qemu_start_time', 'inventory_sha256', 'caddy_configuration_sha256',
            'evidence_sha256', 'traffic_fenced', 'existing_requests_drained',
            'direct_writers_fenced', 'provider_jobs_drained')
    need(set(receipt) == set(keys), 'exact native drain receipt schema required')
    stamp = re.fullmatch(r'(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d{1,9})?(?:Z|\+00:00)', receipt['generated_at'])
    need(stamp is not None, 'UTC native drain timestamp required')
    value = {key: receipt[key] for key in keys}
    value['generated_at'] = stamp[1] + (stamp[2] or '').rstrip('0').rstrip('.') + 'Z'
    raw = json.dumps(value, separators=(',', ':'), ensure_ascii=False, allow_nan=False)
    return b.sha(raw.replace('&', '\\u0026').replace('<', '\\u003c').replace('>', '\\u003e').replace('\u2028', '\\u2028').replace('\u2029', '\\u2029').encode())


def validate_clean(receipt, path, expected, inventory, status, now=None):
    """Accept only the supervisor's immutable receipt for this actual stop."""
    now = time.time() if now is None else now
    need(set(receipt) == {'version', 'state', 'generated_at', 'proof'} and
         receipt['version'] == 1 and receipt['state'] == 'stopped-clean',
         'normal supervisor receipt required; external recovery is separate')
    proof = receipt['proof']
    need(isinstance(proof, dict) and proof.get('version') == 1 and
         proof.get('verified') is True and proof.get('provider_jobs') == 0,
         'complete native pause proof required')
    for key in ('qemu_pid', 'qemu_start_time', 'worker_boot_id', 'worker_machine_id', 'data_uuid'):
        need(proof.get(key) == expected[key], 'clean receipt belongs to another worker generation')
    need(proof.get('inventory_sha256') == inventory['SHA256'] and
         b.SHA.fullmatch(proof.get('receipt_sha256', '')),
         'clean receipt inventory or drain hash differs')
    ids = [row['RuntimeID'] for row in inventory['Bindings']]
    need(ids and len(ids) == len(set(ids)) and proof.get('guest_states') == dict.fromkeys(ids, 'paused'),
         'clean receipt must cover every exact retained guest')
    generated = datetime.datetime.fromisoformat(proof['generated_at'].replace('Z', '+00:00')).timestamp()
    need(0 < generated <= receipt['generated_at'] <= now + 1, 'invalid supervisor receipt chronology')
    digest = hashlib.sha256(json.dumps(proof, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    need(Path(path) == m.ROOT / ('clean-stop-' + digest + '.json'), 'unexpected supervisor receipt path')
    need(status.get('state') == 'stopped-clean' and status.get('qemu_pid') == expected['qemu_pid'] and
         status.get('supervisor_pid') == expected['supervisor_pid'], 'actual supervisor has not reported clean exit')
    return proof


class Sequence(m.Sequence):
    def stop(self):
        h = self.host
        h.preflight(); self.event('preflight-passed')
        self.event('drain-intent'); h.drain()
        self.event('drained'); h.prepare_stop_config(); h.capture_before()
        self.event('before-role-closure'); h.fence()
        inventory = h.predrain()
        self.event('supervisor-stop-intent')
        receipt = h.supervisor_stop(inventory)
        self.event('supervisor-stopped-clean', receipt)
        return receipt


class Host(m.Host):
    source_state = 'running-unreconciled'

    def validate_plan(self):
        validate_plan(self.plan)

    def worker_unit(self):
        fields = ('ActiveState', 'SubState', 'MainPID', 'ExecMainStatus', 'KillMode',
                  'SendSIGKILL', 'TimeoutStopUSec', 'Restart', 'ExecStop', 'Job')
        raw = self.command(['/usr/bin/systemctl', 'show', UNIT, '--property=' + ','.join(fields)])
        return dict(line.split('=', 1) for line in raw.decode().splitlines() if '=' in line)

    def preflight(self, defer_busy=False):
        result = super().preflight(defer_busy=defer_busy)
        unit = self.worker_unit()
        need(unit.get('ActiveState') == 'active' and unit.get('SubState') == 'running' and
             unit.get('MainPID') == str(self.e['supervisor_pid']) and
             unit.get('KillMode') == 'process' and unit.get('SendSIGKILL') == 'no' and
             unit.get('TimeoutStopUSec') == 'infinity' and unit.get('Restart') == 'no' and
             not unit.get('ExecStop') and unit.get('Job', '') in ('', '0'),
             'planned stop requires the exact non-forcing supervisor unit')
        observation = self.bridge.observe()
        need(observation.get('consistent') is True and
             observation.get('bindings') == len(self.plan['bindings']) and
             observation.get('worker_boot_id') == self.e['worker_boot_id'],
             'running supervisor lacks reconciled tenant binding evidence')
        need(b.http('/readyz', 9090).strip() == b'ready', 'current controller is not ready')
        self.bridge.fresh(b.strict(b.trusted(b.GUARD)), 0)
        return result

    def supervisor_stop(self, inventory):
        self.fence()
        # Exactly one normal service stop. The running supervisor owns native
        # pause, nested component shutdown, QMP powerdown and waitpid evidence.
        self.command(['/usr/bin/systemctl', 'stop', '--no-block', UNIT])
        until = time.monotonic() + 900
        while True:
            status = b.strict(b.trusted(m.ROOT / 'lifecycle-status.json'))
            need(status.get('qemu_pid') == self.e['qemu_pid'] and
                 status.get('supervisor_pid') == self.e['supervisor_pid'], 'supervisor generation changed during stop')
            need(status.get('state') not in ('stop-blocked', 'worker-lost'),
                 'normal stop failed; retain actual state and operator fences')
            if status.get('state') == 'stopped-clean':
                unit = self.worker_unit()
                if unit.get('ActiveState') == 'inactive' and unit.get('MainPID') == '0':
                    need(unit.get('ExecMainStatus') == '0', 'supervisor did not exit successfully')
                    break
            need(time.monotonic() < until, 'normal stop remains pending; no forced fallback')
            time.sleep(1)
        need(all(not Path('/proc/' + str(self.e[k])).exists() for k in ('qemu_pid', 'supervisor_pid')),
             'old worker process remains after service stop')
        candidates = []
        for path in m.ROOT.glob('clean-stop-*.json'):
            receipt = b.strict(b.trusted(path))
            proof = receipt.get('proof', {})
            if all(proof.get(k) == self.e[k] for k in ('qemu_pid', 'qemu_start_time', 'worker_boot_id')):
                candidates.append((path, receipt))
        need(len(candidates) == 1, 'unambiguous current supervisor receipt required')
        path, receipt = candidates[0]
        proof = validate_clean(receipt, path, self.e, inventory, status)
        need(proof['receipt_sha256'] == drain_hash(b.strict(b.trusted(self.job / 'pre-drain.json'))),
             'supervisor used another drain receipt')
        x.publish(self.job / 'pause.json', proof)
        x.publish(self.job / 'supervisor-clean.json', receipt)
        x.publish(self.job / 'supervisor-stopped-status.json', status)
        self.clean_path, self.clean_inventory = path, inventory
        if self.nested_context is not None:
            self.nested_context.__exit__(None, None, None)
            self.nested_context = None
        return receipt

    def stopped_fence(self, receipt):
        need(b.strict(b.trusted(self.clean_path)) == receipt, 'supervisor receipt changed')
        status = b.strict(b.trusted(m.ROOT / 'lifecycle-status.json'))
        validate_clean(receipt, self.clean_path, self.e, self.clean_inventory, status)
        need(status == b.strict(b.trusted(self.job / 'supervisor-stopped-status.json')),
             'stopped supervisor record changed')
        self.cp(True); self.writers()
        need(b.strict(b.http('/config/', 2019)) == self.routes['offline'], 'offline routing lost')
        need(Path('/proc/sys/kernel/random/boot_id').read_text().strip() == self.e['outer_boot_id'],
             'host reboot is not this planned cycle')
        need(all(not Path('/proc/' + str(self.e[k])).exists() for k in ('qemu_pid', 'supervisor_pid')),
             'old worker process generation remains')
        need(b.strict(b.trusted(b.STOP)) == self.want_stop, 'STOP changed after pause')
        need(not x.START_AUTH.exists(), 'external one-use authorization must not exist for a normal start')
        with b.locked(list(self.fds)):
            pass

    def authorize_start(self, receipt):
        self.stopped_fence(receipt)
        start = {'version': 1, 'pause_proof': str(self.job / 'pause.json'),
                 'clean_receipt': str(self.clean_path)}
        need(b.sha(b.trusted(b.START)) == self.plan['files'][str(b.START)], 'START changed before CAS')
        x.publish(self.job / 'start-before.json', b.trusted(b.START))
        x.publish(self.job / 'start-wanted.json', start)
        b.atomic(b.START, b.encoded(start))
        plan = {'version': 1, 'outer_machine_id': self.e['outer_machine_id'],
                'files': {str(p): b.digest(p) for p in b.PINNED},
                'initial': {str(p): b.digest(p) for p in (b.STOP, b.GUARD, b.COMPOSE, b.ACTIVE)},
                'controller_image': self.e['controller_image'], 'disk_identity': {}}
        for path in (m.ROOT / 'root.qcow2', Path('/mnt/nvme/baarcha-cube/worker-01/data.qcow2'), m.ROOT / 'seed.img'):
            need(path.resolve(strict=True) == path, 'retained disk path changed')
            info = path.stat()
            need(stat.S_ISREG(info.st_mode) and info.st_uid == 0, 'untrusted retained disk')
            if path.name == 'seed.img':
                size = info.st_size
            else:
                with path.open('rb') as f:
                    header = f.read(32)
                need(len(header) == 32 and header[:4] == b'QFI\xfb', 'standalone qcow2 header required')
                size = struct.unpack('>Q', header[24:32])[0]
            fs = self.command(['/usr/bin/findmnt', '-n', '-o', 'UUID', '--target', str(path)]).decode().strip()
            plan['disk_identity'][path.name] = {'inode': info.st_ino, 'virtual_bytes': size, 'filesystem_uuid': fs}
        b.validate_plan(plan)
        self.transition_plan = plan
        x.publish(self.job / 'transition-plan.json', plan)

    def start_worker(self):
        need(self.worker_unit().get('Job', '') in ('', '0'), 'worker operation appeared')
        need(not x.START_AUTH.exists(), 'normal start cannot consume external recovery authorization')
        self.command(['/usr/bin/systemctl', 'start', UNIT], 30)
        host = b.Host(self.transition_plan)
        def ready():
            generation = host.generation(self.want_stop)
            need(generation['worker_boot_id'] != self.e['worker_boot_id'], 'worker boot did not change')
            args = ['/usr/bin/python3', str(b.LIFECYCLE), 'verify-start',
                    '--machine-id', generation['worker_machine_id'],
                    '--boot-id', generation['worker_boot_id'], '--data-uuid', generation['inner_fs_uuid']]
            result = b.strict(self.command(b.SSH + [' '.join(map(shlex.quote, args))], 70))
            need(result.get('verified') is True and result.get('boot_id') == generation['worker_boot_id'],
                 'retained management services are not ready')
            return generation
        # SSH/storage can be ready before retained management services. Retry
        # only this read-only check before the single native reconciliation.
        self.wait(ready, 240)


def main():
    m.run_cli(Host, Sequence, validate_plan, __doc__)


if __name__ == '__main__':
    main()
