import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('template_defaults', Path(__file__).with_name('template_defaults.py'))
d = importlib.util.module_from_spec(spec); spec.loader.exec_module(d)


class TemplateDefaultTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(); self.addCleanup(temp.cleanup)
        self.root = Path(temp.name).resolve(); self.target = self.root / '.bash_logout'
        self.target.write_bytes(d.STOCK); self.target.chmod(0o644)
        self.owner = self.root / 'owner-file'; self.owner.write_bytes(b'unchanged owner data')
    def reconcile(self): return d.reconcile(self.root, os.getuid(), os.getgid())
    def test_exact_stock_removed_and_absence_is_replayable(self):
        result = self.reconcile(); self.assertTrue(result['removed']); self.assertEqual(result['sha256'], d.STOCK_SHA256)
        self.assertFalse(self.target.exists()); self.assertFalse(self.reconcile()['removed'])
        self.assertEqual(self.owner.read_bytes(), b'unchanged owner data')
    def test_changed_same_size_script_never_removed(self):
        changed = b'x' + d.STOCK[1:]; self.target.write_bytes(changed)
        with self.assertRaisesRegex(RuntimeError, 'content differs'): self.reconcile()
        self.assertEqual(self.target.read_bytes(), changed)
    def test_link_directory_mode_owner_and_noncanonical_home_refused(self):
        for kind in ('hardlink', 'symlink', 'directory', 'mode', 'owner', 'parent'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temp:
                root = Path(temp).resolve(); target = root / '.bash_logout'
                target.write_bytes(d.STOCK); target.chmod(0o644)
                home = root; uid = os.getuid()
                if kind == 'hardlink': os.link(target, root / 'second-link')
                elif kind == 'symlink': target.unlink(); target.symlink_to(self.owner)
                elif kind == 'directory': target.unlink(); target.mkdir()
                elif kind == 'mode': target.chmod(0o600)
                elif kind == 'owner': uid += 1
                else: home = root / 'alias'; home.symlink_to(root, target_is_directory=True)
                with self.assertRaises(RuntimeError): d.reconcile(home, uid, os.getgid())
                self.assertTrue(target.exists() or target.is_symlink())
        self.assertEqual(self.owner.read_bytes(), b'unchanged owner data')


if __name__ == '__main__': unittest.main()
