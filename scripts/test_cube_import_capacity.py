import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('capacity', Path(__file__).with_name('check-cube-import-capacity.py'))
capacity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(capacity)


class CapacityTests(unittest.TestCase):
    def test_floor_is_available_bytes_not_total_disk(self):
        with self.assertRaises(ValueError):
            capacity.checked_capacity(capacity.MIN_FREE_BYTES - 1, 'xfs', '/data')
        result = capacity.checked_capacity(capacity.MIN_FREE_BYTES, 'xfs', '/data')
        self.assertTrue(result['eligible'])
        self.assertFalse(result['reservation'])

    def test_wrong_filesystem_cannot_supply_false_headroom(self):
        for fs, mount in [('ext4', '/data'), ('xfs', '/'), ('overlay', '/data')]:
            with self.assertRaises(ValueError):
                capacity.checked_capacity(1024 ** 4, fs, mount)

    def test_mount_must_be_exact_and_unambiguous(self):
        row = '34 22 8:16 / /data rw - xfs /dev/vdb rw\n'
        self.assertEqual(capacity.data_mount_info(row), ('xfs', '/data'))
        for invalid in ['', row + row, row.replace('/data', '/data/other')]:
            with self.assertRaises(ValueError):
                capacity.data_mount_info(invalid)


if __name__ == '__main__':
    unittest.main()
