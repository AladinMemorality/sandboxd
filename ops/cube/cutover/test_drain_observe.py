import importlib.util
import json
import sqlite3
from contextlib import closing
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace
from datetime import datetime, timezone, timedelta

spec = importlib.util.spec_from_file_location('drain_observe', Path(__file__).with_name('drain_observe.py'))
d = importlib.util.module_from_spec(spec); spec.loader.exec_module(d)


class ObserveTests(unittest.TestCase):
    def sqlite_fixture(self, directory, complete=True):
        path = Path(directory) / 'state.db'
        with closing(sqlite3.connect(path)) as db:
            db.execute('CREATE TABLE task(status TEXT)')
            db.executemany('INSERT INTO task VALUES(?)', [('running',), ('queued',), ('done',)])
            if complete:
                db.execute('CREATE TABLE runtime_binding(runtime_id TEXT)')
                db.execute("INSERT INTO runtime_binding VALUES('owned-fixture')")
            db.commit()
        return path

    def assert_connection_closed(self, connection):
        # Keep a strong reference: garbage collection must not make a leaked
        # sqlite3.Connection context manager look safe by closing it for us.
        with self.assertRaisesRegex(sqlite3.ProgrammingError, 'closed'):
            connection.execute('SELECT 1')

    def test_sqlite_observation_closes_connection_on_success(self):
        real_connect = sqlite3.connect
        held = []
        def connect(*args, **kwargs):
            connection = real_connect(*args, **kwargs)
            held.append(connection)
            return connection
        with tempfile.TemporaryDirectory() as directory:
            path = self.sqlite_fixture(directory)
            with patch.object(d, 'DB', str(path)), patch.object(d.sqlite3, 'connect', side_effect=connect):
                result = d.sqlite_observation()
            self.assertEqual(result, {'task_status_counts': {'done': 1, 'queued': 1, 'running': 1},
                                      'runtime_bindings': 1, 'active_tasks': 2})
            self.assertEqual(len(held), 1)
            self.assert_connection_closed(held[0])

    def test_sqlite_observation_closes_connection_on_query_error(self):
        real_connect = sqlite3.connect
        held = []
        def connect(*args, **kwargs):
            connection = real_connect(*args, **kwargs)
            held.append(connection)
            return connection
        with tempfile.TemporaryDirectory() as directory:
            path = self.sqlite_fixture(directory, complete=False)
            with patch.object(d, 'DB', str(path)), patch.object(d.sqlite3, 'connect', side_effect=connect):
                with self.assertRaisesRegex(sqlite3.OperationalError, 'runtime_binding'):
                    d.sqlite_observation()
            self.assertEqual(len(held), 1)
            self.assert_connection_closed(held[0])

    def test_tcp_ownership_and_no_peer_export(self):
        rows = d.parse_tcp('header\n0: 0100007F:2328 AAAAAAAA:DEAD 01 0 0 0 0 0 45\n1: 0:2328 0:0000 0A 0 0 0 0 0 46', {'45'})
        self.assertEqual(rows, [{'inode': '45', 'state': 'established', 'local_port': 9000}])
        self.assertNotIn('AAAA', json.dumps(rows))

    def test_malformed_table_refused(self):
        with self.assertRaises(ValueError): d.parse_tcp('header\nshort', set())

    def test_socket_links_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp); (p/'1').symlink_to('socket:[42]'); (p/'2').symlink_to('private-file')
            self.assertEqual(d.socket_inodes(p), {'42'})

    def test_process_idle_does_not_become_request_count(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = Path(tmp)/'123'; (p/'fd').mkdir(parents=True); (p/'net').mkdir()
            (p/'stat').write_text('123 (process name) '+' '.join(['S']+['0']*18+['321']))
            (p/'fd'/'1').symlink_to('socket:[45]')
            (p/'net'/'tcp').write_text('header\n0: 0100007F:2328 0:0 0A 0 0 0 0 0 45\n')
            (p/'net'/'tcp6').write_text('header\n')
            result=d.process_sockets(123, Path(tmp))
            self.assertEqual(result['tcp_non_listen'], 0)
            self.assertTrue(result['stable_socket_set'])
            self.assertIsNone(result['request_count'])

    def controller(self):
        return {'Id':'a'*64, 'State':{'Running':False,'Pid':0,'ExitCode':0,'OOMKilled':False,
               'FinishedAt':datetime.now(timezone.utc).isoformat()}}

    def check_shutdown(self, raw, controller=None):
        since=(datetime.now(timezone.utc)-timedelta(seconds=10)).isoformat()
        with patch.object(d.subprocess, 'run', return_value=SimpleNamespace(stdout=raw, stderr=b'')):
            return d.shutdown_observation(controller or self.controller(), since)

    def test_clean_shutdown_evidence_limited_to_regular_http(self):
        result=self.check_shutdown(b'shutdown: signal received')
        self.assertTrue(result['regular_http_shutdown_success_observed'])
        self.assertFalse(result['hijacked_websocket_completion_proven'])

    def test_shutdown_timeout_refuses_proof(self):
        self.assertFalse(self.check_shutdown(b'shutdown: signal received\nshutdown: http server shutdown failed')['regular_http_shutdown_success_observed'])

    def test_silent_or_repeated_shutdown_refuses_proof(self):
        for raw in [b'', b'shutdown: signal received\nshutdown: signal received']:
            self.assertFalse(self.check_shutdown(raw)['regular_http_shutdown_success_observed'])

    def test_oom_and_nonzero_exit_refuse_proof(self):
        for field,value in [('OOMKilled',True),('ExitCode',137),('Running',True)]:
            c=self.controller(); c['State'][field]=value
            self.assertFalse(self.check_shutdown(b'shutdown: signal received', c)['regular_http_shutdown_success_observed'])

    def test_old_shutdown_window_refused(self):
        with self.assertRaises(ValueError): d.shutdown_observation(self.controller(), '2000-01-01T00:00:00Z')

    def test_absent_window_does_not_infer_success(self):
        self.assertIsNone(d.shutdown_observation(self.controller(), None)['regular_http_shutdown_success_observed'])

    def test_replaced_controller_refused_before_other_probes(self):
        with patch.object(d, 'command', return_value=json.dumps([{'Id':'b'*64}]).encode()):
            with self.assertRaisesRegex(ValueError, 'identity'): d.collect('a'*64)

    def test_descriptor_permission_failure_not_treated_as_idle(self):
        with patch.object(d.os, 'readlink', side_effect=PermissionError):
            with tempfile.TemporaryDirectory() as tmp:
                p=Path(tmp); (p/'1').touch()
                with self.assertRaises(PermissionError): d.socket_inodes(p)


if __name__ == '__main__': unittest.main()
