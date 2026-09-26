import copy
import fcntl
import contextlib
import io
import sqlite3
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import docker_homes as homes

A, B = 'a' * 64, 'b' * 64


def container(cid, running, restart):
    return {'Id': cid, 'Name': '/sandbox-' + cid[:12], 'Image': 'sha256:' + 'd' * 64,
            'Created': '2026-09-26T00:00:00Z', 'Config': {'Env': ['FIXTURE_SECRET=not-for-journal']},
            'HostConfig': {'RestartPolicy': {'Name': restart, 'MaximumRetryCount': 0}, 'Binds': ['/fixture/home:/home/sandbox']},
            'Mounts': [{'Destination': '/home/sandbox', 'Type': 'bind', 'Source': '/fixture/' + cid, 'RW': True}],
            'State': {'Running': running, 'Pid': 123 if running else 0, 'Status': 'running' if running else 'exited', 'Paused': False, 'Restarting': False, 'Dead': False}}


class DockerHomes(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name).resolve() / 'journal'
        self.rows = {A: container(A, True, 'unless-stopped'), B: container(B, False, 'always')}
        self.config = {'docker_homes': [{'container_id': A}, {'container_id': B}]}
        self.calls = []
        self.observer = mock.patch.object(homes, 'observe', side_effect=self.observe).start()
        self.addCleanup(mock.patch.stopall)
        mock.patch.object(homes, 'held_locks').start()
        mock.patch.object(homes, 'inspect', side_effect=lambda ids: {cid: copy.deepcopy(self.rows[cid]) for cid in ids}).start()
        mock.patch.object(homes, 'run', side_effect=self.effect).start()
        mock.patch.object(homes.roles, 'private', side_effect=lambda p, **kw: Path(p)).start()

    def observe(self, config, allowed):
        for cid, c in self.rows.items():
            homes.need(not c['State']['Running'] or cid in allowed, 'Unexpected running fixture')
        return copy.deepcopy(self.rows)

    def effect(self, args, **kw):
        self.calls.append(args)
        cid = args[-1]
        # A durable, fsynced request must already be present before every effect.
        entries = sorted(self.directory.glob('[0-9][0-9][0-9][0-9]-*.json'))
        request = json.loads(entries[-1].read_text())
        self.assertEqual(request['phase'], 'request')
        self.assertEqual(request['container_id'], cid)
        c = self.rows[cid]
        if args[1] == 'update':
            value = args[2].removeprefix('--restart=')
            name, _, count = value.partition(':')
            c['HostConfig']['RestartPolicy'] = {'Name': name, 'MaximumRetryCount': int(count or 0)}
        elif args[1] in ('stop', 'start'):
            running = args[1] == 'start'
            c['State'].update(Running=running, Pid=123 if running else 0, Status='running' if running else 'exited')
        else: self.fail('Unexpected mutating operation')
        return b''

    def freeze(self): homes.freeze(self.config, self.directory, (3, 4, 5, 6))
    def restore(self): homes.restore(self.config, self.directory, (3, 4, 5, 6))

    def test_roundtrip_preserves_stopped_and_every_restart_policy(self):
        self.freeze()
        self.assertTrue((self.directory / 'frozen.json').is_file())
        self.assertEqual([self.rows[c]['HostConfig']['RestartPolicy']['Name'] for c in (A, B)], ['no', 'no'])
        self.assertFalse(any(c['State']['Running'] for c in self.rows.values()))
        self.assertEqual([x for x in self.calls if x[1] == 'stop'], [['docker', 'stop', '--time=-1', A]])
        self.assertEqual([x[-1] for x in self.calls[:2]], [A, B])
        self.restore()
        self.assertTrue(self.rows[A]['State']['Running'])
        self.assertFalse(self.rows[B]['State']['Running'])
        self.assertEqual([x for x in self.calls if x[1] == 'start'], [['docker', 'start', A]])
        self.assertEqual([self.rows[c]['HostConfig']['RestartPolicy']['Name'] for c in (A, B)], ['unless-stopped', 'always'])
        self.assertNotIn('not-for-journal', (self.directory / 'baseline.json').read_text())
        with self.assertRaisesRegex(RuntimeError, 'already attempted'): self.restore()

    def test_stop_timeout_retains_request_and_never_restarts(self):
        def timeout(args, **kw):
            if args[1] == 'stop': raise subprocess.TimeoutExpired(args, 180)
            return self.effect(args, **kw)
        with mock.patch.object(homes, 'run', side_effect=timeout):
            with self.assertRaises(subprocess.TimeoutExpired): self.freeze()
        self.assertFalse((self.directory / 'frozen.json').exists())
        self.assertTrue(self.rows[A]['State']['Running'])
        self.assertEqual(self.rows[B]['HostConfig']['RestartPolicy']['Name'], 'no')
        self.assertFalse(any(x[1] in ('start', 'kill', 'rm') for x in self.calls))
        with self.assertRaises(FileNotFoundError): self.restore()

    def test_stop_acknowledgement_must_be_authoritative(self):
        def fake(args, **kw):
            if args[1] == 'stop': return b'ack-but-still-running'
            return self.effect(args, **kw)
        with mock.patch.object(homes, 'run', side_effect=fake):
            with self.assertRaisesRegex(RuntimeError, 'expected state'): self.freeze()
        self.assertFalse((self.directory / 'frozen.json').exists())

    def test_policy_drift_in_previously_processed_source_refuses_next_effect(self):
        observations = 0
        def observe(config, allowed):
            nonlocal observations
            observations += 1
            if observations == 3: self.rows[A]['HostConfig']['RestartPolicy']['Name'] = 'always'
            return self.observe(config, allowed)
        with mock.patch.object(homes, 'observe', side_effect=observe):
            with self.assertRaisesRegex(RuntimeError, 'expected state'): self.freeze()
        self.assertEqual(len(self.calls), 1)

    def test_recreated_container_or_changed_definition_never_restarted(self):
        self.freeze()
        self.rows[A]['Config']['Env'].append('CHANGED=true')
        before = len(self.calls)
        with self.assertRaisesRegex(RuntimeError, 'generation changed'): self.restore()
        self.assertEqual(len(self.calls), before)

    def test_restore_partial_failure_cannot_automatically_retry(self):
        self.freeze()
        def fail(args, **kw):
            if args[1] == 'start': raise subprocess.TimeoutExpired(args, 60)
            return self.effect(args, **kw)
        with mock.patch.object(homes, 'run', side_effect=fail):
            with self.assertRaises(subprocess.TimeoutExpired): self.restore()
        with self.assertRaisesRegex(RuntimeError, 'already attempted'): self.restore()

    def test_already_stopped_unexpectedly_running_blocks_restore(self):
        self.freeze()
        self.rows[B]['State'].update(Running=True, Pid=12, Status='running')
        before = len(self.calls)
        with self.assertRaisesRegex(RuntimeError, 'Unexpected running'): self.restore()
        self.assertEqual(len(self.calls), before)

    def test_incomplete_or_refused_journal_cannot_certify_freeze(self):
        self.freeze()
        homes.Journal(self.directory).event('request', action='stop', container_id=A)
        with self.assertRaisesRegex(RuntimeError, 'Incomplete'): self.restore()

    def test_policy_retry_count_roundtrip(self):
        self.rows[A]['HostConfig']['RestartPolicy'] = {'Name': 'on-failure', 'MaximumRetryCount': 7}
        self.freeze(); self.restore()
        self.assertIn(['docker', 'update', '--restart=on-failure:7', A], self.calls)

    def test_nested_mount_overlap_detected(self):
        self.assertTrue(homes.overlap('/var/lib', '/var/lib/sandboxd/workspaces/one'))
        self.assertTrue(homes.overlap('/var/lib/sandboxd/workspaces/one/data', '/var/lib/sandboxd/workspaces/one'))
        self.assertFalse(homes.overlap('/var/lib/sandboxd/workspaces/one-two', '/var/lib/sandboxd/workspaces/one'))


