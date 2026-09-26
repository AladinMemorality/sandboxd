import copy
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).parent))
import deployment_profile as p

IMAGE = 'sha256:' + 'a' * 64
APP = '01M3CKN983PFRGMD711PCEPDFD'


def layers():
    base = {'services': {'sandboxd': {'image': 'old-controller', 'build': {'context': './control-plane'},
              'ports': ['127.0.0.1:9090:9000'], 'networks': ['sandboxd_net'],
              'volumes': ['/var/run/docker.sock:/var/run/docker.sock', p.DATA_EXPR + ':' + p.DATA_EXPR,
                          '${SANDBOXD_LOG_DIR:-/var/lib/sandboxd/log}:${SANDBOXD_LOG_DIR:-/var/lib/sandboxd/log}'],
              'environment': {'SANDBOXD_API_TOKENS': '${SANDBOXD_API_TOKENS:-}', 'SANDBOXD_DATA_DIR': p.DATA,
                              'SANDBOXD_API_AUTH_DISABLED': 'false'}},
             'traefik': {'image': 'retained', 'volumes': ['/var/run/docker.sock:/var/run/docker.sock:ro']}}}
    runtime = {'services': {'sandboxd': {'image': 'old-controller',
               'environment': {'SANDBOXD_CUBE_API_URL': 'http://127.0.0.1:20300', 'SANDBOXD_CUBE_PROXY_URL': 'http://127.0.0.1:20080',
                   'SANDBOXD_CUBE_REVERSE_EGRESS': 'true', 'SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED': 'true',
                   'SANDBOXD_CUBE_ADMISSION': 'retained-policy', 'SANDBOXD_CUBE_ROLLOUT': 'allowlist', 'SANDBOXD_CUBE_APP_IDS': 'fixture'},
               'volumes': [p.bind(p.OBSERVER)]},
               **{n: {'network_mode': 'service:sandboxd', 'image': 'retained-relay'}
                  for n in ('cube-management-api', 'cube-management-proxy')}}}
    active = {'services': {'sandboxd': {'image': 'old-controller', 'environment': {'AUTH_SECRET': 'fixture-secret'}}}}
    return base, runtime, active


def resolved(prepared):
    # A small resolved fixture for validator fault injection. Actual Compose
    # merging/interpolation is separately exercised on the installed native CLI.
    base, runtime, active = prepared
    value = copy.deepcopy(base)
    value['services'].update({k: copy.deepcopy(v) for k, v in runtime['services'].items() if k != p.SERVICE})
    service = value['services'][p.SERVICE]
    environment = {**service['environment'], **runtime['services'][p.SERVICE]['environment'], **active['services'][p.SERVICE]['environment']}
    service.update(copy.deepcopy(active['services'][p.SERVICE]))
    service['environment'] = environment
    return value


