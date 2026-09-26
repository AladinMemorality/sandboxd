import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('planned', Path(__file__).with_name('planned.py'))
p = importlib.util.module_from_spec(spec)
spec.loader.exec_module(p)
fixture = p.m.load(Path(__file__).with_name('test_maintenance.py'), 'planned_fixture')


def plan():
    value = fixture.plan()
    value.update(kind=p.KIND, files={str(path): 'a'*64 for path in p.FILES})
    return value


def drain():
    # Retained native Go receipt from the completed owned-worker recovery.
    return dict(version=1, generated_at='2026-09-26T13:35:17.515098+00:00',
                controller_id='76d8ce7041e4b9c86b75bd676ada1500db0167d63025f81a05f24e0e1a8f04c7',
                worker_boot_id='c1b590df-28d3-4222-b9a3-c0408c8ccc07', qemu_pid=3026420,
                qemu_start_time='483403245',
                inventory_sha256='bbd8635e50fb6ae00701c2ff673f932c56faab1955960f33c8346ca7a9eea240',
                caddy_configuration_sha256='722424580f4ce3459d90ac758331b264d3b208096ea2c0cd36c744e945c914fa',
                evidence_sha256='f633c81b3ceeff84c9836dffa1012cf8ff033ae41fff09fe295c9faa9d7cf664',
                traffic_fenced=True, existing_requests_drained=True, direct_writers_fenced=True,
                provider_jobs_drained=True)


def clean():
    expected = plan()['expected']
    expected.update(qemu_pid=2147483000, supervisor_pid=2147483001)
    inventory = {'SHA256': 'c'*64, 'Bindings': [{'RuntimeID': 'provider'}]}
    proof = {key: expected[key] for key in ('qemu_pid', 'qemu_start_time', 'worker_boot_id', 'worker_machine_id', 'data_uuid')}
    proof.update(version=1, verified=True, provider_jobs=0, inventory_sha256=inventory['SHA256'],
                 receipt_sha256=p.drain_hash(drain()), guest_states={'provider': 'paused'},
                 generated_at='2026-09-26T13:35:18Z')
    receipt = dict(version=1, state='stopped-clean', generated_at=1790429719, proof=proof)
    status = dict(state='stopped-clean', qemu_pid=expected['qemu_pid'], supervisor_pid=expected['supervisor_pid'])
    return expected, inventory, receipt, status


def receipt_path(receipt):
    digest = p.b.sha(json.dumps(receipt['proof'], sort_keys=True, separators=(',', ':')).encode())
    return p.m.ROOT / ('clean-stop-' + digest + '.json')


