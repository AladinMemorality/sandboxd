import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('prepare_api', Path(__file__).with_name('prepare-api.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class PreparationTests(unittest.TestCase):
    def test_exact_reviewed_resource_profile_only(self):
        source = 'MaxActive: 12, CPUCount: 2, MemoryMB: 2048\nreport := map[string]any{"existing_assertion": true}'
        got = module.bounded_fixture(source)
        self.assertIn('MaxActive: 4, CPUCount: 2, MemoryMB: 2048', got)
        self.assertIn('"existing_assertion": true', got)
        self.assertIn('"admission_max_active": 4', got)
        for changed in (source + source, source.replace('MaxActive: 12', 'MaxActive: 99')):
            with self.assertRaises(ValueError):
                module.bounded_fixture(changed)

    def test_copied_preflight_requires_complete_scan(self):
        source = Path(__file__).resolve().parents[1] / 'api_preflight.py'
        namespace = {'__name__': 'fixture_test'}
        exec(module.complete_inventory(source.read_text()), namespace)
        check = namespace['empty_cli_inventory']
        self.assertTrue(check('SANDBOX_COUNT 0\nNODES_SCANNED 1/1\n'))
        for bad in ('SANDBOX_COUNT 0', 'SANDBOX_COUNT 0\nNODES_SCANNED 0/1',
                    'SANDBOX_COUNT 1\nNODES_SCANNED 1/1',
                    'SANDBOX_COUNT 0\nNODES_SCANNED 1/1\nNODES_SCANNED 1/1'):
            self.assertFalse(check(bad))


if __name__ == '__main__':
    unittest.main()
