import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec=importlib.util.spec_from_file_location('inventory',Path(__file__).with_name('inventory-cube-egress-clients.py'))
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class InventoryTests(unittest.TestCase):
    def test_capabilities_without_source_or_values(self):
        with tempfile.TemporaryDirectory() as root:
            path=Path(root)
            (path/'package.json').write_text(json.dumps({'dependencies':{'pg':'secret-version','axios':'1','better-sqlite3':'1'}}))
            (path/'index.ts').write_text('const secret="postgres://secret:password@private"; fetch(process.env.BRIDGE_URL);')
            report=module.inspect(str(Path(root).resolve()))
            self.assertEqual(report['raw_tcp_dependencies'],['pg'])
            self.assertTrue(report['requires_raw_client_review'])
            self.assertIn('bridge',report['source_capabilities'])
            self.assertNotIn('secret',json.dumps(report))
            self.assertNotIn('password',json.dumps(report))
    def test_no_symlink_or_dependency_tree_escape(self):
        with tempfile.TemporaryDirectory() as root, tempfile.TemporaryDirectory() as other:
            (Path(other)/'secret.ts').write_text('postgres://secret')
            (Path(root)/'linked').symlink_to(other,target_is_directory=True)
            modules=Path(root)/'node_modules';modules.mkdir();(modules/'ignore.ts').write_text('postgres://secret')
            report=module.inspect(str(Path(root).resolve()))
            self.assertEqual(report['files_read'],0)
            self.assertEqual(report['incomplete_reasons'],['symlink_not_followed'])
    def test_oversized_source_is_not_read(self):
        with tempfile.TemporaryDirectory() as root:
            (Path(root)/'big.ts').write_bytes(b'x'*(300*1024))
            report=module.inspect(str(Path(root).resolve()))
            self.assertEqual(report['bytes_read'],0)
            self.assertIn('unreadable_or_oversized_source',report['incomplete_reasons'])

if __name__=='__main__': unittest.main()
