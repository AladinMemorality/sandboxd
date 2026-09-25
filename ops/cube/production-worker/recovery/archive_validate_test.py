import hashlib
import io
import json
import tarfile
import unittest
from dataclasses import replace
from archive_validate import InvalidArchive, Limits, validate


def archive(entries, fmt=tarfile.PAX_FORMAT):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w', format=fmt) as tar:
        for entry in entries:
            name, data, kind, target, *options = entry
            info = tarfile.TarInfo(name)
            info.uid = info.gid = 1000
            info.mode = 0o700 if kind == tarfile.DIRTYPE else 0o600
            info.type, info.linkname = kind, target
            info.size = len(data) if kind == tarfile.REGTYPE else 0
            for key, value in (options[0] if options else {}).items(): setattr(info, key, value)
            tar.addfile(info, io.BytesIO(data) if kind == tarfile.REGTYPE else None)
    return output.getvalue()


def file(name, data=b'owned', **options):
    return (name, data, tarfile.REGTYPE, '', options)


class ArchiveValidationTests(unittest.TestCase):
    def reject(self, entries, code=None, **kwargs):
        with self.assertRaises(InvalidArchive) as caught:
            validate(io.BytesIO(archive(entries)), **kwargs)
        if code: self.assertEqual(str(caught.exception), code)

    def test_regular_pax_and_internal_links_preserved(self):
        blob = archive([('./', b'', tarfile.DIRTYPE, ''), file('./workspace/app/a'),
                        ('workspace/app/b', b'', tarfile.LNKTYPE, './workspace/app/a'),
                        ('workspace/app/c', b'', tarfile.SYMTYPE, 'b')])
        result, index = validate(io.BytesIO(blob), return_index=True)
        self.assertEqual(result['entries'], 3)
        self.assertEqual(result['archive_sha256'], hashlib.sha256(blob).hexdigest())
        self.assertEqual(blob[index['workspace/app/a']['offset']:][:5], b'owned')
        self.assertFalse(result['runtimed_import_authorized'])

    def test_paths_duplicates_special_nodes_and_privileges(self):
        for name in ('../owner', '/etc/passwd', 'a/../../b', 'a//b', 'a/./b'):
            self.reject([file(name)])
        self.reject([file('a'), file('a')], 'duplicate_member_or_entry_limit')
        for kind in (tarfile.CHRTYPE, tarfile.BLKTYPE, tarfile.FIFOTYPE, tarfile.GNUTYPE_SPARSE):
            self.reject([('a', b'', kind, '')], 'special_node_or_unsupported_tar_type')
        self.reject([file('a', mode=0o4755)], 'privilege_or_unsupported_mode_bits')
        self.reject([('shared', b'', tarfile.DIRTYPE, '', {'mode': 0o1777})],
                    'privilege_or_unsupported_mode_bits')
        self.reject([file('a', uid=0)], 'unexpected_numeric_ownership')

    def test_ancestors_are_checked_independent_of_tar_order(self):
        for entries in ([file('a/child'), ('a', b'', tarfile.SYMTYPE, 'safe')],
                        [('a', b'', tarfile.SYMTYPE, 'safe'), file('a/child')]):
            self.reject(entries, 'non_directory_ancestor')
        self.reject([file('a'), file('a/child')], 'non_directory_ancestor')

    def test_hardlinks_must_resolve_to_regular_file(self):
        self.reject([('a', b'', tarfile.LNKTYPE, '../escape')])
        self.reject([('a', b'', tarfile.LNKTYPE, 'missing')], 'cyclic_or_missing_hardlink_target')
        self.reject([('a', b'', tarfile.LNKTYPE, 'b'), ('b', b'', tarfile.LNKTYPE, 'a')], 'cyclic_or_missing_hardlink_target')
        self.reject([('a', b'', tarfile.LNKTYPE, 'b'), ('b', b'', tarfile.SYMTYPE, 'c')], 'hardlink_target_not_regular')

    def test_symlink_chains_cannot_escape_or_cycle(self):
        self.reject([('a', b'', tarfile.SYMTYPE, '/etc/shadow')], 'external_symlink_requires_exact_review')
        self.reject([('a', b'', tarfile.SYMTYPE, '../escape')], 'external_symlink_requires_exact_review')
        self.reject([('a', b'', tarfile.SYMTYPE, 'b'), ('b', b'', tarfile.SYMTYPE, 'a')], 'cyclic_symlink_chain')
        result = validate(io.BytesIO(archive([file('c'), ('a', b'', tarfile.SYMTYPE, 'b'), ('b', b'', tarfile.SYMTYPE, 'c')])))
        self.assertTrue(result['valid_merged_home_tar'])

    def test_explicit_external_plan_is_hash_and_image_bound(self):
        blob = archive([('workspace/app/.venv/bin/python', b'', tarfile.SYMTYPE, '/usr/bin/python3.13')])
        plan = dict(version=1, archive_sha256=hashlib.sha256(blob).hexdigest(), destination_image_sha256='a'*64,
                    links=[dict(path='workspace/app/.venv/bin/python', target='/usr/bin/python3.13')])
        result = validate(io.BytesIO(blob), link_plan=plan)
        self.assertEqual(result['reviewed_external_links'], 1)
        self.assertFalse(result['runtimed_import_authorized'])
        with self.assertRaisesRegex(InvalidArchive, 'archive_hash_mismatch'):
            validate(io.BytesIO(blob), link_plan=plan | {'archive_sha256':'b'*64})

    def test_pax_xattr_sparse_and_oversized_metadata_fail_before_read(self):
        self.reject([file('a', pax_headers={'SCHILY.xattr.security.capability':'untrusted'})], 'unsupported_pax_sparse_xattr_or_extension')
        self.reject([file('a', pax_headers={'GNU.sparse.size':'100'})], 'unsupported_pax_sparse_xattr_or_extension')
        header = tarfile.TarInfo('pax'); header.type = tarfile.XHDTYPE; header.size = 1 << 30
        with self.assertRaisesRegex(InvalidArchive, 'metadata_size_limit'):
            validate(io.BytesIO(header.tobuf()))

    def test_limits_truncation_and_concatenation(self):
        self.reject([file('a', b'1234')], 'payload_size_limit', limits=replace(Limits(), member_bytes=3))
        blob = archive([file('a')])
        for invalid in (blob[:512], blob+archive([file('other')])):
            with self.assertRaises(InvalidArchive): validate(io.BytesIO(invalid))
        self.reject([file('a'), file('b')], 'duplicate_member_or_entry_limit', limits=replace(Limits(), entries=1))

    def test_postgres_and_latest_files_are_not_sql_recovery_proof(self):
        marker = dict(fixture='a'*32, phase='latest', nonce='b'*32)
        entries = [file('.baarcha-postgres/data/PG_VERSION', b'18\n'),
                   file('.baarcha-postgres/data/global/pg_control', bytes(8192)),
                   file('.baarcha-postgres/data/pg_wal/'+'A'*24, b'WAL'),
                   file('workspace/app/crash-fixture-data/marker.json', json.dumps(marker).encode()),
                   file('.cube-crash-fixture/marker.json', json.dumps(marker).encode())]
        result = validate(io.BytesIO(archive(entries)), require_postgres=True, expected_marker=marker)
        self.assertTrue(result['latest_files_match'])
        self.assertFalse(result['postgres_crash_recovery_proven'])
        self.reject(entries[:-1], 'latest_marker_missing_or_invalid', expected_marker=marker)
        self.reject(entries, 'invalid_latest_marker_expectation', expected_marker=marker | {'phase':'baseline'})
        self.reject(entries, 'latest_marker_mismatch', expected_marker=marker | {'nonce':'c'*32})

if __name__ == '__main__': unittest.main()
