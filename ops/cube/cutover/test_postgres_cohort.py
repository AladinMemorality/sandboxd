import copy
import importlib.util
from pathlib import Path
import unittest
from unittest import mock

def load(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module

pc = load('postgres_cohort')
fixtures = load('test_cohort')


class PostgresCohortTests(unittest.TestCase):
    def config(self):
        value = fixtures.config(); value.update(version=2, parallelism=1)
        value['projects'][0].update(app_id=pc.pg.APP, sandbox_id=pc.pg.SANDBOX, preset='react-pro')
        return value

    def test_exact_project_and_dedicated_wave_required(self):
        value = self.config(); pc.validate_config(value)
        for field, replacement in [('parallelism', 2), ('version', 1)]:
            bad = copy.deepcopy(value); bad[field] = replacement
            with self.assertRaises(pc.c.b.Refused): pc.validate_config(bad)
        value['projects'][0]['app_id'] = fixtures.project()['app_id']
        with self.assertRaises(pc.c.b.Refused): pc.validate_config(value)

    def test_frozen_inventory_never_uses_socket_exception(self):
        host = pc.Host.__new__(pc.Host); host.pg_before = {}; host.pg_proof = {'clean': True}
        with mock.patch.object(pc.c.Host, 'inventory', side_effect=pc.c.b.Refused('special file')), mock.patch.object(pc.pg, 'live_socket_exception') as exception:
            with self.assertRaises(pc.c.b.Refused): host.inventory('frozen')
            exception.assert_not_called()

    def test_frozen_source_proof_cannot_change(self):
        host = pc.Host.__new__(pc.Host); host.pg_before = {}; host.pg_proof = {'hash': 'old'}
        with mock.patch.object(pc.c.Host, 'inventory'), mock.patch.object(pc.pg, 'stopped', return_value={'hash': 'changed'}):
            with self.assertRaisesRegex(pc.c.b.Refused, 'source changed'): host.inventory('frozen')

    def test_unrelated_online_failure_never_gets_socket_exception(self):
        host = pc.Host.__new__(pc.Host); host.c = self.config()
        row = host.c['projects'][0]
        report = {'identity_sha256': host.c['fleet_sha256'], 'projects': [{
            'sandbox_id': pc.pg.SANDBOX, 'target_preset': row['preset'], 'template_id': row['template_id'],
            'state': 'blocked', 'reasons': ['owner home manifest validation failed'],
            'inventory': {'home_report': {'reasons': ['wrong ownership']}}}]}
        host.run_cli = mock.Mock(return_value=report)
        with mock.patch.object(pc.pg, 'live_socket_exception') as exception:
            with self.assertRaisesRegex(pc.c.b.Refused, 'another eligibility failure'): host.inventory('online-preflight')
            exception.assert_not_called()


if __name__ == '__main__': unittest.main()
