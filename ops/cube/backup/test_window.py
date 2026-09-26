import copy
import importlib.util
from pathlib import Path
import unittest
from unittest import mock
import finalize_roles
import test_finalize_roles

spec = importlib.util.spec_from_file_location('backup_window', Path(__file__).with_name('window.py'))
w = importlib.util.module_from_spec(spec); spec.loader.exec_module(w)
fixture = w.m.load(Path(__file__).parents[1] / 'worker-lifecycle/test_maintenance.py', 'backup_fixture')


def plan():
    value = fixture.plan(); value.update(kind=w.KIND, files={str(p): 'a'*64 for p in w.FILES}); return value


def docker(running=False, policy='no'):
    return {'Id': 'exact', 'Image': 'image', 'State': {'Running': running, 'Restarting': False, 'Paused': False, 'Pid': 10 if running else 0},
            'HostConfig': {'RestartPolicy': {'Name': policy, 'MaximumRetryCount': 0}}}


class BackupWindowTests(unittest.TestCase):
    def test_config_and_all_executed_helpers_are_pinned(self):
        value = plan(); w.validate_plan(value)
        for required in (w.CONFIG, w.INSTALLED, w.TOOLS/'capture_roles.py', w.TOOLS/'finalize_roles.py', w.TOOLS/'cold_pair.py', w.MOTION/'natural_stop.py'):
            changed = copy.deepcopy(value); del changed['files'][str(required)]
            with self.subTest(path=required), self.assertRaises(w.b.Refused): w.validate_plan(changed)
        value['kind'] = w.p.KIND
        with self.assertRaises(w.b.Refused): w.validate_plan(value)

    def test_only_native_new_start_receipts_change_closed_roles(self):
        f = test_finalize_roles.FinalRoleTests(); f.setUp(); self.addCleanup(f.doCleanups)
        digests = {str(w.b.START): '4'*64, f.pause_path: '5'*64, f.clean_path: '6'*64}
        after = w.final_roles(f.before, f.start, lambda path: digests[str(path)])
        self.assertEqual(after, f.after)
        finalize_roles.validate_transition(f.before, after, f.pause_path, f.clean_path)
        self.assertEqual(f.before['reviewed_files'][str(w.b.START)], '3'*64)
        with self.assertRaises(w.b.Refused): w.final_roles(after, f.start, lambda path: digests[str(path)])

    def test_stopped_source_requires_exact_process_absence_and_restart_disabled(self):
        expected = {'container_id': 'exact', 'image': 'image'}
        w.stopped_container(docker(), expected)
        for changed in (docker(True), docker(policy='always'), {**docker(), 'Id': 'replacement'},
                        {**docker(), 'State': {**docker()['State'], 'Pid': 10}},
                        {**docker(), 'State': {**docker()['State'], 'Restarting': True}}):
            with self.assertRaises(w.b.Refused): w.stopped_container(changed, expected)

    def test_refused_natural_exit_cannot_stop_docker_or_capture_roles(self):
        h = w.Host.__new__(w.Host); h.motion_closed = False
        h.fence = mock.Mock(); h.quiet_tasks = mock.Mock(); h.event = mock.Mock(); h.command = mock.Mock()
        h.motion = mock.Mock(); h.motion.stop.side_effect = RuntimeError('natural completion unproved')
        with self.assertRaises(RuntimeError): h.capture_before()
        self.assertFalse(h.motion_closed); h.command.assert_not_called()

    def test_failed_heavy_roles_never_power_down_worker(self):
        h = mock.Mock(); h.capture_before.side_effect = RuntimeError('PostgreSQL control is not clean')
        with self.assertRaises(RuntimeError): w.Sequence(h, mock.Mock()).stop()
        h.supervisor_stop.assert_not_called(); h.predrain.assert_not_called()

    def test_failed_finalization_or_capture_never_starts_worker(self):
        for error in ('finalization failed', 'cold pair compare failed'):
            h = mock.Mock(); h.authorize_start.side_effect = RuntimeError(error)
            with self.assertRaises(RuntimeError): w.Sequence(h, mock.Mock()).start({'native': 'receipt'})
            h.start_worker.assert_not_called(); h.transition.assert_not_called(); h.reopen.assert_not_called()

    def test_no_incident_recovery_or_automatic_retry_in_planned_capture(self):
        h = mock.Mock(); h.transition.return_value = {'tenant_ready': True, 'routing_changed': False}; h.capture_result = {'captured': True}
        sequence = w.Sequence(h, mock.Mock()); result = sequence.start(sequence.stop())
        self.assertTrue(result['pair_capture']['captured']); self.assertFalse(result['full_recovery_verified'])
        self.assertEqual(h.supervisor_stop.call_count, 1); self.assertEqual(h.capture_before.call_count, 1)
        self.assertEqual(h.authorize_start.call_count, 1); self.assertEqual(h.start_worker.call_count, 1)
        h.external_stop.assert_not_called(); h.pause.assert_not_called(); h.retained_stop.assert_not_called()

    def test_reopen_restores_only_original_running_containers(self):
        h = w.Host.__new__(w.Host); h.ready_fence = mock.Mock(); h.docker_stopped = mock.Mock()
        h.plan = {'motion': {'proxy_id': 'proxy'}}
        h.roles = {'docker_homes': [{'container_id': v} for v in ('running', 'stopped', 'proxy')]}
        h.backup = {'docker_original': {v: {'running': v != 'stopped', 'restart': {'Name': 'no', 'MaximumRetryCount': 0}} for v in ('running', 'stopped', 'proxy')}}
        h.command = mock.Mock(); h.inspect = lambda ident: docker(ident == 'running')
        with mock.patch.object(w.p.Host, 'reopen') as reopen: h.reopen(); reopen.assert_called_once()
        self.assertEqual(h.command.call_args_list, [mock.call(['/usr/bin/docker', 'update', '--restart=no', 'running']),
                         mock.call(['/usr/bin/docker', 'start', 'running'], 60), mock.call(['/usr/bin/docker', 'update', '--restart=no', 'stopped'])])

    def test_failed_docker_restore_does_not_reopen_customer_routes(self):
        h = w.Host.__new__(w.Host); h.ready_fence = mock.Mock(); h.docker_stopped = mock.Mock()
        h.plan = {'motion': {'proxy_id': 'proxy'}}; h.roles = {'docker_homes': [{'container_id': 'running'}]}
        h.backup = {'docker_original': {'running': {'running': True, 'restart': {'Name': 'no', 'MaximumRetryCount': 0}}}}
        h.command = mock.Mock(); h.inspect = lambda ident: docker(False)
        with mock.patch.object(w.p.Host, 'reopen') as reopen:
            with self.assertRaises(w.b.Refused): h.reopen()
            reopen.assert_not_called()


if __name__ == '__main__': unittest.main()
