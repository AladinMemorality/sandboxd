import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('motion_release', HERE / 'release.py')
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)


def tar_bytes(rows):
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode='w') as tar:
        for name, raw, kind in rows:
            info = tarfile.TarInfo(name); info.mode = 0o644; info.size = len(raw); info.type = kind
            tar.addfile(info, io.BytesIO(raw) if kind == tarfile.REGTYPE else None)
    return out.getvalue()


class CandidateTests(unittest.TestCase):
    def check_rows(self, rows):
        data = tar_bytes(rows)
        with tempfile.TemporaryDirectory() as d:
            p = Path(d).resolve() / 'source.tar'; p.write_bytes(data)
            manifest = {'source_tar_sha256': m.sha(data), 'files': [
                {'path': f, 'candidate_sha256': m.sha(f.encode()), 'bytes': len(f)} for f in m.FILES]}
            return m.candidate(p, manifest)

    def test_exact_three_regular_members(self):
        result = self.check_rows([(f, f.encode(), tarfile.REGTYPE) for f in m.FILES])
        self.assertEqual(set(result), set(m.FILES))

    def test_extra_path_traversal_link_and_duplicate_refused(self):
        rows = [(f, f.encode(), tarfile.REGTYPE) for f in m.FILES]
        for added in [('../worker.env', b'x', tarfile.REGTYPE), ('server/index.mjs', b'x', tarfile.REGTYPE),
                      ('server/extra.mjs', b'', tarfile.SYMTYPE)]:
            with self.subTest(added=added), self.assertRaises(RuntimeError): self.check_rows(rows + [added])

    def test_modified_candidate_refused(self):
        rows = [(f, b'changed' if i == 0 else f.encode(), tarfile.REGTYPE) for i, f in enumerate(m.FILES)]
        with self.assertRaisesRegex(RuntimeError, 'content drift'): self.check_rows(rows)



class FilesystemTests(unittest.TestCase):
    def test_snapshot_rejects_symlink_and_hardlink(self):
        with tempfile.TemporaryDirectory() as d:
            d = Path(d).resolve(); original = d / 'original'; original.write_bytes(b'x')
            link = d / 'link'; link.symlink_to(original)
            with self.assertRaises(RuntimeError): m.snapshot(link)
            link.unlink(); os.link(original, link)
            with self.assertRaises(RuntimeError): m.snapshot(original)

    def test_atomic_preserves_explicit_mode_and_owner(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d).resolve() / 'index.mjs'; path.write_bytes(b'old')
            m.atomic(path, b'new', os.getuid(), os.getgid(), 0o640)
            self.assertEqual(path.read_bytes(), b'new')
            self.assertEqual(path.stat().st_mode & 0o777, 0o640)
            self.assertEqual(list(path.parent.iterdir()), [path])

    def test_exclusive_journal_input_refuses_replay(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d).resolve() / 'intent'
            m.exclusive(path, b'first')
            with self.assertRaises(FileExistsError): m.exclusive(path, b'replay')
            self.assertEqual(path.read_bytes(), b'first')


class Fake(m.Release):
    def __init__(self, fail=None):
        self.calls = []; self.fail = fail; self.mutated = False; self.stop_unconfirmed = False
    def step(self, name):
        self.calls.append(name)
        if name == self.fail: raise RuntimeError('injected')
    def event(self, name, **detail): self.calls.append('event:' + name)
    def preflight(self): self.step('preflight')
    def stop(self):
        self.stop_unconfirmed = True
        self.step('stop')
        self.stop_unconfirmed = False
    def fence(self): self.step('fence')
    def backup(self): self.step('backup')
    def install(self): self.step('install')
    def start_verify(self, uds): self.step('verify:' + str(uds))
    def rollback(self): self.step('rollback')


