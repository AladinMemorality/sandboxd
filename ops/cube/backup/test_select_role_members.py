import hashlib
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock

import select_role_members as select


class SelectedMembers(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.root.chmod(0o700)
        self.capture = self.root / 'pair'
        self.capture.mkdir(mode=0o700)
        self.payload = b'private original evidence\n'
        self.member = 'opt/owned/operator/journal.json'

    def build(self, entries=None):
        path = self.capture / 'rollback'
        with tarfile.open(path, 'w', format=tarfile.PAX_FORMAT) as archive:
            for name, kind, body in entries or [(self.member, 'file', self.payload), ('home/owner/unselected.txt', 'file', b'not selected')]:
                info = tarfile.TarInfo(name)
                if kind == 'file':
                    info.size = len(body)
                    archive.addfile(info, io.BytesIO(body))
                else:
                    info.type = tarfile.SYMTYPE if kind == 'symlink' else tarfile.LNKTYPE
                    info.linkname = body
                    archive.addfile(info)
        path.chmod(0o600)
        manifest = {'version': 1, 'kind': 'cold-cube-pair', 'captured_at': 1790376000,
                    'files': {'rollback': {'bytes': path.stat().st_size, 'sha256': select.cold_pair.digest(path)}}}
        mp = self.capture / 'manifest.json'
        mp.write_text(json.dumps(manifest))
        mp.chmod(0o600)
        return {'version': 1, 'capture_directory': str(self.capture),
                'capture_manifest_sha256': select.cold_pair.digest(mp),
                'selections': [{'role': 'rollback', 'member': self.member, 'output': 'operator.json',
                                'bytes': len(self.payload), 'sha256': hashlib.sha256(self.payload).hexdigest()}]}

    def test_only_explicit_regular_member_and_receipt_written(self):
        plan = self.build()
        output = self.root / 'selected'
        result = select.extract(plan, output)
        self.assertEqual((output / 'operator.json').read_bytes(), self.payload)
        self.assertEqual({p.name for p in output.iterdir()}, {'operator.json', 'complete.json'})
        self.assertEqual((output / 'operator.json').stat().st_mode & 0o777, 0o600)
        self.assertFalse(result['application_restore_verified'])
        self.assertEqual(result['capture_manifest_sha256'], plan['capture_manifest_sha256'])

    def test_symlink_and_hardlink_selected_refused(self):
        for kind in ('symlink', 'hardlink'):
            with self.subTest(kind=kind):
                plan = self.build([(self.member, kind, '/etc/shadow')])
                output = self.root / kind
                with self.assertRaises(RuntimeError): select.extract(plan, output)
                self.assertFalse(output.exists())

    def test_unselected_symlink_never_followed(self):
        plan = self.build([('home/owner/link', 'symlink', '/etc/shadow'), (self.member, 'file', self.payload)])
        select.extract(plan, self.root / 'selected')
        self.assertEqual(set((self.root / 'selected').iterdir()), {self.root / 'selected/operator.json', self.root / 'selected/complete.json'})

    def test_absolute_traversal_and_encoded_separators_are_not_interpreted(self):
        for name in ('/etc/shadow', '../escape', 'home/../escape', 'home//double', 'home/./dot'):
            with self.subTest(name=name), self.assertRaises(RuntimeError): select.canonical_member(name)
        self.assertEqual(select.canonical_member('home/%2e%2e/file'), 'home/%2e%2e/file')
        plan = self.build([('../outside', 'file', b'bad'), (self.member, 'file', self.payload)])
        with self.assertRaises(RuntimeError): select.extract(plan, self.root / 'selected')
        self.assertFalse((self.root / 'outside').exists())

    def test_unusual_unix_names_are_literal_not_destination_paths(self):
        name = 'home/owner/literal\\backslash\n# [1].txt'
        self.assertEqual(select.canonical_member(name), name)
        plan = self.build([(name, 'file', b'unselected'), (self.member, 'file', self.payload)])
        select.extract(plan, self.root / 'unusual')
        self.assertFalse((self.root / 'unusual' / name).exists())

    def test_duplicate_missing_and_wrong_payload_refused(self):
        cases = [[(self.member, 'file', self.payload)] * 2,
                 [('other', 'file', self.payload)],
                 [(self.member, 'file', b'X' * len(self.payload))]]
        for i, entries in enumerate(cases):
            plan = self.build(entries)
            with self.assertRaises(RuntimeError): select.extract(plan, self.root / str(i))

    def test_size_and_total_limits_and_unsafe_output_names(self):
        for change in ('oversize', 'wrong_size', 'output', 'duplicate', 'unknown_role'):
            plan = self.build()
            if change == 'oversize': plan['selections'][0]['bytes'] = select.MAX_MEMBER + 1
            if change == 'wrong_size': plan['selections'][0]['bytes'] += 1
            if change == 'output': plan['selections'][0]['output'] = '../outside'
            if change == 'duplicate': plan['selections'] *= 2
            if change == 'unknown_role': plan['selections'][0]['role'] = '../../etc'
            with self.subTest(change=change), self.assertRaises(RuntimeError): select.extract(plan, self.root / change)

    def test_hash_and_original_manifest_tampering_refused(self):
        plan = self.build()
        (self.capture / 'rollback').write_bytes(b'changed')
        with self.assertRaises(RuntimeError): select.extract(plan, self.root / 'changed-role')
        plan = self.build()
        plan['capture_manifest_sha256'] = '0' * 64
        with self.assertRaises(RuntimeError): select.extract(plan, self.root / 'changed-manifest')

    def test_manifest_changed_during_selection_is_not_published(self):
        plan = self.build()
        original = select.copy_selected
        def change(*args):
            original(*args)
            (self.capture / 'manifest.json').write_text('{}')
        with mock.patch.object(select, 'copy_selected', side_effect=change):
            with self.assertRaises(RuntimeError): select.extract(plan, self.root / 'mutated')
        self.assertFalse((self.root / 'mutated').exists())

    def test_existing_output_never_replaced(self):
        plan = self.build()
        output = self.root / 'keep'
        output.mkdir(mode=0o700)
        (output / 'marker').write_text('retain')
        with self.assertRaises(RuntimeError): select.extract(plan, output)
        self.assertEqual((output / 'marker').read_text(), 'retain')

    def test_source_symlink_hardlink_and_public_mode_refused(self):
        plan = self.build()
        role = self.capture / 'rollback'
        original = self.capture / 'original'
        role.rename(original)
        role.symlink_to(original)
        with self.assertRaises(RuntimeError): select.extract(plan, self.root / 'link')
        role.unlink()
        os.link(original, role)
        with self.assertRaises(RuntimeError): select.extract(plan, self.root / 'hard')
        role.unlink()
        original.rename(role)
        role.chmod(0o644)
        with self.assertRaises(RuntimeError): select.extract(plan, self.root / 'public')


if __name__ == '__main__': unittest.main()
