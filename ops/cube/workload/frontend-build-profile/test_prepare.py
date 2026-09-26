import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('frontend_prepare', Path(__file__).with_name('prepare.py'))
p = importlib.util.module_from_spec(spec)
spec.loader.exec_module(p)


class PlanTests(unittest.TestCase):
    def test_preserves_existing_web_pg_build_bytes(self):
        original = 'version: 1\nweb:\n  command: node server.mjs\nworkers:\n  - name: postgres\n    command: node /opt/services/postgres/worker.mjs\nbuild:\n  command: node --check server.mjs\n'
        replacement = p.candidate(original, 'a' * 16)
        addition = '  - name: operator_frontend_profile\n    command: ' + p.command('a' * 16) + '\n'
        self.assertEqual(replacement.replace(addition, ''), original)
        self.assertLess(replacement.index(addition), replacement.index('build:'))

    def test_empty_worker_list_and_unsupported_layout(self):
        self.assertIn('workers:\n  - name:', p.candidate('version: 1\nworkers: []\n', 'a' * 16))
        for original in ['workers: [ {name: pg} ]\n', 'workers:\nworkers:\n', '---\nversion: 1\n', 'workers:\n\t- name: pg\n']:
            with self.assertRaises(ValueError):
                p.candidate(original, 'a' * 16)
        with self.assertRaises(ValueError):
            p.command('../../evil')

    def test_validated_services_must_be_exactly_preserved(self):
        before = {'valid': True, 'effective': {'web': {'command': 'node server.mjs', 'port': 3000}, 'workers': [{'name': 'postgres', 'command': 'pg'}]}}
        after = {'valid': True, 'effective': {'web': before['effective']['web'], 'workers': before['effective']['workers'] + [{'name': p.WORKER, 'command': p.command('a' * 16)}]}}
        p.verify_validations(before, after, 'a' * 16)
        after['effective']['workers'][0] = {'name': 'postgres', 'command': 'wrong'}
        with self.assertRaisesRegex(ValueError, 'existing service'):
            p.verify_validations(before, after, 'a' * 16)

    def test_restore_is_exact_and_rejects_concurrent_edit(self):
        original, installed = b'original\n', b'candidate\n'
        digest = p.hashlib.sha256(installed).hexdigest()
        self.assertEqual(p.verify_restore(installed, original, digest), original)
        with self.assertRaisesRegex(ValueError, 'drift'):
            p.verify_restore(b'new customer content', original, digest)


if __name__ == '__main__':
    unittest.main()