class LifecycleTests(unittest.TestCase):
    def test_success_order_backup_precedes_install_and_no_reopen(self):
        r = Fake(); r.execute()
        self.assertLess(r.calls.index('stop'), r.calls.index('backup'))
        self.assertLess(r.calls.index('backup'), r.calls.index('install'))
        self.assertIn('verify:True', r.calls)
        self.assertNotIn('rollback', r.calls)
        self.assertNotIn('reopen', r.calls)

    def test_preflight_failure_never_stops_or_restarts(self):
        r = Fake('preflight')
        with self.assertRaises(RuntimeError): r.execute()
        self.assertEqual(r.calls, ['preflight'])

    def test_stop_backup_partial_install_startup_failure_are_retained(self):
        for phase in ('stop', 'backup', 'install', 'verify:True'):
            with self.subTest(phase=phase):
                r = Fake(phase)
                with self.assertRaises(RuntimeError): r.execute()
                self.assertIn('event:failed', r.calls)
                if phase == 'stop':
                    self.assertNotIn('rollback', r.calls)
                    self.assertIn('event:stop_unconfirmed', r.calls)
                    self.assertNotIn('backup', r.calls)
                else:
                    self.assertIn('rollback', r.calls)
                self.assertNotIn('event:complete', r.calls)

    def test_rollback_failure_is_not_success(self):
        r = Fake('install')
        def fail(): raise RuntimeError('unknown stop')
        r.rollback = fail
        with self.assertRaises(RuntimeError): r.execute()
        self.assertIn('event:rollback_incomplete', r.calls)
        self.assertNotIn('event:complete', r.calls)

    def test_partial_install_restores_only_source_not_customer_data(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d).resolve(); app = root / 'app'; (app / 'server').mkdir(parents=True)
            stage = root / 'stage'; stage.mkdir(); drop = root / 'service.d/20-cube-uds.conf'
            original = app / m.FILES[0]; original.write_bytes(b'old'); old = m.snapshot(original)
            (stage / 'original-index.mjs').write_bytes(b'old')
            (stage / 'original-index.mjs').chmod(0o600)
            original.write_bytes(b'new'); (app / m.FILES[1]).write_bytes(b'helper')
            data = root / 'customer.sql'; data.write_bytes(b'latest committed data')
            r = m.Release({'source_before': {m.FILES[0]: old}}, stage,
                          {m.FILES[0]: b'new', m.FILES[1]: b'helper'}, b'drop')
            r.installed = list(m.FILES[:2]); r.stop = mock.Mock(); r.fence = mock.Mock()
            r.cmd = mock.Mock(); r.start_verify = mock.Mock(); r.event = mock.Mock()
            original_regular = m.regular
            # root0600 policy is tested separately; filesystem rollback also runs as unprivileged CI.
            def regular(path, private=False, maximum=m.MAX_JSON): return original_regular(path, False, maximum)
            with mock.patch.object(m, 'APP', app), mock.patch.object(m, 'DROP', drop), mock.patch.object(m, 'regular', regular):
                r.rollback()
            self.assertEqual(original.read_bytes(), b'old')
            self.assertFalse((app / m.FILES[1]).exists())
            self.assertEqual(data.read_bytes(), b'latest committed data')
            r.start_verify.assert_called_once_with(False)

    def test_rollback_refuses_unrelated_source_drift(self):
        with tempfile.TemporaryDirectory() as d:
            app = Path(d).resolve(); (app / 'server').mkdir(); (app / m.FILES[0]).write_bytes(b'external change')
            r = m.Release({'source_before': {}}, app, {m.FILES[0]: b'candidate'}, b'drop')
            r.installed = [m.FILES[0]]; r.stop = mock.Mock(); r.fence = mock.Mock()
            with mock.patch.object(m, 'APP', app), self.assertRaisesRegex(RuntimeError, 'unreviewed'):
                r.rollback()
            self.assertEqual((app / m.FILES[0]).read_bytes(), b'external change')

    def test_abnormal_systemd_stop_refuses_backup_and_retains_evidence(self):
        for result, code, status in [('timeout', '2', '9'), ('oom-kill', '2', '9'),
                                     ('success', '2', '9'), ('success', '1', '1'),
                                     ('success', '3', '15'), ('signal', '2', '15')]:
            with self.subTest(result=result, code=code, status=status):
                r = m.Release({}, '/', {}, b''); r.cmd = mock.Mock()
                r.service = lambda: {'Result': result, 'ExecMainCode': code, 'ExecMainStatus': status,
                                     'ActiveState': 'inactive', 'SubState': 'dead', 'MainPID': '0', 'ControlPID': '0'}
                r.event = mock.Mock()
                with self.assertRaisesRegex(RuntimeError, 'not proven graceful'): r.stop()
                self.assertTrue(r.stop_unconfirmed)
                self.assertEqual(r.event.call_args.args[0], 'worker_stop_observed')
                self.assertNotIn('worker_stopped', [x.args[0] for x in r.event.call_args_list])

    def test_normal_zero_or_term_stop_is_accepted_after_process_absence(self):
        for code, status in [('1', '0'), ('2', '15')]:
            with self.subTest(code=code, status=status):
                r = m.Release({'service_uid': 985}, '/', {}, b''); r.cmd = mock.Mock()
                r.service = lambda: {'Result': 'success', 'ExecMainCode': code, 'ExecMainStatus': status,
                                     'ActiveState': 'inactive', 'SubState': 'dead', 'MainPID': '0', 'ControlPID': '0'}
                r.event = mock.Mock()
                with mock.patch.object(m.Path, 'iterdir', return_value=iter(())): r.stop()
                self.assertFalse(r.stop_unconfirmed)
                self.assertEqual(r.event.call_args.args[0], 'worker_stopped')

    def test_projects_require_exact_complete_state_and_drained_jobs(self):
        body = {'projects': [{'id': 'one', 'assets': [{'id': 'asset'}], 'jobs': []}]}
        r = m.Release({'project_count': 1, 'projects_sha256': m.canonical(body)}, '/', {}, b'')
        r.http = lambda *a: (200, json.dumps(body).encode())
        self.assertEqual(r.projects(), body)
        body['projects'][0]['assets'][0]['id'] = 'different'
        with self.assertRaisesRegex(RuntimeError, 'changed'): r.projects()
        body['projects'][0]['jobs'] = [{'status': 'running'}]
        r.c['projects_sha256'] = m.canonical(body)
        with self.assertRaisesRegex(RuntimeError, 'not drained'): r.projects()