class FenceObservation(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dbpath = Path(self.tmp.name).resolve() / 'state.db'
        with contextlib.closing(sqlite3.connect(self.dbpath)) as db:
            db.executescript("""CREATE TABLE task(status TEXT);
            CREATE TABLE cube_recovery(phase TEXT,source_artifact_paths_json TEXT);
            CREATE TABLE cube_admission(state TEXT); CREATE TABLE runtime_migration(phase TEXT);
            CREATE TABLE sandbox(id TEXT,container_id TEXT,workspace_mnt TEXT,runtime_provider TEXT);
            CREATE TABLE snapshot(id TEXT,image_path TEXT,status TEXT);""")
            db.execute('INSERT INTO sandbox VALUES(?,?,?,?)', ('source', A, '/fixture/' + A, 'docker'))
            db.commit()
            self.inventory = homes.roles.database_inventory(db)
        self.cp = container('c' * 64, False, 'no')
        self.cp['Name'] = '/src-sandboxd-1'
        self.source = container(A, True, 'always')
        self.outside = container('e' * 64, True, 'no')
        self.outside['Mounts'] = [{'RW': True, 'Source': '/unrelated', 'Destination': '/data', 'Type': 'bind'}]
        self.config = {'inventory': self.inventory, 'controller_id': self.cp['Id'], 'controller_image': self.cp['Image'],
                       'controller_env_sha256': homes.hash_json(self.cp['Config']['Env']), 'reviewed_caddy_sha256': homes.hash_json({}),
                       'docker_homes': [{'sandbox_id': 'source', 'container_id': A, 'image': self.source['Image'], 'source': '/fixture/' + A}],
                       'role_paths': {'rollback-extra': [], 'library-extra': []}, 'stopped_writer_units': ['baarcha-project-env-apply.timer']}
        self.writer_active = False
        def inspect(ids):
            all_rows = {r['Id']: r for r in (self.cp, self.source, self.outside)}
            return {i: copy.deepcopy(all_rows[i]) for i in ids}
        def run(args, **kwargs):
            if args[0] == 'systemctl': return ('LoadState=loaded\nActiveState=' + ('active' if self.writer_active else 'inactive') + '\n').encode()
            if args[:3] == ['docker', 'ps', '--no-trunc']: return (A + '\n' + self.outside['Id']).encode()
            raise AssertionError(args)
        for patch in (mock.patch.object(homes.roles, 'DB', self.dbpath),
                      mock.patch.object(homes.roles, 'verify_inputs'),
                      mock.patch.object(homes.roles.cold_pair, 'no_open_users'),
                      mock.patch.object(homes.roles, 'route_fence'),
                      mock.patch.object(homes, 'inspect', side_effect=inspect),
                      mock.patch.object(homes, 'run', side_effect=run),
                      mock.patch.object(homes.urllib.request, 'urlopen', side_effect=lambda *a, **kw: io.BytesIO(b'{}'))):
            patch.start()
        self.addCleanup(mock.patch.stopall)

    def test_running_sources_allowed_only_before_ack_and_unrelated_readers_stay_live(self):
        self.assertIn(A, homes.observe(self.config, {A}))
        with self.assertRaisesRegex(RuntimeError, 'Unexpected running source'): homes.observe(self.config, set())

    def test_shared_parent_writable_mount_blocks_freeze(self):
        self.outside['Mounts'][0]['Source'] = '/fixture'
        with self.assertRaisesRegex(RuntimeError, 'writable|write'): homes.observe(self.config, {A})
        self.outside['Mounts'][0]['RW'] = False
        homes.observe(self.config, {A})

    def test_controller_restart_directwriter_caddy_or_active_task_block(self):
        self.cp['HostConfig']['RestartPolicy']['Name'] = 'always'
        with self.assertRaisesRegex(RuntimeError, 'Controller'): homes.observe(self.config, {A})
        self.cp['HostConfig']['RestartPolicy']['Name'] = 'no'
        self.writer_active = True
        with self.assertRaisesRegex(RuntimeError, 'Direct writer'): homes.observe(self.config, {A})
        self.writer_active = False
        with mock.patch.object(homes.roles, 'route_fence', side_effect=RuntimeError('route open')):
            with self.assertRaisesRegex(RuntimeError, 'route open'): homes.observe(self.config, {A})
        with contextlib.closing(sqlite3.connect(self.dbpath)) as db:
            db.execute("INSERT INTO task VALUES('running')")
            db.commit()
        with self.assertRaisesRegex(RuntimeError, 'Active coding'): homes.observe(self.config, {A})



class RealLockProbe(unittest.TestCase):
    def test_unlocked_shared_and_other_ofd_refused_without_upgrade(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / 'lock'
            fd = os.open(p, os.O_RDWR | os.O_CREAT, 0o600)
            other = os.open(p, os.O_RDWR)
            try:
                with self.assertRaises(RuntimeError): homes.roles.require_inherited_exclusive(p, fd)
                fcntl.flock(fd, fcntl.LOCK_SH)
                with self.assertRaises(RuntimeError): homes.roles.require_inherited_exclusive(p, fd)
                fcntl.flock(fd, fcntl.LOCK_UN)
                fcntl.flock(other, fcntl.LOCK_EX)
                with self.assertRaises(BlockingIOError): homes.roles.require_inherited_exclusive(p, fd)
                fcntl.flock(other, fcntl.LOCK_UN)
                fcntl.flock(fd, fcntl.LOCK_EX)
                homes.roles.require_inherited_exclusive(p, fd)
                with self.assertRaises(BlockingIOError): fcntl.flock(other, fcntl.LOCK_SH | fcntl.LOCK_NB)
            finally:
                os.close(other); os.close(fd)


if __name__ == '__main__': unittest.main()
