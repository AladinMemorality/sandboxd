import hashlib
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('readiness', Path(__file__).with_name('production-readiness.py'))
readiness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(readiness)


class ReadinessTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.runtime = {'SANDBOXD_CUBE_ENABLED': 'true', 'SANDBOXD_CUBE_ROLLOUT': 'global',
                        'SANDBOXD_CUBE_AGENT_RELAY_ORIGIN': 'https://relay.example',
                        'SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED': 'true',
                        'SANDBOXD_CUBE_API_KEY': 'DO-NOT-PRINT', 'SANDBOXD_PREVIEW_TOKEN_SECRETS': 'ANOTHER-SECRET',
                        'SANDBOXD_API_AUTH_DISABLED': 'false',
                        'SANDBOXD_CUBE_TEMPLATES': json.dumps({name: 'template-' + name for name in readiness.PRESETS})}
        for key in ('SANDBOXD_CUBE_API_URL', 'SANDBOXD_CUBE_PROXY_URL', 'SANDBOXD_AGENT_PROXY_URL'):
            self.runtime[key] = 'http://127.0.0.1:9090'
        self.platform = {'BRIDGE_PUBLIC_URL': 'https://app.example/api/bridge',
                         'SANDBOXD_PREVIEW_ORIGIN': 'https://%ID%.preview.example',
                         'CAPTURE_BACKEND': 'service', 'CAPTURE_SERVICE_SOCKET': '/run/capture.sock', 'CAPTURE_DENY_CIDRS': '8.8.8.8/32'}
        self.plan = {'protected_addresses': ['8.8.8.8'], 'capacity': {
            'host_memory_mib': 16384, 'reserved_memory_mib': 2048, 'running_guests': 4,
            'peak_waking_guests': 2, 'guest_memory_mib': 1024, 'free_storage_bytes': 100000,
            'project_export_bytes': 100, 'rollback_reserve_bytes': 100, 'backup_staging_bytes': 100,
            'planned_snapshot_growth_bytes': 100}, 'evidence': {}}

    def check(self):
        return readiness.audit(self.runtime, self.platform, self.plan, self.base)

    def test_boolean_and_complete_artifact_inventory_cannot_enable_networking(self):
        path = self.base / 'review.txt'
        path.write_text('Fixture review document, not a production attestation.')
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        self.plan['evidence'] = {name: {'path': path.name, 'sha256': digest} for name in readiness.EVIDENCE}
        result = self.check()
        self.assertFalse(result['authorizes_rollout'])
        self.assertFalse(result['network_actions_performed'])
        self.assertEqual([value['code'] for value in result['findings']], ['guest_egress_unavailable'])
        self.assertEqual(len(result['evidence_attachments']), len(readiness.EVIDENCE))
        self.assertNotIn('DO-NOT-PRINT', json.dumps(result))
        self.assertNotIn('ANOTHER-SECRET', json.dumps(result))

    def test_resource_shortage_and_unsupported_agent_are_reported(self):
        self.plan['capacity']['host_memory_mib'] = 1024
        self.plan['capacity']['free_storage_bytes'] = 100
        self.platform['SANDBOXD_AGENT'] = 'opencode'
        codes = {entry['code'] for entry in self.check()['findings']}
        self.assertTrue({'memory_overcommitted', 'storage_overcommitted', 'unsupported_model_agent'} <= codes)

    def test_artifact_escape_digest_mismatch_and_booleans_are_not_evidence(self):
        self.plan['evidence'] = {'network-isolation': True, 'backup-restore': {'path': '../outside', 'sha256': 'a' * 64}}
        path = self.base / 'evidence.txt'; path.write_text('different bytes')
        self.plan['evidence']['worker-recovery'] = {'path': path.name, 'sha256': '0' * 64}
        codes = {entry['code'] for entry in self.check()['findings']}
        self.assertTrue({'evidence_network-isolation', 'evidence_backup-restore', 'evidence_worker-recovery'} <= codes)

    def test_missing_template_and_public_management_exclusion_are_visible(self):
        self.runtime['SANDBOXD_CUBE_TEMPLATES'] = '{}'
        self.platform['CAPTURE_DENY_CIDRS'] = '192.168.0.0/16'
        self.runtime['SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS'] = 'registry.npmjs.org'
        codes = {entry['code'] for entry in self.check()['findings']}
        self.assertTrue({'missing_templates', 'capture_management_exclusion_missing', 'unsupported_domain_allowance'} <= codes)

    def test_dotenv_is_data_and_unrelated_secrets_are_not_loaded(self):
        path = self.base / 'config.env'
        path.write_text('UNRELATED_SECRET=do-not-read\nexport SANDBOXD_AGENT="claude-code"\nSANDBOXD_CUBE_API_KEY=$(touch SHOULD_NOT_EXIST)\n')
        config = readiness.read_config(path)
        self.assertNotIn('UNRELATED_SECRET', config)
        self.assertEqual(config['SANDBOXD_AGENT'], 'claude-code')
        self.assertEqual(config['SANDBOXD_CUBE_API_KEY'], '$(touch SHOULD_NOT_EXIST)')
        self.assertFalse((self.base / 'SHOULD_NOT_EXIST').exists())

    def test_malformed_nested_inventory_is_reported_without_crashing(self):
        self.plan['capacity'] = []
        self.plan['protected_addresses'] = None
        codes = {entry['code'] for entry in self.check()['findings']}
        self.assertTrue({'capacity_inventory_missing', 'invalid_protected_addresses'} <= codes)
        self.assertFalse(readiness.valid_url('https://bad host.example'))
        self.assertFalse(readiness.valid_url('https://example.com\\@internal'))

    def test_mapped_ipv4_cannot_bypass_public_management_inventory(self):
        self.plan['protected_addresses'] = ['::ffff:8.8.8.8']
        self.platform['CAPTURE_DENY_CIDRS'] = '192.168.0.0/16'
        codes = {entry['code'] for entry in self.check()['findings']}
        self.assertIn('capture_management_exclusion_missing', codes)

    def test_input_reads_reject_oversize_and_nonregular_files(self):
        path = self.base / 'large'; path.write_bytes(b'a' * 101)
        with self.assertRaises(ValueError): readiness.read_bounded(path, 100)
        path = self.base / 'fifo'; os.mkfifo(path)
        with self.assertRaises(ValueError): readiness.read_bounded(path, 100)

    def test_expanded_capture_pool_is_included_in_memory_budget(self):
        before = self.check()['resource_summary']['minimum_planned_memory_mib']
        self.plan['capacity']['capture_workers'] = 8
        self.assertEqual(self.check()['resource_summary']['minimum_planned_memory_mib'], before + 6 * 768)


    def test_shared_capture_does_not_require_replacement_service(self):
        self.platform.pop('CAPTURE_BACKEND')
        self.platform.pop('CAPTURE_SERVICE_SOCKET')
        self.plan['capacity']['capture_memory_mib'] = 1024
        result = self.check()
        codes = {entry['code'] for entry in result['findings']}
        self.assertNotIn('capture_socket_missing', codes)
        self.assertNotIn('capacity_inventory_missing', codes)
        self.assertEqual(result['resource_summary']['capture_backend'], 'shared')
        self.assertEqual(result['resource_summary']['capture_workers'], 0)
        self.assertEqual(result['resource_summary']['minimum_planned_memory_mib'], 2048 + 6 * 1024 + 1024)
        self.assertFalse(result['authorizes_rollout'])
        self.platform['CAPTURE_DENY_CIDRS'] = ''
        self.assertIn('capture_management_exclusion_missing', {entry['code'] for entry in self.check()['findings']})

    def test_selected_service_and_shared_capture_require_their_own_resources(self):
        self.platform.pop('CAPTURE_SERVICE_SOCKET')
        self.assertIn('capture_socket_missing', {entry['code'] for entry in self.check()['findings']})
        self.platform['CAPTURE_BACKEND'] = 'shared'
        self.assertIn('capacity_inventory_missing', {entry['code'] for entry in self.check()['findings']})
        self.platform['CAPTURE_BACKEND'] = 'typo'
        self.assertIn('capture_backend_invalid', {entry['code'] for entry in self.check()['findings']})


if __name__ == '__main__':
    unittest.main()