class FenceTests(unittest.TestCase):
    def fixture(self):
        live = {'routes': ['reviewed exact maintenance fence']}
        config = {'boot_id': 'boot', 'controller_id': 'controller', 'controller_image': 'image',
                  'fence': {'expires_boottime': 1100, 'caddy_sha256': m.canonical(live),
                            'writer_containers': ['writer'], 'stopped_units': ['timer', 'refresh']}}
        r = m.Release(config, '/', {}, b'')
        state = {'controller': False, 'writer': False, 'timer': 'inactive', 'refresh': 'inactive', 'connections': b''}
        def cmd(args, **kw):
            if args[0] == 'docker':
                return json.dumps([{'Id': args[2], 'Image': 'image', 'State': {
                    'Running': state[args[2]], 'Restarting': False}}]).encode()
            if args[0] == 'systemctl': return ('ActiveState=' + state[args[2]] + '\nMainPID=0\nControlPID=0').encode()
            if args[0] == 'ss': return state['connections']
            raise AssertionError(args)
        r.cmd = cmd
        connection = mock.Mock(); connection.getresponse.return_value.status = 200
        connection.getresponse.return_value.read.return_value = json.dumps(live).encode()
        return r, state, connection

    def invoke(self, r, connection):
        with mock.patch.object(m.Path, 'read_text', return_value='boot'), \
             mock.patch.object(m.time, 'CLOCK_BOOTTIME', 7, create=True), \
             mock.patch.object(m.time, 'clock_gettime', return_value=1000), \
             mock.patch.object(m.http.client, 'HTTPConnection', return_value=connection): r.fence()

    def test_verified_stopped_writers_empty_connections(self):
        r, state, c = self.fixture(); self.invoke(r, c)

    def test_running_controller_or_proxy_refused(self):
        for key in ('controller', 'writer'):
            r, state, c = self.fixture(); state[key] = True
            with self.subTest(key=key), self.assertRaises(RuntimeError): self.invoke(r, c)

    def test_timer_and_inflight_requests_cannot_masquerade_as_empty_queue(self):
        for key, value in [('timer', 'active'), ('refresh', 'activating'), ('connections', b'ESTAB')]:
            r, state, c = self.fixture(); state[key] = value
            with self.subTest(key=key), self.assertRaises(RuntimeError): self.invoke(r, c)

    def test_expired_fence_or_live_routing_drift_refused(self):
        r, state, c = self.fixture(); r.c['fence']['expires_boottime'] = 999
        with self.assertRaises(RuntimeError): self.invoke(r, c)
        r, state, c = self.fixture(); c.getresponse.return_value.read.return_value = b'{}'
        with self.assertRaises(RuntimeError): self.invoke(r, c)

    def test_private_metadata_does_not_accept_public_mode(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d).resolve() / 'config'; p.write_bytes(b'{}'); p.chmod(0o644)
            with self.assertRaises(RuntimeError): m.regular(p, private=True)


class InheritedLockTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.path = Path(self.tmp.name).resolve() / 'lock'
        self.path.touch()
        self.fd = os.open(self.path, os.O_RDWR)
        self.probe = os.open(self.path, os.O_RDWR)

    def tearDown(self):
        os.close(self.probe); os.close(self.fd); self.tmp.cleanup()

    def test_unlocked_descriptor_is_refused_without_acquiring(self):
        with self.assertRaisesRegex(RuntimeError, 'not already held'):
            m.require_inherited_exclusive(self.path, self.fd)
        m.fcntl.flock(self.probe, m.fcntl.LOCK_EX | m.fcntl.LOCK_NB)

    def test_shared_descriptor_is_refused_without_upgrading(self):
        m.fcntl.flock(self.fd, m.fcntl.LOCK_SH | m.fcntl.LOCK_NB)
        with self.assertRaisesRegex(RuntimeError, 'not already held'):
            m.require_inherited_exclusive(self.path, self.fd)
        # Still shared: an independent shared holder joins, but EX cannot.
        m.fcntl.flock(self.probe, m.fcntl.LOCK_SH | m.fcntl.LOCK_NB)
        m.fcntl.flock(self.probe, m.fcntl.LOCK_UN)
        with self.assertRaises(BlockingIOError):
            m.fcntl.flock(self.probe, m.fcntl.LOCK_EX | m.fcntl.LOCK_NB)

    def test_different_open_description_owner_is_refused_and_retained(self):
        m.fcntl.flock(self.probe, m.fcntl.LOCK_EX | m.fcntl.LOCK_NB)
        with self.assertRaises(BlockingIOError):
            m.require_inherited_exclusive(self.path, self.fd)
        with self.assertRaises(BlockingIOError):
            m.fcntl.flock(self.fd, m.fcntl.LOCK_SH | m.fcntl.LOCK_NB)

    def test_exact_held_description_accepts_and_duplicate_close_retains(self):
        m.fcntl.flock(self.fd, m.fcntl.LOCK_EX | m.fcntl.LOCK_NB)
        duplicate = os.dup(self.fd)
        try: m.require_inherited_exclusive(self.path, duplicate)
        finally: os.close(duplicate)
        with self.assertRaises(BlockingIOError):
            m.fcntl.flock(self.probe, m.fcntl.LOCK_SH | m.fcntl.LOCK_NB)

    def test_failure_inside_four_lock_context_retains_caller_locks(self):
        paths = []; owned = []
        original_fstat = os.fstat
        def root_owned_fstat(fd):
            # Exercise real kernel flocks on unprivileged CI; only the root-owner
            # policy field is synthesized for these test-owned temporary files.
            values = list(original_fstat(fd)); values[4] = 0
            return os.stat_result(values)
        try:
            for i in range(4):
                p = Path(self.tmp.name).resolve() / ('lock' + str(i)); p.touch(); paths.append(str(p))
                fd = os.open(p, os.O_RDWR); owned.append(fd)
                m.fcntl.flock(fd, m.fcntl.LOCK_EX | m.fcntl.LOCK_NB)
            with mock.patch.object(m, 'LOCKS', tuple(paths)), mock.patch.object(m.os, 'fstat', root_owned_fstat):
                with self.assertRaisesRegex(RuntimeError, 'caller failure'):
                    with m.locks(owned): raise RuntimeError('caller failure')
            for path in paths:
                fd = os.open(path, os.O_RDWR)
                try:
                    with self.assertRaises(BlockingIOError): m.fcntl.flock(fd, m.fcntl.LOCK_SH | m.fcntl.LOCK_NB)
                finally: os.close(fd)
        finally:
            for fd in owned: os.close(fd)


if __name__ == '__main__': unittest.main()
