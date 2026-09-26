import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('window', Path(__file__).with_name('window.py'))
w = importlib.util.module_from_spec(spec); spec.loader.exec_module(w)
fixture = w.m.load(Path(__file__).parents[2] / 'worker-lifecycle/test_maintenance.py', 'window_fixture')


def plan():
    value = fixture.plan()
    value.update(kind=w.KIND, files={str(p): 'a'*64 for p in w.FILES})
    return value


class WindowTests(unittest.TestCase):
    def test_exact_release_inputs_are_part_of_the_plan(self):
        value = plan(); w.validate_plan(value)
        for missing in (w.CONFIG, w.INSTALLED, w.RELEASE / 'natural_drain.mjs', w.RELEASE / 'worker-uds-source.tar'):
            changed = copy.deepcopy(value); changed['files'].pop(str(missing))
            with self.subTest(missing=missing), self.assertRaises(w.b.Refused): w.validate_plan(changed)
        value['kind'] = w.p.KIND
        with self.assertRaises(w.b.Refused): w.validate_plan(value)

    def test_every_failure_keeps_completion_unclaimed_and_never_cycles_worker(self):
        for failure in ('preflight', 'drain', 'release_motion', 'accepted_release', 'restore_controller', 'ready_fence', 'reopen', 'close_nested', None):
            with self.subTest(failure=failure):
                h = mock.Mock(); h.e = plan()['expected']; events = []
                if failure: getattr(h, failure).side_effect = w.b.Refused('injected')
                sequence = w.Sequence(h, lambda name, value=None: events.append(name))
                if failure:
                    with self.assertRaises(w.b.Refused): sequence.start(sequence.stop())
                    self.assertNotIn('complete', events)
                    if failure not in ('reopen', 'close_nested'): h.reopen.assert_not_called()
                else:
                    result = sequence.start(sequence.stop())
                    self.assertEqual(result['worker_power_operations'], 0)
                    self.assertFalse(result['full_backup']); self.assertEqual(events[-1], 'complete')
                for method in ('pause', 'supervisor_stop', 'start_worker', 'authorize_start', 'external_stop', 'prepare_stop_config'):
                    getattr(h, method).assert_not_called()
                self.assertLessEqual(h.release_motion.call_count, 1)
                self.assertLessEqual(h.restore_controller.call_count, 1)

    def test_unsuccessful_release_never_adopts_new_process_or_source(self):
        h = w.Host.__new__(w.Host); h.plan = plan(); original = copy.deepcopy(h.plan)
        h.fence = mock.Mock(); h.release_run = mock.Mock(); h.release_run.execute.side_effect = RuntimeError('stop unconfirmed')
        h.accepted = None
        with mock.patch.object(Path, 'exists', return_value=False), self.assertRaises(RuntimeError): h.release_motion()
        self.assertEqual(h.plan, original); self.assertIsNone(h.accepted)
        h.release_run.service.assert_not_called()

    def test_uds_is_accepted_only_after_proved_release_and_exact_credential(self):
        h = w.Host.__new__(w.Host); h.accepted = None
        with mock.patch.object(w.p.Host, 'motion_jobs', return_value={'old': True}) as old:
            self.assertEqual(h.motion_jobs(), {'old': True}); old.assert_called_once()
        key = b'fixture-credential'; raw = b'STUDIO_WORKER_KEY=' + key + b'\n'
        h.accepted = {'verified': True}; h.plan = plan()
        h.plan['files']['/opt/baarcha/motion-studio/worker.env'] = w.b.sha(raw)
        h.release = mock.Mock(); h.release.SOCKET = Path('/run/baarcha-motion-studio/worker.sock')
        h.release_run = mock.Mock(); h.release_run.http.return_value = (200, b'{"projects":[]}')
        environment = b'STUDIO_WORKER_KEY=' + key + b'\0STUDIO_WORKER_SOCKET=' + str(h.release.SOCKET).encode() + b'\0'
        with mock.patch.object(w.b, 'trusted', return_value=raw), mock.patch.object(w.x, 'ticks', return_value=h.plan['motion']['worker_start_time']):
            with mock.patch.object(Path, 'read_bytes', return_value=environment):
                self.assertEqual(h.motion_jobs()['active_jobs'], 0)
            for changed in (environment.replace(key, b'other'), environment.replace(b'worker.sock', b'other.sock'), environment + b'STUDIO_WORKER_URL=http://other\0'):
                with mock.patch.object(Path, 'read_bytes', return_value=changed), self.assertRaises(w.b.Refused): h.motion_jobs()

    def test_restart_preserves_controller_id_and_rejoins_relays_once(self):
        h = w.Host.__new__(w.Host); h.e = plan()['expected']; h.accepted = {}; h.controller_environment = {'opaque': 'unchanged'}
        h.baseline = {'controller_restart': {'Name': 'unless-stopped', 'MaximumRetryCount': 0}}
        calls = mock.Mock(); h.command = calls.command; h.bridge = calls.bridge
        h.accepted_release = mock.Mock(); h.fence = mock.Mock(); h.ready_fence = mock.Mock()
        h.wait = lambda action, timeout: action()
        h.restore_controller()
        self.assertEqual(calls.method_calls[0], mock.call.command(['/usr/bin/docker', 'start', h.e['controller_id']], 60))
        self.assertEqual(calls.method_calls[1], mock.call.bridge.compose('up', '-d', '--no-deps', '--no-build', '--pull', 'never', '--force-recreate', *w.b.SERVICES[1:]))
        self.assertEqual(calls.method_calls[2], mock.call.command(['/usr/bin/docker', 'update', '--restart=unless-stopped', h.e['controller_id']]))
        h.bridge.recreated.assert_called_once_with(h.controller_environment); h.ready_fence.assert_called_once()

    def test_ambiguous_relay_start_is_not_replayed_or_reported_ready(self):
        h = w.Host.__new__(w.Host); h.e = plan()['expected']; h.accepted = {}
        h.baseline = {'controller_restart': {'Name': 'unless-stopped', 'MaximumRetryCount': 0}}
        h.command = mock.Mock(); h.bridge = mock.Mock(); h.bridge.compose.side_effect = RuntimeError('ambiguous')
        h.accepted_release = mock.Mock(); h.fence = mock.Mock(); h.ready_fence = mock.Mock()
        with self.assertRaises(RuntimeError): h.restore_controller()
        self.assertEqual(h.command.call_count, 1); self.assertEqual(h.bridge.compose.call_count, 1)
        h.ready_fence.assert_not_called()

    def test_reopen_allows_new_legitimate_project_activity(self):
        h = w.Host.__new__(w.Host); h.controller_ready = mock.Mock(); h.accepted = {'worker_pid': 100}
        h.release_run = mock.Mock(); h.release_run.service.return_value = {'MainPID': '100'}
        h.release_run.http.return_value = (200, b'{}'); h.release_run.projects.side_effect = AssertionError('old content must not be frozen after reopen')
        h.ready_after_reopen()
        self.assertEqual(h.release_run.http.call_args_list, [mock.call('/api/health', 'tcp'), mock.call('/api/health', 'uds')])
        h.release_run.projects.assert_not_called()


if __name__ == '__main__':
    unittest.main()
