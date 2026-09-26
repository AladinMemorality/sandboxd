import copy
import json
import tempfile
import importlib.util
from pathlib import Path
import subprocess
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('pg_verify', Path(__file__).with_name('verify.py'))
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)


def proof():
    return {'tables': {name: {'count': 2, 'sha256': '1' * 64} for name in
                      ('waitlist', 'platform_session', 'published_app', 'upload', 'schema_migrations')},
            'fixture_owners': 2,
            'canary': {'project_id': verify.APP, 'owner': 103, 'visibility': 'private', 'cover_id': 'owned-image',
                       'upload_owner': 103, 'upload_project': verify.APP, 'upload_sha256': '2' * 64,
                       'upload_bytes': 86634, 'upload_storage': 's3', 'upload_deleted': None}}


class PGRestoreProof(unittest.TestCase):
    def test_command_has_no_external_network_mounts_or_socket(self):
        args = verify.create_args('cube-pair-platform-pg-owned', '1' * 16)
        for flag in ('--network=none', '--read-only', '--user=70:70', '--cpus=1', '--memory=512m',
                     '--memory-swap=512m', '--pids-limit=128', '--pull=never', '--cap-drop=ALL'):
            self.assertIn(flag, args)
        self.assertFalse(any(x in args for x in ('--mount', '-v', '--volume', '--privileged', '-p', '--publish')))
        self.assertIn(verify.IMAGE, args)
        self.assertIn('listen_addresses=', args)
        self.assertEqual(args.count('--tmpfs'), 3)
        mounts = [args[i + 1].split(':', 1)[0] for i, arg in enumerate(args) if arg == '--tmpfs']
        self.assertIn('/var/lib/postgresql/data', mounts)
        self.assertNotIn('/var/lib/postgresql', mounts)

    def test_native_docker_endpoint_cannot_follow_ambient_remote_context(self):
        response = subprocess.CompletedProcess([], 0, b'ok', b'')
        with mock.patch.object(verify.subprocess, 'run', return_value=response) as run:
            verify.command(['docker', 'image', 'inspect', verify.IMAGE])
        self.assertEqual(run.call_args.args[0][:2], ['docker', '--host=unix:///var/run/docker.sock'])

    def test_real_restore_fingerprints_and_private_scope_required(self):
        expected = proof()
        verify.validate_proof(expected, copy.deepcopy(expected))
        for field, value in [('owner', 104), ('visibility', 'public'), ('upload_owner', 104),
                             ('upload_project', 'other'), ('upload_deleted', 'deleted'), ('upload_bytes', 0)]:
            changed = copy.deepcopy(expected)
            changed['canary'][field] = value
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                verify.validate_proof(changed, changed)
        changed = copy.deepcopy(expected)
        changed['tables']['platform_session']['sha256'] = '3' * 64
        with self.assertRaises(RuntimeError): verify.validate_proof(expected, changed)
        changed = copy.deepcopy(expected)
        del changed['tables']['platform_session']
        with self.assertRaises(RuntimeError): verify.validate_proof(changed, changed)

    def test_cleanup_inspection_requires_exact_owned_id_image_label_and_limits(self):
        row = {'Id': '1' * 64, 'Name': '/own', 'Image': 'sha256:reviewed',
               'Config': {'Labels': {verify.LABEL: 'reviewed'}},
               'HostConfig': {'NetworkMode': 'none', 'PortBindings': {}, 'ReadonlyRootfs': True,
                              'Memory': 512 << 20, 'MemorySwap': 512 << 20, 'NanoCpus': 1_000_000_000,
                              'PidsLimit': 128, 'Binds': [], 'Privileged': False, 'PidMode': '',
                              'CapDrop': ['ALL'], 'SecurityOpt': ['no-new-privileges:true']}, 'Mounts': []}
        import json
        def call(value):
            response = subprocess.CompletedProcess([], 0, json.dumps([value]).encode(), b'')
            with mock.patch.object(verify, 'command', return_value=response):
                return verify.inspect_owned('1' * 64, 'own', 'reviewed', 'sha256:reviewed')
        self.assertEqual(call(row), row)
        for name in ('Id', 'Name', 'Image'):
            changed = copy.deepcopy(row)
            changed[name] = 'other'
            with self.assertRaises(RuntimeError): call(changed)
        changed = copy.deepcopy(row)
        changed['Mounts'] = [{'Type': 'volume', 'Destination': '/var/lib/postgresql/data', 'Name': 'unexpected'}]
        with self.assertRaises(RuntimeError): call(changed)
        for field, value in [('NetworkMode', 'host'), ('Memory', 0), ('Privileged', True), ('CapDrop', []),
                             ('Binds', ['/var/run/docker.sock:/var/run/docker.sock'])]:
            changed = copy.deepcopy(row)
            changed['HostConfig'][field] = value
            with self.assertRaises(RuntimeError): call(changed)

    def test_private_diagnostics_survive_failed_status_and_json(self):
        for response in (subprocess.CompletedProcess([], 1, b'', b'bounded SQL error'),
                         subprocess.CompletedProcess([], 0, b'not json', b'')):
            with self.subTest(status=response.returncode), tempfile.TemporaryDirectory() as td:
                with self.assertRaises((RuntimeError, json.JSONDecodeError)):
                    verify.private_query_result(Path(td), 'actual-provenance', response)
                path = Path(td) / 'actual-provenance.PRIVATE.json'
                saved = json.loads(path.read_text())
                self.assertEqual(saved['returncode'], response.returncode)
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertEqual(saved['stdout_bytes'], len(response.stdout))
        with tempfile.TemporaryDirectory() as td:
            response = subprocess.CompletedProcess([], 0, b'{}', b'x' * 65537)
            with self.assertRaises(RuntimeError): verify.private_query_result(Path(td), 'actual-provenance', response)
            saved = json.loads((Path(td) / 'actual-provenance.PRIVATE.json').read_text())
            self.assertTrue(saved['truncated'])
            self.assertEqual(len(saved['stderr']), 65536)

    def test_comparison_diagnostics_do_not_relax_validation(self):
        expected = proof()
        actual = copy.deepcopy(expected)
        actual['tables']['upload']['sha256'] = '3' * 64
        differences = verify.proof_difference(expected, actual)
        self.assertFalse(differences['tables_equal']['upload'])
        self.assertTrue(differences['canary_equal'])
        with self.assertRaises(RuntimeError): verify.validate_proof(expected, actual)
        self.assertIn('BEGIN READ ONLY', verify.DIAGNOSTIC_SQL)
        for field in ('server_version', 'database_collate', 'database_ctype', 'datlocprovider', 'datcollversion'):
            self.assertIn(field, verify.DIAGNOSTIC_SQL)

    def test_query_reports_only_hashes_counts_and_owned_metadata(self):
        query = Path(__file__).with_name('provenance.sql').read_text()
        self.assertIn('sha256(convert_to', query)
        self.assertIn('ORDER BY token_hash', query)
        self.assertNotIn('jsonb_agg', query)
        source = Path(__file__).with_name('capture.mjs').read_text()
        self.assertIn('repeatable read read only', source)
        self.assertIn('pg_export_snapshot()', source)
        self.assertIn("'--snapshot='+meta.snapshot", source)
        self.assertNotIn('UPDATE ', source)
        self.assertNotIn('INSERT ', source)


if __name__ == '__main__': unittest.main()