class PlannedTests(unittest.TestCase):
    def test_incident_and_planned_schemas_remain_separate(self):
        p.validate_plan(plan())
        with self.assertRaises(p.b.Refused): p.m.validate_plan(plan())
        with self.assertRaises(p.b.Refused): p.validate_plan(fixture.plan())
        value = plan(); value['files'].pop(str(p.INSTALLED))
        with self.assertRaises(p.b.Refused): p.validate_plan(value)

    def test_hash_matches_real_native_go_proof_and_normalizes_time(self):
        value = drain()
        self.assertEqual(p.drain_hash(value), '3e383bbd322488b21bd5d7d45970814d30f67405eccca37558819340c9bf497c')
        value['generated_at'] = '2026-09-26T13:35:17.515098000Z'
        self.assertEqual(p.drain_hash(value), p.drain_hash(drain()))
        value['generated_at'] = '2026-09-26T13:35:17.000000+00:00'
        expected = copy.deepcopy(value); expected['generated_at'] = '2026-09-26T13:35:17Z'
        self.assertEqual(p.drain_hash(value), p.drain_hash(expected))
        self.assertNotEqual(p.drain_hash(value), p.b.sha(p.b.encoded(value)))

    def test_receipt_requires_exact_actual_generation_inventory_and_normal_exit(self):
        e, inventory, receipt, status = clean()
        p.validate_clean(receipt, receipt_path(receipt), e, inventory, status, now=receipt['generated_at'])
        for fault in ('external', 'qemu_pid', 'qemu_start_time', 'worker_boot_id', 'data_uuid',
                      'unpaused', 'missing', 'extra', 'inventory', 'pending', 'future', 'status', 'path'):
            with self.subTest(fault=fault):
                r, s = copy.deepcopy(receipt), copy.deepcopy(status)
                if fault == 'external': r['state'] = 'externally-stopped-clean'
                elif fault in ('qemu_pid', 'qemu_start_time', 'worker_boot_id', 'data_uuid'): r['proof'][fault] = 'other'
                elif fault == 'unpaused': r['proof']['guest_states']['provider'] = 'running'
                elif fault == 'missing': r['proof']['guest_states'] = {}
                elif fault == 'extra': r['proof']['guest_states']['unknown'] = 'paused'
                elif fault == 'inventory': r['proof']['inventory_sha256'] = 'd'*64
                elif fault == 'pending': r['proof']['provider_jobs'] = 1
                elif fault == 'future': r['generated_at'] += 120
                elif fault == 'status': s['state'] = 'worker-lost'
                path = receipt_path(r) if fault != 'path' else p.m.ROOT / 'invented.json'
                with self.assertRaises(p.b.Refused): p.validate_clean(r, path, e, inventory, s, now=receipt['generated_at'])

    def test_failure_never_retries_stop_uses_external_recovery_or_reopens(self):
        for fail in ('predrain', 'supervisor_stop', 'authorize_start', 'start_worker', 'transition', 'ready_fence', None):
            h = mock.Mock()
            h.transition.return_value = {'tenant_ready': True, 'routing_changed': False}
            if fail: getattr(h, fail).side_effect = p.b.Refused('injected')
            sequence = p.Sequence(h, mock.Mock())
            if fail:
                with self.assertRaises(p.b.Refused): sequence.start(sequence.stop())
                h.reopen.assert_not_called()
            else: sequence.start(sequence.stop()); h.reopen.assert_called_once()
            self.assertLessEqual(h.supervisor_stop.call_count, 1)
            h.external_stop.assert_not_called(); h.retained_stop.assert_not_called(); h.pause.assert_not_called()

    def test_actual_stop_call_waits_for_service_exit_and_uses_immutable_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); job = root / 'job'; job.mkdir()
            e, inventory, receipt, status = clean()
            h = p.Host.__new__(p.Host); h.e = e; h.job = job; h.fence = mock.Mock(); h.command = mock.Mock()
            h.nested_context = mock.MagicMock()
            h.worker_unit = mock.Mock(side_effect=[{'ActiveState': 'deactivating', 'MainPID': str(e['supervisor_pid'])}, {'ActiveState': 'inactive', 'MainPID': '0', 'ExecMainStatus': '0'}])
            with mock.patch.object(p.m, 'ROOT', root), mock.patch.object(p.b, 'trusted', side_effect=lambda path: Path(path).read_bytes()), mock.patch.object(p.x, 'publish', side_effect=lambda path, value: Path(path).write_bytes(p.b.encoded(value))), mock.patch.object(p.time, 'sleep'):
                path = receipt_path(receipt); path.write_bytes(p.b.encoded(receipt))
                (root / 'lifecycle-status.json').write_bytes(p.b.encoded(status))
                (job / 'pre-drain.json').write_bytes(p.b.encoded(drain()))
                self.assertEqual(h.supervisor_stop(inventory), receipt)
                self.assertEqual(json.loads((job / 'pause.json').read_bytes()), receipt['proof'])
                self.assertEqual(h.clean_path, path); self.assertIsNone(h.nested_context)
            h.command.assert_called_once_with(['/usr/bin/systemctl', 'stop', '--no-block', p.UNIT])

    def test_stop_blocked_never_creates_clean_evidence(self):
        h = p.Host.__new__(p.Host); h.e = clean()[0]; h.fence = mock.Mock(); h.command = mock.Mock(); h.worker_unit = mock.Mock()
        status = dict(clean()[3], state='stop-blocked')
        with mock.patch.object(p.b, 'trusted', return_value=p.b.encoded(status)), mock.patch.object(p.x, 'publish') as publish:
            with self.assertRaises(p.b.Refused): h.supervisor_stop(clean()[1])
            publish.assert_not_called(); h.worker_unit.assert_not_called()
        self.assertEqual(h.command.call_count, 1)


if __name__ == '__main__':
    unittest.main()
