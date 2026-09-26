import importlib.util
import json
import os
from pathlib import Path
import sys
import subprocess
import errno
import tempfile
import time
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('frontend_profile_guest', Path(__file__).with_name('guest.py'))
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)


class GuestTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.source = self.root / 'source'
        self.source.mkdir()
        # Darwin reports EPERM for a vanished process group, unlike Linux's
        # ESRCH. Adapt only this test host, after proving no live group member.
        # The Linux-only runner keeps strict production signal failures.
        original_killpg = os.killpg
        def portable_test_killpg(pgid, sig):
            try:
                return original_killpg(pgid, sig)
            except PermissionError:
                rows = subprocess.check_output(['ps', '-axo', 'pgid=,stat='], text=True).splitlines()
                live = [line for line in rows if line.split() and line.split()[0] == str(pgid) and not line.split()[1].startswith('Z')]
                if sys.platform == 'darwin' and not live:
                    raise ProcessLookupError(errno.ESRCH, 'test-owned group is gone')
                raise
        self.signals = patch.object(g.os, 'killpg', portable_test_killpg)
        self.signals.start()

    def tearDown(self):
        self.signals.stop()
        self.temp.cleanup()

    def test_copy_preserves_internal_links_without_mutating_source(self):
        (self.source / 'dep').mkdir()
        (self.source / 'dep/file').write_text('immutable')
        (self.source / 'relative').symlink_to('dep/file')
        (self.source / 'absolute').symlink_to(self.source / 'dep/file')
        out = self.root / 'copy'
        result = g.copy_source(self.source, out, time.monotonic() + 2)
        self.assertEqual(result['logical_bytes'], 9)
        for name in ['relative', 'absolute']:
            self.assertEqual((out / name).resolve(), out / 'dep/file')
        (out / 'dep/file').write_text('changed')
        self.assertEqual((self.source / 'dep/file').read_text(), 'immutable')

    def test_escape_special_inode_and_deadline_refused(self):
        (self.source / 'escape').symlink_to(self.root)
        with self.assertRaisesRegex(ValueError, 'escapes'):
            g.inventory(self.source)
        (self.source / 'escape').unlink()
        os.mkfifo(self.source / 'pipe')
        with self.assertRaisesRegex(ValueError, 'unsupported'):
            g.inventory(self.source)
        (self.source / 'pipe').unlink()
        (self.source / 'file').write_text('x')
        with self.assertRaisesRegex(ValueError, 'deadline'):
            g.inventory(self.source, deadline=time.monotonic() - 1)
        with self.assertRaisesRegex(ValueError, 'byte limit'):
            g.inventory(self.source, max_bytes=0)
        with self.assertRaisesRegex(ValueError, 'count/depth'):
            g.inventory(self.source, max_files=0)

    def test_root_exec_is_not_silently_accepted(self):
        with patch.object(g.sys, 'platform', 'linux'), patch.object(g.os, 'getuid', return_value=0), patch.object(g.os, 'geteuid', return_value=0):
            with self.assertRaisesRegex(ValueError, 'never operator root'):
                g.require_uid()

    def test_no_environment_credentials_passed_to_build(self):
        with patch.dict(os.environ, {'ANTHROPIC_API_KEY': 'fake-secret', 'BRIDGE_TOKEN': 'fake-bridge', 'HTTPS_PROXY': 'http://private'}):
            env = g.sterile_env(self.root)
        self.assertNotIn('ANTHROPIC_API_KEY', env)
        self.assertNotIn('BRIDGE_TOKEN', env)
        self.assertNotIn('HTTPS_PROXY', env)
        self.assertEqual(env['npm_config_offline'], 'true')

    def test_success_and_log_cap(self):
        result = g.build(self.source, self.root / 'build.log', seconds=3,
                         command=[sys.executable, '-c', 'print("a"*1200000)'])
        self.assertEqual(result['exit_code'], 0)
        self.assertFalse(result['timed_out'])
        self.assertTrue(result['log_truncated'])
        self.assertEqual((self.root / 'build.log').stat().st_size, g.MAX_LOG)

    def test_closed_stdout_process_still_obeys_deadline(self):
        result = g.build(self.source, self.root / 'timeout.log', seconds=.2,
                         command=[sys.executable, '-c', 'import os,time;os.close(1);os.close(2);time.sleep(60)'])
        self.assertTrue(result['timed_out'])
        self.assertLess(result['elapsed_ms'], 3000)

    def test_exited_parent_cannot_leave_term_ignoring_writer(self):
        script = """import os,signal,time
if os.fork(): os._exit(0)
signal.signal(signal.SIGTERM, signal.SIG_IGN)
while True:
 with open('counter','w') as f: f.write(str(time.monotonic_ns()))
 time.sleep(.02)
"""
        result = g.build(self.source, self.root / 'child.log', seconds=.3, command=[sys.executable, '-c', script])
        self.assertTrue(result['timed_out'])
        before = (self.source / 'counter').read_text()
        time.sleep(.12)
        self.assertEqual((self.source / 'counter').read_text(), before)

    def test_terminal_guard_refuses_missing_extra_or_active_tasks(self):
        tasks = self.root / 'tasks'; tasks.mkdir()
        for task in g.PRIOR:
            (tasks / task).mkdir()
            (tasks / task / 'result.json').write_text(json.dumps({'id': task, 'status': 'failed'}))
        self.assertEqual(g.terminal_guard(tasks)['status'], 'failed')
        (tasks / g.TASK / 'result.json').write_text(json.dumps({'id': g.TASK, 'status': 'running'}))
        with self.assertRaisesRegex(ValueError, 'not terminal'):
            g.terminal_guard(tasks)
        (tasks / 'another').mkdir()
        with self.assertRaisesRegex(ValueError, 'new or missing'):
            g.terminal_guard(tasks)

    def test_generated_workload_is_owned_and_repeat_refuses(self):
        (self.source / 'src').mkdir()
        (self.source / 'dist').mkdir()
        (self.source / 'dist/old').write_text('old')
        g.prepare_workload(self.source, 'a' * 16)
        self.assertEqual(len(list((self.source / 'src/profile').glob('feature*.ts'))), 48)
        self.assertIn('z.array(Item).parse', (self.source / 'src/App.tsx').read_text())
        self.assertIn('127.0.0.1', (self.source / 'vite.config.ts').read_text())
        self.assertFalse((self.source / 'dist').exists())
        with self.assertRaises(FileExistsError):
            g.prepare_workload(self.source, 'a' * 16)


if __name__ == '__main__':
    unittest.main()
