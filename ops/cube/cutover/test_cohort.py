import copy
import importlib.util
from pathlib import Path
import sqlite3
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
        host = mock.Mock(); host.c = config(); events = []
        sequence = c.Sequence(host, lambda phase, value=None: events.append(phase)); result = sequence.start(sequence.stop())
        self.assertTrue(result['original_sources_retained']); self.assertFalse(result['global_migration_complete'])
        self.assertEqual(result['customer_projects_migrated'], 1)
        self.assertEqual(host.migrate_cohort.call_count, 1); host.start_worker.assert_not_called()

    def test_cli_retains_canonical_paths_and_has_no_retirement_action(self):
        host = c.Host.__new__(c.Host); host.c = config()
        args = host.cli_args()
        self.assertEqual(args[args.index('--database') + 1], '/var/lib/sandboxd/state/sandboxd.db')
        self.assertEqual(args[args.index('--workspaces') + 1], '/var/lib/sandboxd/workspaces')
        self.assertEqual(args[args.index('--archives') + 1], '/mnt/nvme/baarcha-cube/migration-archives')
        self.assertNotIn('retire-source', args)


if __name__ == '__main__': unittest.main()
