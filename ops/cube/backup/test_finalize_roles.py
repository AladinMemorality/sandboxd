import copy
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import tarfile
import time
import unittest
from unittest import mock

import finalize_roles as final
import test_capture_roles


class FinalRoleTests(unittest.TestCase):
    def setUp(self):
        fixture = test_capture_roles.RoleTests()
        fixture.setUp()
        self.addCleanup(fixture.doCleanups)
        self.root = fixture.root
        self.before = fixture.config
        self.before['reviewed_files'][final.START] = '3' * 64
        self.before['role_paths']['worker-config'].append(final.START)
        self.pause_path = '/reviewed/pause-current.json'
        self.clean_path = '/reviewed/clean-current.json'
        self.after = copy.deepcopy(self.before)
        self.after['reviewed_files'][final.START] = '4' * 64
        self.after['reviewed_files'].update({self.pause_path: '5' * 64, self.clean_path: '6' * 64})
        self.after['role_paths']['worker-config'] += [self.pause_path, self.clean_path]
        self.pause = {'version': 1, 'verified': True, 'provider_jobs': 0,
                      'generated_at': datetime.now(timezone.utc).isoformat(), 'guest_states': {'existing': 'paused'}}
        self.clean = {'version': 1, 'state': 'stopped-clean', 'proof': self.pause, 'generated_at': time.time()}
        self.start = {'version': 1, 'pause_proof': self.pause_path, 'clean_receipt': self.clean_path}

    def test_only_new_startup_selection_and_exact_evidence_can_change(self):
        final.validate_transition(self.before, self.after, self.pause_path, self.clean_path)
        for change in ('key', 'inventory', 'omission', 'extra', 'role', 'unpin'):
            bad = copy.deepcopy(self.after)
            if change == 'key': bad['reviewed_files'][str(final.roles.PREVIEW_KEY)] = '9' * 64
            if change == 'inventory': bad['inventory']['homes'].append({'sandbox_id': 'new'})
            if change == 'omission': del bad['reviewed_files'][self.pause_path]
            if change == 'extra': bad['reviewed_files']['/reviewed/unrelated'] = '0' * 64
            if change == 'role': bad['role_paths']['rollback-extra'].append('/changed-home')
            if change == 'unpin': del bad['reviewed_files'][final.START]
            with self.subTest(change=change), self.assertRaises(RuntimeError):
                final.validate_transition(self.before, bad, self.pause_path, self.clean_path)

    def test_receipts_must_match_installed_start_and_native_pause(self):
        def check(start=None, pause=None, clean=None):
            final.validate_receipts(start or self.start, self.pause_path, self.clean_path,
                                    pause or self.pause, clean or self.clean)
        check()
        for bad in ({**self.clean, 'state': 'worker-lost'}, {**self.clean, 'proof': {}},
                    {**self.clean, 'generated_at': time.time() + 60}, {**self.clean, 'generated_at': 1}):
            with self.assertRaises(RuntimeError): check(clean=bad)
        with self.assertRaises(RuntimeError): check(start={**self.start, 'pause_proof': '/old'})
        bad = {**self.pause, 'verified': False}
        with self.assertRaises(RuntimeError): check(pause=bad, clean={**self.clean, 'proof': bad})

    def test_external_receipt_requires_validated_pinned_evidence_closure(self):
        clean = {**self.clean, 'state': 'externally-stopped-clean', 'external': {'method': 'fixture'}}
        with self.assertRaises(RuntimeError):
            final.validate_receipts(self.start, self.pause_path, self.clean_path, self.pause, clean)
        closure = [{'path': '/reviewed/external/wait4.trace', 'sha256': '7' * 64, 'bytes': 25}]
        validated = {'receipt': clean, 'closure': closure, 'validator_sha256': '9' * 64}
        start = {**self.start, 'external_verifier_sha256': '9' * 64}
        final.validate_receipts(start, self.pause_path, self.clean_path, self.pause, clean, validated)
        with self.assertRaisesRegex(RuntimeError, 'Installed startup config'):
            final.validate_receipts({**start, 'external_verifier_sha256': '0' * 64}, self.pause_path,
                                    self.clean_path, self.pause, clean, validated)
        after = copy.deepcopy(self.after)
        after['reviewed_files'][closure[0]['path']] = closure[0]['sha256']
        after['role_paths']['worker-config'].append(closure[0]['path'])
        final.validate_transition(self.before, after, self.pause_path, self.clean_path, closure)
        after['reviewed_files'][closure[0]['path']] = '8' * 64
        with self.assertRaisesRegex(RuntimeError, 'External evidence hash'):
            final.validate_transition(self.before, after, self.pause_path, self.clean_path, closure)
        with mock.patch.object(final.roles, 'verify_inputs') as verify:
            with self.assertRaisesRegex(RuntimeError, 'validator must be pinned'):
                final.external_evidence(self.before, self.after, self.clean_path, clean)
            verify.assert_not_called()

    def parent(self):
        directory = self.root / 'closed'
        directory.mkdir(mode=0o700)
        final.roles.write(directory / 'frozen-identities.PRIVATE.json', {'config': self.before})
        result = {}
        for name in (*final.CONFIG_ROLES, *final.HEAVY_ROLES):
            path = directory / name
            if name == 'controller-config':
                with tarfile.open(path, 'w') as archive:
                    identities = directory / 'frozen-identities.PRIVATE.json'
                    archive.add(identities, arcname=str(identities).lstrip('/'))
            else:
                path.write_bytes(('original-' + name).encode())
            path.chmod(0o600)
            result[name] = {'bytes': path.stat().st_size, 'sha256': final.roles.sha(path)}
        for name, value in {
            'complete.json': {'version': 1, 'roles': result, 'full_pair_captured': False},
            'started.json': {'config_sha256': hashlib.sha256(final.roles.canonical(self.before)).hexdigest()},
        }.items():
            final.roles.write(directory / name, value)
        return directory, final.roles.sha(directory / 'complete.json')

    def test_replaced_sidecars_cannot_redefine_the_closed_archive(self):
        directory, digest = self.parent()
        bad = copy.deepcopy(self.before)
        bad['inventory']['homes'][0]['container_id'] = 'other-container'
        (directory / 'frozen-identities.PRIVATE.json').write_text(json.dumps({'config': bad}))
        (directory / 'started.json').write_text(json.dumps({'config_sha256': hashlib.sha256(final.roles.canonical(bad)).hexdigest()}))
        with mock.patch.object(final.roles, 'private', side_effect=lambda p, *a: Path(p)):
            with self.assertRaisesRegex(RuntimeError, 'Frozen identities'):
                final.original_roles(directory, digest)

    def test_closed_heavy_roles_reused_only_when_hashes_and_parent_config_match(self):
        directory, digest = self.parent()
        # Native root ownership is covered by the shared helper. Tests use real
        # files/hashes here, with only its root ownership seam replaced on macOS.
        with mock.patch.object(final.roles, 'private', side_effect=lambda p, *a: Path(p)):
            before, complete, selected = final.original_roles(directory, digest)
            self.assertEqual(before, self.before)
            self.assertEqual(selected['rollback']['path'], str(directory / 'rollback'))
            original = (directory / 'complete.json').read_bytes()
            (directory / 'rollback').write_bytes(b'corrupted')
            with self.assertRaisesRegex(RuntimeError, 'role changed'):
                final.original_roles(directory, digest)
            self.assertEqual((directory / 'complete.json').read_bytes(), original)

    def test_failed_parent_or_changed_receipt_refused(self):
        directory, digest = self.parent()
        with mock.patch.object(final.roles, 'private', side_effect=lambda p, *a: Path(p)):
            with self.assertRaisesRegex(RuntimeError, 'receipt changed'):
                final.original_roles(directory, '0' * 64)
            (directory / 'INCOMPLETE.json').write_text('{}')
            with self.assertRaisesRegex(RuntimeError, 'Failed parent'):
                final.original_roles(directory, digest)

    def test_lock_handoff_is_required_before_any_live_observation(self):
        with mock.patch.object(final.cold_pair, 'native_host'), \
             mock.patch.object(final.cold_pair, 'verify_unit') as verify:
            with self.assertRaisesRegex(RuntimeError, 'Continuous parent'):
                final.finalize(self.after, str(self.root), '0' * 64, str(self.root / 'new'), ())
            verify.assert_not_called()


if __name__ == '__main__':
    unittest.main()