class ProfileTests(unittest.TestCase):
    def test_existing_routes_relays_secrets_and_capacity_are_preserved(self):
        before = layers()
        saved = copy.deepcopy(before)
        out = p.prepare_layers(*before, IMAGE, APP)
        self.assertEqual(before, saved)
        self.assertEqual(out[0]['services']['traefik'], before[0]['services']['traefik'])
        self.assertEqual(out[0]['services']['sandboxd']['ports'], before[0]['services']['sandboxd']['ports'])
        self.assertEqual(out[0]['services']['sandboxd']['networks'], before[0]['services']['sandboxd']['networks'])
        self.assertEqual(out[0]['services']['sandboxd']['environment'], before[0]['services']['sandboxd']['environment'])
        self.assertEqual(out[1]['services']['sandboxd']['environment'], before[1]['services']['sandboxd']['environment'])
        self.assertEqual(out[2]['services']['sandboxd']['environment']['AUTH_SECRET'], 'fixture-secret')
        for name in ('cube-management-api', 'cube-management-proxy'):
            self.assertEqual(out[1]['services'][name], before[1]['services'][name])
        result = p.validate_resolved(resolved(out), IMAGE, APP)
        self.assertFalse(result['production_deployed'])
        self.assertFalse(result['docker_socket_mounted'])
        self.assertEqual(result['controller_binary'], '/usr/local/bin/cube-controller')

    def test_no_late_layer_can_restore_old_image_build_or_docker_socket(self):
        original = layers()
        for layer in original:
            service = layer['services'][p.SERVICE]
            service.update(build={'context': 'legacy'}, entrypoint=['/usr/local/bin/sandboxd'], command=['legacy'])
            service.setdefault('volumes', []).append('/var/run/docker.sock:/var/run/docker.sock:ro')
        for layer in p.prepare_layers(*original, IMAGE, APP):
            service = layer['services'][p.SERVICE]
            self.assertEqual(service['image'], IMAGE)
            self.assertEqual(service['entrypoint'], [p.BINARY])
            self.assertEqual(service['command'], [])
            self.assertNotIn('build', service)
            self.assertFalse(any(v['target'] == '/var/run/docker.sock' for v in service.get('volumes', [])))

    def test_environment_interpolation_is_not_replaced_by_frozen_credentials(self):
        out = p.prepare_layers(*layers(), IMAGE, APP)
        self.assertEqual(out[0]['services'][p.SERVICE]['environment']['SANDBOXD_API_TOKENS'], '${SANDBOXD_API_TOKENS:-}')
        root = next(v for v in out[0]['services'][p.SERVICE]['volumes'] if v['target'] == p.DATA_EXPR)
        self.assertEqual(root, p.bind(p.DATA_EXPR))

    def test_exact_mounts_protect_history_and_keep_database_writable(self):
        out = resolved(p.prepare_layers(*layers(), IMAGE, APP))
        mounts = {v['target']: v for v in out['services'][p.SERVICE]['volumes']}
        self.assertTrue(mounts[p.DATA]['read_only'])
        self.assertTrue(mounts[p.SOCKET]['read_only'])
        for path in p.WRITABLE:
            self.assertFalse(mounts[path]['read_only'])
            self.assertFalse(mounts[path]['bind']['create_host_path'])
        self.assertNotIn(p.DATA + '/workspaces', mounts)

    def test_normalized_compose_omits_false_create_host_path_but_true_is_rejected(self):
        out = resolved(p.prepare_layers(*layers(), IMAGE, APP))
        for value in out['services'][p.SERVICE]['volumes']:
            value['bind'].pop('create_host_path')
        p.validate_resolved(out, IMAGE, APP)
        out['services'][p.SERVICE]['volumes'][0]['bind']['create_host_path'] = True
        with self.assertRaises(RuntimeError):
            p.validate_resolved(out, IMAGE, APP)

    def test_unsafe_image_app_and_mount_alias_refused(self):
        for image in ('latest', 'controller:latest', 'sha256:bad'):
            with self.subTest(image=image), self.assertRaises(RuntimeError):
                p.prepare_layers(*layers(), image, APP)
        with self.assertRaises(RuntimeError):
            p.prepare_layers(*layers(), IMAGE, 'not-an-app')
        original = layers()
        original[0]['services'][p.SERVICE]['volumes'].append('/var/run/docker.sock:/alias.sock')
        with self.assertRaises(RuntimeError):
            p.prepare_layers(*original, IMAGE, APP)

    def test_rendered_mount_privilege_and_namespace_regressions_refused(self):
        original = resolved(p.prepare_layers(*layers(), IMAGE, APP))
        changes = [lambda v: v['services'][p.SERVICE]['volumes'].append(p.bind('/var/run/docker.sock')),
                   lambda v: v['services'][p.SERVICE].update(entrypoint=['/usr/local/bin/sandboxd']),
                   lambda v: v['services'][p.SERVICE].update(privileged=True),
                   lambda v: v['services'][p.SERVICE].update(network_mode='host'),
                   lambda v: v['services'][p.SERVICE]['environment'].update(SANDBOXD_CUBE_ROLLOUT='allowlist'),
                   lambda v: v['services'][p.SERVICE]['environment'].update(SANDBOXD_API_AUTH_DISABLED='true'),
                   lambda v: v['services']['cube-management-api'].update(network_mode='host'),
                   lambda v: v['services'][p.SERVICE]['volumes'][0].update(read_only=False),
                   lambda v: v['services'][p.SERVICE]['volumes'][1].update(read_only=True)]
        for change in changes:
            value = copy.deepcopy(original)
            change(value)
            with self.assertRaises(RuntimeError):
                p.validate_resolved(value, IMAGE, APP)

    def test_normal_worker_boot_advances_admission_without_restoring_old_controller(self):
        path = Path(__file__).parents[1] / 'worker-lifecycle/test_boot_transition.py'
        spec = importlib.util.spec_from_file_location('profile_boot_fixture', path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as tmp:
            fixture = module.Fixture(Path(tmp))
            base = layers()[0]
            prepared = p.prepare_layers(base, fixture.c, fixture.a, IMAGE, APP)
            with fixture.patches():
                wanted = module.b.new_configs(fixture.s, fixture.g, prepared[1], fixture.gen, prepared[2])
            for key in (str(fixture.compose), str(fixture.activefile)):
                service = wanted[key]['services'][p.SERVICE]
                self.assertEqual(service['image'], IMAGE)
                self.assertEqual(service['entrypoint'], [p.BINARY])
                self.assertEqual(json.loads(service['environment']['SANDBOXD_CUBE_ADMISSION'])['storage_guard']['expected_boot_id'], module.NEW)
            active = wanted[str(fixture.activefile)]['services'][p.SERVICE]
            self.assertEqual(active['environment']['SANDBOXD_CUBE_ROLLOUT'], 'global')
            self.assertEqual(active['environment']['SANDBOXD_CUBE_APP_IDS'], '')
            self.assertEqual(active['volumes'], prepared[2]['services'][p.SERVICE]['volumes'])


if __name__ == '__main__':
    unittest.main()
