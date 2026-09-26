import copy
import importlib.util
import json
import os
import stat
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('cutover', Path(__file__).with_name('run.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
spec2 = importlib.util.spec_from_file_location('routing_candidate', Path(__file__).with_name('routing_candidate.py'))
routing = importlib.util.module_from_spec(spec2)
spec2.loader.exec_module(routing)


def candidate():
    guard = {'expected_boot_id': 'reviewed-boot', 'observation_path': '/run/sandboxd-cube-storage/observation.json'}
    admission = {'max_active': 4, 'cpu_count': 2, 'memory_mb': 2048, 'writable_disk_mb': 10240, 'storage_guard': guard}
    env = {'SANDBOXD_PREVIEW_TOKEN_SECRETS': 'fixture='+'a'*32, 'SANDBOXD_CUBE_ENABLED': 'true', 'SANDBOXD_CUBE_REVERSE_EGRESS': 'true', 'SANDBOXD_CUBE_ROLLOUT': 'allowlist', 'SANDBOXD_CUBE_APP_IDS': 'owned', 'SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED': 'true', 'SANDBOXD_CUBE_ADMISSION': json.dumps(admission)}
    relay = {'image': 'sha256:relay', 'network_mode': 'service:sandboxd', 'read_only': True, 'pull_policy': 'never', 'user': '65532:65532', 'userns_mode': 'host', 'group_add': ['982'], 'cap_drop': ['ALL'], 'security_opt': ['no-new-privileges:true'], 'volumes': [{'type': 'bind', 'source': '/run/cube-management', 'target': '/run/cube-management', 'read_only': True, 'bind': {'create_host_path': False}}]}
    return {'services': {'sandboxd': {'environment': env, 'volumes': [{'source': '/run/sandboxd-cube-storage', 'target': '/run/sandboxd-cube-storage', 'read_only': True}]}, **{k: copy.deepcopy(relay) for k in m.SERVICES[1:]}}}, {'allowed_app_id': 'owned', 'worker_boot_id': 'reviewed-boot', 'relay_image': 'sha256:relay'}


class CutoverTests(unittest.TestCase):
    def test_routing_mode_contract_is_separate_from_private_metadata(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp).resolve()
            target = base / 'drain.json'
            target.write_bytes(b'original')
            actual_stat = os.stat
            # Unit fixtures are owned by the test user on non-root CI. Simulate
            # ONLY their reviewed root ownership; preserve real modes/inodes.
            def root_stat(path, *args, **kwargs):
                value = actual_stat(path, *args, **kwargs)
                fields = list(value)
                fields[4] = 0
                return os.stat_result(fields)
            with patch.object(m, 'ROUTING', base), patch.object(os, 'stat', root_stat):
                for mode in (0o600, 0o644):
                    target.chmod(mode)
                    m.routing_file(target)
                    m.install_bytes(target, b'replacement', routing=True)
                    self.assertEqual(target.read_bytes(), b'replacement')
                    self.assertEqual(stat.S_IMODE(target.stat().st_mode), mode)
                with self.assertRaisesRegex(RuntimeError, 'unsafe private input'):
                    m.private(target)
                with self.assertRaisesRegex(RuntimeError, 'unsafe private input'):
                    m.install_bytes(target, b'must-not-write')
                for mode in (0o666, 0o640, 0o664, 0o1600):
                    target.chmod(mode)
                    with self.assertRaisesRegex(RuntimeError, 'unsafe routing input'):
                        m.routing_file(target)
                target.chmod(0o644)
                alias = base / 'offline.json'
                alias.symlink_to(target)
                with self.assertRaisesRegex(RuntimeError, 'noncanonical routing'):
                    m.routing_file(alias)
                alias.unlink()
                os.link(target, alias)
                with self.assertRaisesRegex(RuntimeError, 'unsafe routing'):
                    m.routing_file(target)
                alias.unlink()
                with self.assertRaisesRegex(RuntimeError, 'unexpected maintenance'):
                    m.routing_file(base / 'secrets.key')

    def test_preview_key_dependency_fails_closed_without_secret_disclosure(self):
        value, config = candidate()
        for bad in ('', 'broken', 'v1=', 'v1=short', 'v1='+'a'*32+',v1='+'b'*32):
            value['services']['sandboxd']['environment']['SANDBOXD_PREVIEW_TOKEN_SECRETS'] = bad
            with self.assertRaisesRegex(RuntimeError, 'preview signing'):
                m.check_candidate(value, config)
        value['services']['sandboxd']['environment']['SANDBOXD_PREVIEW_TOKEN_SECRETS'] = 'v1='+'a'*32
        m.check_candidate(value, config)

    def test_added_live_alias_id_preserved_and_fenced(self):
        def config(routes): return {'apps': {'http': {'servers': {'srv0': {'routes': routes}}}}}
        preview = {'match': [{'host': ['*.preview.65.108.225.153.sslip.io']}], 'handle': [{'handler': 'reverse_proxy'}]}
        old = config([preview])
        alias = {'@id': 'baarcha-motion-studio-preview', 'match': [{'host': ['owned.preview.example']}], 'handle': [{'handler': 'reverse_proxy', 'upstreams': [{'dial': '127.0.0.1:8091'}]}]}
        live = config([alias, preview])
        offline = config([dict(preview, handle=[{'handler': 'static_response', 'status_code': 503}])])
        drain, fenced = routing.extend_alias(old, live, old, offline, 'owned.preview.example')
        self.assertEqual(drain, live)
        out = fenced['apps']['http']['servers']['srv0']['routes'][0]
        self.assertEqual(out['@id'], alias['@id'])
        self.assertEqual(out['match'], alias['match'])
        self.assertEqual(out['handle'], [{'handler': 'static_response', 'status_code': 503}])
        self.assertEqual(live['apps']['http']['servers']['srv0']['routes'][0], alias)
        live['other_change'] = True
        with self.assertRaises(ValueError):
            routing.extend_alias(old, live, old, offline, 'owned.preview.example')

    def test_actual_allowlist_names_and_guard(self):
        value, config = candidate()
        self.assertEqual(m.check_candidate(value, config)['max_active'], 4)
        for key, bad in [('SANDBOXD_CUBE_ROLLOUT', 'global'), ('SANDBOXD_CUBE_APP_IDS', 'owned,other'), ('SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS', 'registry.npmjs.org'), ('SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED', 'false')]:
            with self.subTest(key=key):
                changed = copy.deepcopy(value)
                changed['services']['sandboxd']['environment'][key] = bad
                with self.assertRaises(RuntimeError):
                    m.check_candidate(changed, config)

    def test_obsolete_allowlist_names_do_not_authorize(self):
        value, config = candidate()
        env = value['services']['sandboxd']['environment']
        del env['SANDBOXD_CUBE_APP_IDS']
        env['SANDBOXD_CUBE_APP_ALLOWLIST'] = 'owned'
        with self.assertRaises(RuntimeError):
            m.check_candidate(value, config)

    def test_namespace_privileges_ports_and_mounts(self):
        value, config = candidate()
        changes = [('network_mode', 'host'), ('ports', [20300]), ('user', 'root'), ('group_add', ['0']), ('cap_drop', []), ('read_only', False), ('volumes', [])]
        for key, bad in changes:
            with self.subTest(key=key):
                changed = copy.deepcopy(value)
                changed['services']['cube-management-api'][key] = bad
                with self.assertRaises(RuntimeError):
                    m.check_candidate(changed, config)
        value['services']['sandboxd']['volumes'][0]['read_only'] = False
        with self.assertRaises(RuntimeError):
            m.check_candidate(value, config)

    def test_normalized_compose_omits_false_bind_option(self):
        value, config = candidate()
        mount = value['services']['cube-management-api']['volumes'][0]
        mount['bind'] = {}
        m.check_candidate(value, config)
        mount['bind'] = {'create_host_path': True}
        with self.assertRaises(RuntimeError):
            m.check_candidate(value, config)

    def test_drain_accepts_only_same_app_same_image_canonical_recreation(self):
        a = [{'name': '/s-A', 'id': 'a'*64, 'image': 'image'}, {'name': '/unrelated', 'id': 'c'*64, 'image': 'other'}]
        b = copy.deepcopy(a)
        b[0]['id'] = 'b'*64
        old = [{'id': 'A', 'app_id': 'owner-app', 'container_id': 'a'*12}]
        new = [{'id': 'A', 'app_id': 'owner-app', 'container_id': 'b'*12}]
        self.assertEqual(len(m.validate_drain_transition(a, b, old, new)), 1)
        for mutation in ('image', 'unrelated', 'owner', 'missing-binding'):
            changed, rows = copy.deepcopy(b), copy.deepcopy(new)
            if mutation == 'image': changed[0]['image'] = 'different'
            if mutation == 'unrelated': changed[1]['id'] = 'd'*64
            if mutation == 'owner': rows[0]['app_id'] = 'other-app'
            if mutation == 'missing-binding': rows[0]['container_id'] = None
            with self.subTest(mutation=mutation), self.assertRaises(RuntimeError):
                m.validate_drain_transition(a, changed, old, rows)

    def test_partial_install_restores_first_file_without_rewinding_db(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            state, job = base/'state', base/'job'
            state.mkdir(); job.mkdir()
            (state/'runtime-compose.json').write_text('candidate-runtime')
            (state/'active-images.json').write_text('old-active')
            (job/'active.before').write_text('old-active')
            task = object.__new__(m.Cutover)
            calls = []
            task.c = {'controller_id': 'old-controller', 'candidate_hashes': {'runtime-compose.json': m.digest(state/'runtime-compose.json'), 'active-images.json': 'uninstalled-candidate-hash'}}
            task.current = 'old-controller'; task.job = job
            task.installed = True; task.recreated = False; task.routing_installed = False
            task.backups = {str(state/'runtime-compose.json'): None, str(state/'active-images.json'): {'backup': 'active.before', 'sha256': m.digest(state/'active-images.json')}}
            task.baseline = {'host_relays': {}}
            task.empty = lambda: calls.append('empty')
            task.verify_ready = lambda value: calls.append(('ready', value))
            task.reopen = lambda: calls.append('reopen')
            task.advance = lambda p: calls.append(p)
            task.h = types.SimpleNamespace(run=lambda argv: calls.append(argv))
            with patch.object(m, 'STATE', state): task.rollback()
            self.assertFalse((state/'runtime-compose.json').exists())
            self.assertEqual((state/'active-images.json').read_text(), 'old-active')
            self.assertEqual(calls[0], 'empty')
            self.assertIn('reopen', calls)
            self.assertFalse(any('rm' in v for v in calls if isinstance(v, list)))

    def test_allocation_blocks_rollback_before_any_mutation(self):
        task = object.__new__(m.Cutover)
        task.empty = lambda: (_ for _ in ()).throw(RuntimeError('provider guest exists'))
        task.h = types.SimpleNamespace(run=lambda _: self.fail('mutation reached'))
        with self.assertRaisesRegex(RuntimeError, 'provider guest exists'): task.rollback()

    def test_reopen_failure_redrains_before_claiming_fence(self):
        with tempfile.TemporaryDirectory() as tmp:
            task = object.__new__(m.Cutover)
            calls = []
            task.h = types.SimpleNamespace(ROUTING=Path(tmp), TIMERS=('writer.timer',),
                run=lambda args: calls.append(args), unit=lambda _: {'ActiveState': 'inactive'},
                db_counts=lambda: {'active': 0}, pg_counts=lambda: {'thumbnail': 0},
                wait_for=lambda fn, _: self.assertTrue(fn()))
            task.current = 'controller'; task.traffic_restore_attempted = True
            task.inspect = lambda _: {'State': {'Pid': 123}}
            task.observer = types.SimpleNamespace(process_sockets=lambda _: {'tcp_non_listen': 0})
            task.offline_verified = lambda: True
            task.event = lambda *args: calls.append(args)
            task.refence_after_reopen()
            self.assertEqual(calls[0], ['caddy', 'reload', '--config', str(Path(tmp)/'drain.json')])
            self.assertIn(['systemctl', 'stop', 'writer.timer'], calls)
            self.assertFalse(task.traffic_restore_attempted)


if __name__ == '__main__':
    unittest.main()
