import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('operator_run', Path(__file__).with_name('run.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class ReviewedHelperTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.helper = self.root / 'execute.py'
        self.helper.write_text('# fixed source')
        self.helper.chmod(0o644)

    def test_fixed_readonly_public_source_is_supported(self):
        self.assertEqual(module.reviewed_helper(self.helper, os.getuid()), self.helper)

    def test_symlink_refused(self):
        link = self.root / 'link'
        link.symlink_to(self.helper)
        with self.assertRaises(ValueError): module.reviewed_helper(link, os.getuid())

    def test_wrong_owner_or_writable_source_refused(self):
        with self.assertRaises(ValueError): module.reviewed_helper(self.helper, os.getuid()+1)
        self.helper.chmod(0o666)
        with self.assertRaises(ValueError): module.reviewed_helper(self.helper, os.getuid())

    def test_hardlinked_source_refused(self):
        os.link(self.helper, self.root / 'second')
        with self.assertRaises(ValueError): module.reviewed_helper(self.helper, os.getuid())

if __name__ == '__main__': unittest.main()
