import copy
import importlib.util
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
import unittest
from unittest import mock


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


p = load('profile', Path(__file__).with_name('profile.py'))
cohort_tests = load('cohort_tests', Path(__file__).resolve().parents[2] / 'cutover/test_cohort.py')
c = cohort_tests.c
motion = load('motion', Path(__file__).with_name('cohort.py'))


def inputs():
    admission = {'max_active': 4, 'cpu_count': 2, 'memory_mb': 2048, 'writable_disk_mb': 10240,
                 'storage_guard': {'expected_boot_id': 'pinned'}, 'templates': {'original': {'cpu_count': 2, 'memory_mb': 2048}}}
    env = {'SANDBOXD_CUBE_ADMISSION': p.encoded(admission), 'SANDBOXD_CUBE_TEMPLATES': '{"react-vite":"original"}',
           'SANDBOXD_CUBE_ROLLOUT': 'allowlist', 'SANDBOXD_CUBE_API_KEY': 'existing-private-key'}
    runtime = {'services': {'sandboxd': {'environment': env}, 'relay': {'image': 'fixed'}}}
    active = {'services': {'sandboxd': {'environment': copy.deepcopy(env), 'image': 'retained-tag'}}}
    stop = {'controller_id': 'old', 'admission': admission, 'worker_boot_id': 'unchanged'}
    return runtime, active, stop


class ProfileTests(unittest.TestCase):
    def test_native_output_is_private_even_with_normal_process_umask(self):
        with tempfile.TemporaryDirectory() as directory:
            host = motion.Host.__new__(motion.Host)
            host.environment = {}; host.templates = {}; host.fds = (); host.job = Path(directory)
            host.event = lambda *args: None
            host.cli_args = lambda: [sys.executable, '-c', 'print("{}")']
            previous = os.umask(0o022)
            try:
                with mock.patch.object(motion.b, 'trusted', side_effect=lambda p: Path(p).read_bytes()):
                    self.assertEqual(host.run_cli([], 'test', 30), {})
            finally: os.umask(previous)
            for file in host.job.iterdir(): self.assertEqual(stat.S_IMODE(file.stat().st_mode), 0o600)
    def test_exact_template_growth_without_capacity_default_or_secret_change(self):
        before = inputs(); retained = copy.deepcopy(before)
        runtime, active, stop = p.prepare(*before)
        self.assertEqual(before, retained)
        self.assertEqual(active['services']['sandboxd']['volumes'], [p.MOUNT])
        self.assertEqual(active['services']['sandboxd']['image'], p.IMAGE)
        for layer in (runtime, active):
            env = layer['services']['sandboxd']['environment']
            self.assertEqual(env['SANDBOXD_CUBE_MOTION_STUDIO_APP_ID'], p.APP)
            self.assertEqual(env['SANDBOXD_CUBE_TEMPLATES'], retained[0]['services']['sandboxd']['environment']['SANDBOXD_CUBE_TEMPLATES'])
            self.assertEqual(env['SANDBOXD_CUBE_API_KEY'], 'existing-private-key')
            self.assertEqual(env['SANDBOXD_CUBE_ROLLOUT'], 'allowlist')
            self.assertEqual(json.loads(env['SANDBOXD_CUBE_ADMISSION']), stop['admission'])
        self.assertEqual(stop['admission']['max_active'], 4)
        self.assertEqual(stop['admission']['templates'][p.TEMPLATE], {'cpu_count': 2, 'memory_mb': 2048})
        self.assertEqual(stop['controller_id'], 'old')
    def test_refuses_changed_policies_and_preexisting_socket_mounts(self):
        for mutation in ('capacity', 'mapping', 'active-mount', 'stop-policy'):
            runtime, active, stop = inputs()
            if mutation == 'capacity':
                v = json.loads(runtime['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION']); v['max_active'] = 5
                runtime['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION'] = p.encoded(v)
            if mutation == 'mapping': active['services']['sandboxd']['environment']['SANDBOXD_CUBE_MOTION_STUDIO_APP_ID'] = 'another'
            if mutation == 'active-mount': active['services']['sandboxd']['volumes'] = ['/run:/run']
            if mutation == 'stop-policy': stop['admission']['memory_mb'] = 4096
            with self.subTest(mutation=mutation), self.assertRaises(RuntimeError): p.prepare(runtime, active, stop)
    def test_render_allows_only_exact_environment_mount_and_image_changes(self):
        runtime, active, _ = inputs()
        before = {'services': {'sandboxd': {**active['services']['sandboxd'], 'volumes': [{'type': 'bind', 'source': '/var/lib/sandboxd', 'target': '/var/lib/sandboxd'}]}, 'relay': runtime['services']['relay']}}
        after = copy.deepcopy(before); service = after['services']['sandboxd']
        service['environment'] = p.environment(service['environment']); service['image'] = p.IMAGE
        service['volumes'].append(p.MOUNT)
        p.validate_rendered(before, after)
        for change in ('writable-socket', 'extra-host-mount', 'different-relay', 'default'):
            bad = copy.deepcopy(after)
            if change == 'writable-socket': bad['services']['sandboxd']['volumes'][-1]['read_only'] = False
            if change == 'extra-host-mount': bad['services']['sandboxd']['volumes'].append({'type': 'bind', 'source': '/', 'target': '/host'})
            if change == 'different-relay': bad['services']['relay']['image'] = 'other'
            if change == 'default': bad['services']['sandboxd']['environment']['SANDBOXD_CUBE_ROLLOUT'] = 'global'
            with self.subTest(change=change), self.assertRaises(RuntimeError): p.validate_rendered(before, bad)
    def test_motion_is_still_excluded_from_ordinary_cohorts(self):
        value = cohort_tests.config(); value.update(version=2, parallelism=1)
        value['projects'][0].update(app_id=p.APP, sandbox_id=p.SANDBOX, template_id=p.TEMPLATE)
        with self.assertRaises(c.b.Refused): c.validate_config(value)
        c.validate_config(value, motion=True)
        for field, changed in [('sandbox_id', '1' * 26), ('app_id', '1' * 26), ('template_id', 'tpl-old'), ('preset', 'react-pro')]:
            bad = copy.deepcopy(value); bad['projects'][0][field] = changed
            with self.subTest(field=field), self.assertRaises(c.b.Refused): c.validate_config(bad, motion=True)


if __name__ == '__main__': unittest.main()
