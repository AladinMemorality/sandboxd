import copy
import contextlib
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('cohort', Path(__file__).with_name('cohort.py'))
c = importlib.util.module_from_spec(spec); spec.loader.exec_module(c)


def project():
    return dict(app_id='01M28XBR5M5HVHA4SVD80FTCQC', sandbox_id='01M28XBR5WVEB9B3XWX1CN4NEB',
                preset='react-vite', template_id='tpl-' + 'a' * 24, container_id='b' * 64,
                recorded_container_id='b' * 12, image='sha256:' + 'c' * 64)


def config():
    value = dict(version=1, fleet_sha256='d' * 64, files={}, archives='/mnt/nvme/baarcha-cube/migration-archives', projects=[project()])
    for key in ('cli', 'homes', 'presets', 'resources', 'templates'):
        value[key] = '/root/private/' + key
        value['files'][value[key]] = 'e' * 64
    value['migrations'] = '/root/private/migrations'
    value['files'][value['migrations'] + '/0001.sql'] = 'f' * 64
    return value


def source():
    row = project()
    return dict(Id=row['container_id'], Image=row['image'],
                State=dict(Running=False, Restarting=False, Paused=False, Pid=0, OOMKilled=False, ExitCode=0),
                HostConfig={'RestartPolicy': {'Name': 'no', 'MaximumRetryCount': 0}},
                Mounts=[dict(Type='bind', Destination='/home/sandbox', Source=str(c.WORKSPACES / row['sandbox_id']))])


