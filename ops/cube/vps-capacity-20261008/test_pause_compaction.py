"""Exercise immutable snapshot pinning against concurrent snapshot retirement."""
import ast
import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

TREE = ast.parse(Path(__file__).with_name('compact-vps-pause-memory.py').read_text())
INNER = next(ast.literal_eval(n.value) for n in TREE.body
             if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == 'INNER' for t in n.targets))


class SnapshotPinning(unittest.TestCase):
    def fixture(self, directory):
        root = Path(directory).resolve()
        machine = root / 'machine-id'
        machine.write_text('2b9e31d4abd345e3bd4b966591e61296')
        sid = 'snap-' + 'a' * 24
        name = 'tpl-' + sid + '-memory'
        original = root / 'xfs/objects/volumes/template' / name
        original.parent.mkdir(parents=True)
        original.write_bytes(b'checkpoint' * 8192)
        catalog = root / 'xfs/pause-snapshots' / sid / 'metadata/catalog.json'
        catalog.parent.mkdir(parents=True)
        catalog.write_text(json.dumps(dict(snapshot_id=sid, kind='pause_snapshot', backend='xfs', memory_kind='snapshot', memory_vol=name)))
        code = INNER.replace("'/data/cubelet/storage/xfs'", repr(str(root / 'xfs'))).replace("'/etc/machine-id'", repr(str(machine))).replace("'/data/baarcha-memory-dedupe'", repr(str(root / 'dedupe'))).replace("os.statvfs('/data')", 'os.statvfs('+repr(str(root))+')').replace('s.st_uid==0', 's.st_uid==os.getuid()')
        scope = [dict(snapshot_id=sid, memory_bytes=original.stat().st_size)]
        return code, scope, original

    def run_worker(self, code, scope):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            exec(compile(code, 'inner-worker', 'exec'), dict(SCOPE=scope, GENERATION='test', BATCH=0))
        return json.loads(output.getvalue())

    def test_gc_after_pin_keeps_bytes_and_releases_pin(self):
        with tempfile.TemporaryDirectory() as directory:
            code, scope, original = self.fixture(directory)
            def retire(*args, **kwargs):
                if original.exists(): original.unlink()
            with patch('subprocess.run', side_effect=retire):
                result = self.run_worker(code, scope)
            self.assertEqual(result['count'], 1)
            self.assertTrue(result['contents_identical'])
            self.assertFalse(original.exists())
            self.assertFalse(list(Path(directory).rglob('memory-000')))

    def test_gc_before_pin_skips_without_recreating_source(self):
        with tempfile.TemporaryDirectory() as directory:
            code, scope, original = self.fixture(directory)
            def retire(*args, **kwargs):
                original.unlink()
                raise FileNotFoundError('retired')
            with patch('os.link', side_effect=retire), patch('subprocess.run') as operation:
                result = self.run_worker(code, scope)
            self.assertEqual(result['count'], 0)
            self.assertEqual(result['retired_before_open'], [scope[0]['snapshot_id']])
            operation.assert_not_called()
            self.assertFalse(original.exists())

    def test_content_change_fails_and_retains_review_pin(self):
        with tempfile.TemporaryDirectory() as directory:
            code, scope, original = self.fixture(directory)
            def corrupt(*args, **kwargs):
                with original.open('r+b') as handle: handle.write(b'changed')
            with patch('subprocess.run', side_effect=corrupt), self.assertRaises(AssertionError):
                self.run_worker(code, scope)
            self.assertTrue(list(Path(directory).rglob('memory-000')))
            self.assertFalse(list(Path(directory).rglob('complete.json')))


if __name__ == '__main__':
    unittest.main()
