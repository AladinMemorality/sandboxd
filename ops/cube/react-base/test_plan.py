import copy
import json
import unittest
from plan import plan, budget_contracts


class CutoverPlan(unittest.TestCase):
    def setUp(self):
        self.contract = {'node_id': 'worker-a', 'max_active': 5, 'templates': {'tpl-old': {'cpu_count': 2, 'memory_mb': 2048}}, 'resource_budget': {'profiles': {'tpl-old': {'cpu_millis': 500, 'writable_disk_mb': 10240, 'kind': 'runtime'}}}}
        self.environment = {'SANDBOXD_CUBE_TEMPLATES': json.dumps({'react-pro': 'tpl-old', 'nextjs': 'tpl-next'}), 'SANDBOXD_CUBE_ADMISSION': json.dumps(self.contract), 'SANDBOXD_CUBE_FLEET': json.dumps({'workers': [{'id': 'vps', 'admission': self.contract, 'draining': False}]})}
        self.release = {'replacements': {'react-pro': {'previous_template': 'tpl-old', 'template_id': 'tpl-new', 'cpu_count': 2, 'memory_mb': 2048, 'writable_disk_mb': 10240, 'ready_nodes': ['worker-a'], 'canary_passed': True}}}

    def test_preserves_old_guests_other_presets_and_capacity(self):
        original = copy.deepcopy(self.environment)
        overlay = plan(self.environment, self.release)['services']['sandboxd']['environment']
        self.assertEqual(self.environment, original)
        self.assertEqual(json.loads(overlay['SANDBOXD_CUBE_TEMPLATES']), {'react-pro': 'tpl-new', 'nextjs': 'tpl-next'})
        updated = json.loads(overlay['SANDBOXD_CUBE_ADMISSION'])
        self.assertEqual(updated['max_active'], 5)
        self.assertEqual(updated['templates']['tpl-new'], updated['templates']['tpl-old'])
        self.assertEqual(updated['resource_budget']['profiles']['tpl-new'], updated['resource_budget']['profiles']['tpl-old'])

    def test_refuses_stale_unverified_or_differently_sized_release(self):
        for key, value in [('previous_template', 'tpl-stale'), ('canary_passed', False), ('ready_nodes', []), ('memory_mb', 4096), ('writable_disk_mb', 20480)]:
            release = copy.deepcopy(self.release)
            release['replacements']['react-pro'][key] = value
            with self.assertRaises(ValueError): plan(self.environment, release)

    def test_durable_budget_extends_same_profiles_and_rejects_drift(self):
        self.contract['resource_budget'].update(cpu_millis=9000, memory_mb=46080, runtime_slots=50, build_slots=2)
        fleet = {'workers': [{'id': 'vps', 'admission': self.contract, 'draining': False}]}
        self.environment['SANDBOXD_CUBE_FLEET'] = json.dumps(fleet)
        candidate = plan(self.environment, self.release)['services']['sandboxd']['environment']
        raw = '{"budget":{"cpu_millis":9000,"memory_mb":46080,"runtime_slots":50,"build_slots":2,"profiles":{"tpl-old":{"cpu_millis":500,"writable_disk_mb":10240,"kind":"runtime"}}},"templates":{"tpl-old":{"cpu_count":2,"memory_mb":2048}}}'
        changed = json.loads(budget_contracts(self.environment, candidate, {'vps': raw})['vps'])
        self.assertEqual(changed['budget']['runtime_slots'], 50)
        self.assertEqual(changed['budget']['profiles']['tpl-new'], changed['budget']['profiles']['tpl-old'])
        self.assertEqual(changed['templates']['tpl-new'], changed['templates']['tpl-old'])
        with self.assertRaises(AssertionError): budget_contracts(self.environment, candidate, {'vps': raw.replace('9000', '10000')})
        with self.assertRaises(AssertionError): budget_contracts(self.environment, candidate, {})


if __name__ == '__main__': unittest.main()