class CohortTests(unittest.TestCase):
    def test_existing_guest_pause_requires_offline_routes_and_quiet_tasks(self):
        host = c.Host.__new__(c.Host); host.c = dict(config(), version=2, parallelism=4)
        host.routes = {'offline': {'reviewed': True}}
        host.plan = {'bindings': [{'sandbox_id': 'existing'}], 'files': {str(c.PAUSE): 'digest'}, 'provider_terminal_counts': {'t_cube_pause_snapshot': {'READY': 13}}}
        host.job = Path('/private/operation')
        for name in ('quiet_tasks', 'source_fence', 'bindings_readonly', 'event', 'provider'):
            setattr(host, name, mock.Mock())
        host.command = mock.Mock(return_value=json.dumps({'success': True, 'paused': ['existing', project()['sandbox_id']], 'newly_paused_cube': ['existing']}).encode())
        host.bridge = mock.Mock(); host.bridge.observe.return_value = {'consistent': True, 'active': 0}
        with mock.patch.object(c.b, 'http', return_value=b'{}'), mock.patch.object(c.b, 'digest', return_value='digest'), mock.patch.object(c.x, 'publish'):
            with self.assertRaises(c.b.Refused): host.before_controller_stop()
            host.command.assert_not_called()
        with mock.patch.object(c.b, 'http', return_value=b'{"reviewed":true}'), mock.patch.object(c.b, 'digest', return_value='digest'), mock.patch.object(c.x, 'publish'):
            host.quiet_tasks.side_effect = c.b.Refused('customer task active')
            with self.assertRaises(c.b.Refused): host.before_controller_stop()
            host.command.assert_not_called()
            host.quiet_tasks.side_effect = None
            host.before_controller_stop(); host.command.assert_called_once()
            self.assertEqual(host.plan['provider_terminal_counts']['t_cube_pause_snapshot'], {'READY': 14})
            host.provider.assert_called_once()
            host.bridge.observe.return_value = {'consistent': True, 'active': 1}
            with self.assertRaises(c.b.Refused): host.before_controller_stop()

    def test_larger_cohort_keeps_four_parallel_guest_limit(self):
        value = config(); value.update(version=2, parallelism=4)
        value['projects'] = [dict(project(), app_id='0'*24 + '%02d' % i,
                                 sandbox_id='1'*24 + '%02d' % i,
                                 container_id=('%064x' % (i + 1)),
                                 recorded_container_id=('%064x' % (i + 1))) for i in range(12)]
        c.validate_config(value)
        for width in (0, 5, True):
            bad = copy.deepcopy(value); bad['parallelism'] = width
            with self.subTest(width=width), self.assertRaises(c.b.Refused): c.validate_config(bad)
        value['projects'].append(value['projects'][0])
        with self.assertRaises(c.b.Refused): c.validate_config(value)

    def test_partial_parallel_import_never_reconciles_or_advances(self):
        with tempfile.TemporaryDirectory() as directory:
            host = c.Host.__new__(c.Host); host.c = config(); host.job = Path(directory)
            host.fence = mock.Mock(); host.source_fence = mock.Mock(); host.inputs = mock.Mock()
            host.event = mock.Mock(); host.reconcile_template_default = mock.Mock(); host.db = mock.Mock()
            host.run_cli = mock.Mock(return_value={'success': True, 'projects': []})
            with self.assertRaises(c.b.Refused): host.migrate_wave([project()], 0)
            host.run_cli.assert_called_once(); host.reconcile_template_default.assert_not_called(); host.db.assert_not_called()

    def test_explicit_small_ordinary_cohort_only(self):
        c.validate_config(config())
        for kind in ('empty', 'duplicate', 'motion', 'database', 'unpinned', 'archive', 'mutable_image'):
            value = config()
            if kind == 'empty': value['projects'] = []
            elif kind == 'duplicate': value['projects'] *= 2
            elif kind == 'motion': value['projects'][0]['app_id'] = c.MOTION_APP
            elif kind == 'database': value['projects'][0]['preset'] = 'node-postgres'
            elif kind == 'unpinned': value['files'].pop(value['cli'])
            elif kind == 'archive': value['archives'] = '/var/lib/sandboxd/workspaces'
            else: value['projects'][0]['image'] = 'latest'
            with self.subTest(kind=kind), self.assertRaises(c.b.Refused): c.validate_config(value)

    def test_running_or_restartable_customer_source_never_accepted(self):
        c.stopped_source(source(), project())
        for field, value in (('Running', True), ('Restarting', True), ('Paused', True), ('Pid', 42), ('OOMKilled', True), ('ExitCode', 137)):
            row = source(); row['State'][field] = value
            with self.subTest(field=field), self.assertRaises(c.b.Refused): c.stopped_source(row, project())
        row = source(); row['HostConfig']['RestartPolicy']['Name'] = 'unless-stopped'
        with self.assertRaises(c.b.Refused): c.stopped_source(row, project())

    def test_running_source_allowed_only_before_task_aware_drain(self):
        row = source(); row['State'].update(Running=True, Pid=123)
        c.stopped_source(row, project(), allow_running=True)
        with self.assertRaises(c.b.Refused): c.stopped_source(row, project())
        for field, value in (('Restarting', True), ('Paused', True), ('Pid', 0), ('OOMKilled', True)):
            bad = copy.deepcopy(row); bad['State'][field] = value
            with self.subTest(field=field), self.assertRaises(c.b.Refused): c.stopped_source(bad, project(), allow_running=True)

    def test_source_mount_and_image_identity_are_exact(self):
        for kind in ('image', 'id', 'path', 'extra'):
            row = source()
            if kind == 'image': row['Image'] = 'sha256:' + '0' * 64
            elif kind == 'id': row['Id'] = '0' * 64
            elif kind == 'path': row['Mounts'][0]['Source'] = '/tmp/alternate-home'
            else: row['Mounts'].append(dict(Type='bind', Destination='/data', Source='/tmp/data'))
            with self.subTest(kind=kind), self.assertRaises(c.b.Refused): c.stopped_source(row, project())

    def test_legacy_short_database_id_is_pinned_to_exact_inspected_full_id(self):
        value = config(); c.validate_config(value)
        value['projects'][0]['recorded_container_id'] = 'b' * 64; c.validate_config(value)
        for bad in ('b', 'b' * 11, 'a' * 12, 'b' * 13):
            value['projects'][0]['recorded_container_id'] = bad
            with self.subTest(id=bad), self.assertRaises(c.b.Refused): c.validate_config(value)

    def test_binding_requires_complete_journal_matching_target_and_hashes(self):
        row = project()
        with sqlite3.connect(':memory:') as db:
            db.executescript('''CREATE TABLE sandbox(id,app_id,runtime_provider);
              CREATE TABLE runtime_binding(sandbox_id,provider,runtime_id,template_id,config_revision);
              CREATE TABLE runtime_migration(sandbox_id,phase,runtime_id,template_id,archive_sha256,home_sha256);''')
            db.execute('INSERT INTO sandbox VALUES(?,?,?)', (row['sandbox_id'], row['app_id'], 'cube'))
            db.execute('INSERT INTO runtime_binding VALUES(?,?,?,?,?)', (row['sandbox_id'], 'cube', 'remote', row['template_id'], 4))
            db.execute('INSERT INTO runtime_migration VALUES(?,?,?,?,?,?)', (row['sandbox_id'], 'complete', 'remote', row['template_id'], '1'*64, '2'*64))
            self.assertEqual(c.accepted_binding(db, row)['runtime_id'], 'remote')
            for field, value in (('phase', 'verified'), ('runtime_id', 'other'), ('template_id', 'other'), ('archive_sha256', ''), ('home_sha256', '')):
                db.execute('SAVEPOINT fixture')
                db.execute('UPDATE runtime_migration SET ' + field + '=?', (value,))
                with self.subTest(field=field), self.assertRaises(c.b.Refused): c.accepted_binding(db, row)
                db.execute('ROLLBACK TO fixture'); db.execute('RELEASE fixture')
            db.execute("UPDATE sandbox SET runtime_provider='docker'")
            with self.assertRaises(c.b.Refused): c.accepted_binding(db, row)

    def test_failed_phase_never_reopens_retries_or_powers_worker(self):
        for fail in ('preflight', 'drain', 'migrate_cohort', 'restore_controller', 'ready_fence', 'verify_apis'):
            host = mock.Mock(); host.c = config(); getattr(host, fail).side_effect = RuntimeError('injected')
            events = []; sequence = c.Sequence(host, lambda phase, value=None: events.append(phase))
            with self.subTest(fail=fail), self.assertRaises(RuntimeError): sequence.start(sequence.stop())
            host.reopen.assert_not_called(); host.start_worker.assert_not_called(); host.supervisor_stop.assert_not_called()
            self.assertNotIn('complete', events)
        host = mock.Mock(); host.c = config(); host.accepted = [project()]; host.partial = False; events = []
        sequence = c.Sequence(host, lambda phase, value=None: events.append(phase)); result = sequence.start(sequence.stop())
        self.assertTrue(result['original_sources_retained']); self.assertFalse(result['global_migration_complete'])
        self.assertEqual(result['customer_projects_migrated'], 1)
        self.assertEqual(host.migrate_cohort.call_count, 1); host.start_worker.assert_not_called()

    def settlement_fixture(self, phase='staged'):
        db = sqlite3.connect(':memory:'); self.addCleanup(db.close)
        db.executescript('''CREATE TABLE sandbox(id,app_id,runtime_provider,container_id);
          CREATE TABLE runtime_binding(sandbox_id,provider,runtime_id,template_id,config_revision);
          CREATE TABLE runtime_migration(sandbox_id,phase,runtime_id,template_id,archive_sha256,home_sha256);''')
        host = c.Host.__new__(c.Host); host.c = config(); host.accepted = []; host.partial = False
        rows = [dict(project(), sandbox_id='1'*24 + '%02d' % i, app_id='0'*24 + '%02d' % i) for i in range(3)]
        host.c['projects'] = rows; host.plan = {'bindings': []}
        host.db = lambda: contextlib.nullcontext(db)
        for name in ('fence', 'source_fence', 'inputs', 'event', 'bindings_readonly'):
            setattr(host, name, mock.Mock())
        for i, row in enumerate(rows):
            db.execute('INSERT INTO sandbox VALUES(?,?,?,?)', (row['sandbox_id'], row['app_id'], 'cube' if i == 0 else 'docker', row['recorded_container_id']))
            if i < 2:
                db.execute('INSERT INTO runtime_migration VALUES(?,?,?,?,?,?)', (row['sandbox_id'], 'complete' if i == 0 else phase, 'vm'+str(i), row['template_id'], 'a'*64, 'b'*64))
        db.execute('INSERT INTO runtime_binding VALUES(?,?,?,?,?)', (rows[0]['sandbox_id'], 'cube', 'vm0', rows[0]['template_id'], 0))
        def abort(args, name, timeout):
            self.assertEqual(args[-1], 'abort'); self.assertEqual(args[1], rows[1]['sandbox_id'])
            db.execute("UPDATE runtime_migration SET phase='aborted' WHERE sandbox_id=?", (args[1],))
            return {'sandbox_id': args[1], 'phase': 'aborted'}
        host.run_cli = mock.Mock(side_effect=abort)
        return host, db, rows

    def test_partial_failure_preserves_complete_bindings_and_original_sources(self):
        host, db, rows = self.settlement_fixture()
        before = db.execute('SELECT * FROM sandbox ORDER BY id').fetchall()
        host.settle_failed_cohort(); host.restoration_scope()
        self.assertTrue(host.partial)
        self.assertEqual([v['sandbox_id'] for v in host.accepted], [rows[0]['sandbox_id']])
        self.assertEqual(host.plan['bindings'], host.accepted)
        self.assertEqual(db.execute('SELECT * FROM sandbox ORDER BY id').fetchall(), before)
        self.assertEqual(db.execute('SELECT phase FROM runtime_migration ORDER BY sandbox_id').fetchall(), [('complete',), ('aborted',)])
        host.run_cli.assert_called_once()
        # The unattempted third row remains in the original source fence.
        self.assertEqual(len(host.c['projects']), 3)

    def test_uncertain_create_or_committed_phase_never_auto_aborted(self):
        for phase in ('creating', 'committed', 'rollback_started'):
            host, db, rows = self.settlement_fixture(phase)
            with self.subTest(phase=phase), self.assertRaises(c.b.Refused): host.settle_failed_cohort()
            host.run_cli.assert_not_called(); self.assertFalse(host.partial)

    def test_failed_abort_or_changed_source_prevents_restoration(self):
        host, db, rows = self.settlement_fixture()
        host.run_cli.side_effect = c.NativeMigrationFailed('provider failed')
        with self.assertRaises(c.NativeMigrationFailed): host.settle_failed_cohort()
        self.assertFalse(host.partial)
        with self.assertRaises(c.b.Refused): host.restoration_scope()
        host, db, rows = self.settlement_fixture()
        host.settle_failed_cohort()
        db.execute("UPDATE sandbox SET container_id='replacement' WHERE id=?", (rows[2]['sandbox_id'],))
        with self.assertRaises(c.b.Refused): host.restoration_scope()

    def test_cli_retains_canonical_paths_and_has_no_retirement_action(self):
        host = c.Host.__new__(c.Host); host.c = config()
        args = host.cli_args()
        self.assertEqual(args[args.index('--database') + 1], '/var/lib/sandboxd/state/sandboxd.db')
        self.assertEqual(args[args.index('--workspaces') + 1], '/var/lib/sandboxd/workspaces')
        self.assertEqual(args[args.index('--archives') + 1], '/mnt/nvme/baarcha-cube/migration-archives')
        self.assertNotIn('retire-source', args)


if __name__ == '__main__': unittest.main()
