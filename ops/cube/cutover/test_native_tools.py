import contextlib
import importlib.util
import json
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('native_cohort_tests', Path(__file__).with_name('test_cohort.py'))
fixtures = importlib.util.module_from_spec(spec); spec.loader.exec_module(fixtures)
c = fixtures.c


class NativeToolTests(unittest.TestCase):
    def host(self, phase='imported'):
        h = c.Host.__new__(c.Host)
        row = dict(fixtures.project(), sandbox_id=next(iter(c.native_tools.PYTHON)))
        db = sqlite3.connect(':memory:'); self.addCleanup(db.close)
        db.executescript('CREATE TABLE sandbox(id,app_id,runtime_provider); CREATE TABLE runtime_migration(sandbox_id,phase,runtime_id,template_id);')
        db.execute('INSERT INTO sandbox VALUES(?,?,?)', (row['sandbox_id'], row['app_id'], 'docker'))
        db.execute('INSERT INTO runtime_migration VALUES(?,?,?,?)', (row['sandbox_id'], phase, 'a'*32, row['template_id']))
        h.db = lambda: contextlib.nullcontext(db)
        h.fds = (); h.plan = {'files': {str(c.NATIVE_TOOLS): 'digest'}}
        for name in ('fence', 'source_fence', 'inputs', 'event'): setattr(h, name, mock.Mock())
        return h, row

    def test_target_must_be_imported_before_tool_execution(self):
        h, row = self.host('complete')
        with mock.patch.object(c.b, 'digest', return_value='digest'), mock.patch.object(c.subprocess, 'run') as run:
            with self.assertRaisesRegex(c.b.Refused, 'uncommitted target'): h.verify_native_tools(row)
            run.assert_not_called()

    def test_actual_failure_settles_but_transport_loss_requires_review(self):
        for status, error in [(1, c.NativeMigrationFailed), (255, c.b.Refused)]:
            h, row = self.host()
            with mock.patch.object(c.b, 'digest', return_value='digest'), mock.patch.object(c.b, 'trusted', return_value=b'probe'), mock.patch.object(c.subprocess, 'run', return_value=subprocess.CompletedProcess([], status, b'', b'')):
                with self.assertRaises(error): h.verify_native_tools(row)

    def test_exact_tool_proof_recorded(self):
        h, row = self.host()
        proof = {'sandbox_id': row['sandbox_id'], 'success': True, 'kind': 'python-pillow'}
        response = subprocess.CompletedProcess([], 0, ('CUBE_NATIVE_TOOL='+json.dumps(proof)+'\n').encode(), b'')
        with mock.patch.object(c.b, 'digest', return_value='digest'), mock.patch.object(c.b, 'trusted', return_value=b'probe'), mock.patch.object(c.subprocess, 'run', return_value=response):
            h.verify_native_tools(row)
            h.event.assert_called_with('native-tools-verified', proof)

    def test_failed_tool_never_resumes_or_commits_wave(self):
        with tempfile.TemporaryDirectory() as directory:
            h = c.Host.__new__(c.Host); h.c = fixtures.config(); h.job = Path(directory)
            row = fixtures.project()
            for name in ('fence', 'source_fence', 'inputs', 'event', 'reconcile_template_default'): setattr(h, name, mock.Mock())
            h.run_cli = mock.Mock(return_value={'success': True, 'projects': [{'sandbox_id': row['sandbox_id'], 'phase': 'imported'}]})
            h.verify_native_tools = mock.Mock(side_effect=c.NativeMigrationFailed('tool missing'))
            with self.assertRaises(c.NativeMigrationFailed): h.migrate_wave([row], 0)
            h.run_cli.assert_called_once()
            self.assertEqual(h.run_cli.call_args.args[0][-1], 'migrate')

    def test_chrome_uses_disposable_profile_and_local_document(self):
        module = c.native_tools
        with mock.patch.object(module.os, 'getuid', return_value=1000), mock.patch.object(module.os, 'getgid', return_value=1000), mock.patch.object(module.shutil, 'copytree') as copy, mock.patch.object(module.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, b'<html><body></body></html>', b'')) as run:
            proof = module.probe(next(iter(module.CHROME)))
            command = run.call_args.args[0]; options = run.call_args.kwargs
            self.assertEqual(command[-1], 'about:blank')
            self.assertIn('--disable-background-networking', command)
            self.assertIn('--user-data-dir=' + options['cwd'] + '/profile', command)
            self.assertEqual(options['env']['PYTHONDONTWRITEBYTECODE'], '1')
            self.assertEqual(options['env']['HOME'], options['cwd'])
            self.assertEqual(Path(command[0]).parent, Path(options['cwd']) / 'browser')
            copy.assert_called_once_with(Path('/home/sandbox/chrom-bin'), Path(options['cwd']) / 'browser', symlinks=True)
            self.assertTrue(proof['success'])


if __name__ == '__main__': unittest.main()
