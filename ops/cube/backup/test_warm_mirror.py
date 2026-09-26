import contextlib
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import sqlite3
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import warm_mirror as w

NATIVE = sys.platform == 'linux' and Path('/usr/bin/rsync').exists()


class ScopeTests(unittest.TestCase):
    def test_many_root_scope_preserves_exact_ancestry_and_literal_names(self):
        roots = ['/data/owner-' + str(n) for n in range(112)]
        for path, expected in (
            ('/data/owner-111', True), ('/data/owner-111/deep/.private\nfile', True),
            ('/data/owner-111-extra/file', False), ('/data/owner-112/file', False),
            ('/data/owner-111/../owner-112/file', False), ('/data/owner-111//file', False),
            ('data/owner-111/file', False), ('/data', False), ('/', False)):
            with self.subTest(path=path): self.assertEqual(w.contained(path, roots), expected)
        self.assertTrue(w.contained('/data/owner-1/file', [Path('/data/owner-1')]))
        self.assertTrue(w.contained('/data/owner-1/file', ['/data/owner-1/']))


class MirrorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.root.chmod(0o700)
        self.source = self.root / 'owner'
        self.source.mkdir()
        self.cache = self.root / 'cache'
        self.cache.mkdir(mode=0o700)
        self.tree = self.cache / 'tree'
        self.tree.mkdir(mode=0o700)
        self.roots = [str(self.source)]
        self.file = self.source / '-private\nname'
        self.file.write_bytes(b'original')
        self.file.chmod(0o640)

    def scan(self, label):
        path = self.cache / (label + '.jsonl')
        with path.open('w') as stream:
            for row in w.records(self.roots):
                stream.write(json.dumps(row, sort_keys=True) + '\n')
        return {'sha256': w.digest(path)}

    def prepare(self, copy_hook=None):
        before = self.scan('before')
        listing = self.cache / 'initial.nul'
        listing.write_bytes(os.fsencode(str(self.source).lstrip('/')) + b'\0')
        subprocess.run(w.rsync_args(self.tree, listing), check=True, capture_output=True)
        if copy_hook:
            copy_hook()
        after = self.scan('after')
        w.publish(self.cache / 'scope.PRIVATE.json', {'roots': self.roots, 'destination': str(self.tree)})
        w.publish(self.cache / 'candidate-only.json', {'online_precopy_completed': True, 'rsync_exit': 0,
                                                     'backup_accepted': False, 'before': before, 'after': after})
        w.prepare(self.cache)
        return w.digest(self.cache / 'prepared.json')

    def seal(self, sha):
        calls = []
        value = w.seal(self.cache, sha, self.roots, self.cache / 'sealed', lambda: calls.append('fenced'))
        self.assertEqual(len(calls), 3)
        self.assertFalse(value['backup_accepted'])
        return value

    def cached(self, path):
        return self.tree / str(path).lstrip('/')

    def test_paths_reject_overlap_root_and_traversal(self):
        for roots in [['/'], ['/a', '/a/b'], ['/a/../b'], ['/a', '/a']]:
            with self.subTest(roots=roots), self.assertRaises(RuntimeError):
                w.normalized(roots)
        self.assertFalse(w.contained('/a/../a/x', ['/a']))
        self.assertFalse(w.contained('/ab/x', ['/a']))

    def test_cached_symlink_parent_never_reaches_external_file(self):
        outside = self.root / 'outside'
        outside.mkdir()
        (outside / 'victim').write_bytes(b'keep')
        (self.tree / 'escape').symlink_to(outside, target_is_directory=True)
        self.assertIsNone(w.safe_cached_generation(self.tree, '/escape/victim'))
        w.discard_dirty(self.tree, '/escape/victim')
        self.assertEqual((outside / 'victim').read_bytes(), b'keep')

    def test_socket_exclusion_cannot_expand_to_regular_siblings(self):
        for path in ['/owner/*.sock', '/owner/a?b', '/owner/[abc]', '/owner/../elsewhere']:
            with self.subTest(path=path), self.assertRaises(RuntimeError):
                w.rsync_args(self.tree, self.cache / 'list', [{'path': path}])

    def test_manifest_scope_and_duplicate_rejected(self):
        with contextlib.closing(w.database(self.cache / 'bad.sqlite')) as db:
            w.table(db, 'scan')
            row = w.generation(self.file)
            w.add(db, 'scan', row, self.roots)
            with self.assertRaises(sqlite3.IntegrityError):
                w.add(db, 'scan', row, self.roots)
            with self.assertRaises(RuntimeError):
                w.add(db, 'scan', dict(row, path='/elsewhere'), self.roots)

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_stable_copy_reused_without_source_content_read(self):
        (self.source / 'sibling').symlink_to(self.file.name)
        os.link(self.file, self.source / 'hardlink')
        sha = self.prepare()
        with mock.patch.object(w, 'file_digest', side_effect=AssertionError('no frozen source hashing')):
            result = self.seal(sha)
        self.assertEqual(result['delta']['dirty_files'], 0)
        self.assertEqual(self.cached(self.file).read_bytes(), b'original')
        self.assertEqual(self.cached(self.file).stat().st_ino, self.cached(self.source / 'hardlink').stat().st_ino)
        self.assertEqual(os.readlink(self.cached(self.source / 'sibling')), self.file.name)

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_same_size_same_mtime_edit_and_hardlinks_force_content_copy(self):
        os.link(self.file, self.source / 'hardlink')
        sha = self.prepare()
        original = self.file.stat()
        self.file.write_bytes(b'changed!')
        os.utime(self.file, ns=(original.st_atime_ns, original.st_mtime_ns))
        result = self.seal(sha)
        self.assertEqual(result['delta']['dirty_files'], 2)
        self.assertEqual(self.cached(self.file).read_bytes(), b'changed!')
        self.assertEqual(self.cached(self.file).stat().st_ino, self.cached(self.source / 'hardlink').stat().st_ino)

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_inode_replacement_with_same_size_and_mtime_is_detected(self):
        sha = self.prepare()
        original = self.file.stat()
        replacement = self.source / 'replacement'
        replacement.write_bytes(b'replaced')
        replacement.chmod(0o640)
        os.utime(replacement, ns=(original.st_atime_ns, original.st_mtime_ns))
        replacement.replace(self.file)
        result = self.seal(sha)
        self.assertEqual(result['delta']['dirty_files'], 1)
        self.assertEqual(self.cached(self.file).read_bytes(), b'replaced')

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_online_directory_change_invalidates_descendant_reuse(self):
        def during_copy():
            transient = self.source / 'temporary'
            transient.write_bytes(b'x')
            transient.unlink()
        sha = self.prepare(during_copy)
        self.assertEqual(self.seal(sha)['delta']['dirty_files'], 1)

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_deletions_type_changes_and_external_links(self):
        directory = self.source / 'directory'
        directory.mkdir()
        (directory / 'old').write_bytes(b'old')
        sha = self.prepare()
        self.file.unlink()
        shutil.rmtree(directory)
        directory.symlink_to('/outside/private', target_is_directory=True)
        added = self.source / 'new-dir'
        added.mkdir()
        (added / 'new').write_bytes(b'new')
        self.seal(sha)
        self.assertFalse(self.cached(self.file).exists())
        self.assertEqual(os.readlink(self.cached(directory)), '/outside/private')
        self.assertEqual(self.cached(added / 'new').read_bytes(), b'new')

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_cached_content_tamper_detected_even_with_restored_mtime(self):
        sha = self.prepare()
        cached = self.cached(self.file)
        before = cached.stat()
        cached.write_bytes(b'corrupt!')
        os.utime(cached, ns=(before.st_atime_ns, before.st_mtime_ns))
        result = self.seal(sha)
        self.assertEqual(result['delta']['dirty_files'], 1)
        self.assertEqual(cached.read_bytes(), b'original')

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_budget_refusal_happens_before_cache_mutation(self):
        sha = self.prepare()
        self.file.write_bytes(b'changed!')
        with mock.patch.object(w, 'MAX_DELTA_BYTES', 1), self.assertRaisesRegex(RuntimeError, 'budget'):
            self.seal(sha)
        self.assertEqual(self.cached(self.file).read_bytes(), b'original')
        self.assertFalse((self.cache / 'sealed/sealed.json').exists())

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_new_unreviewed_socket_refuses(self):
        sha = self.prepare()
        sock = socket.socket(socket.AF_UNIX)
        self.addCleanup(sock.close)
        sock.bind(str(self.source / 'sock'))
        with self.assertRaisesRegex(RuntimeError, 'special file'):
            self.seal(sha)

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_exact_reviewed_socket_omitted_without_deleting_source(self):
        sha = self.prepare()
        sock = socket.socket(socket.AF_UNIX)
        self.addCleanup(sock.close)
        path = self.source / 'reviewed.sock'
        sock.bind(str(path))
        info = path.lstat()
        review = [{'path': str(path), 'inode': info.st_ino, 'device': info.st_dev,
                   'reason': 'Disposable test socket, explicitly outside restored data'}]
        w.seal(self.cache, sha, self.roots, self.cache / 'sealed', lambda: None, review)
        self.assertTrue(stat.S_ISSOCK(path.lstat().st_mode))
        self.assertFalse(self.cached(path).exists())
        self.assertEqual(self.cached(self.file).read_bytes(), b'original')

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_consumed_cache_cannot_be_replayed(self):
        sha = self.prepare()
        self.seal(sha)
        with self.assertRaisesRegex(RuntimeError, 'already consumed'):
            w.seal(self.cache, sha, self.roots, self.cache / 'again', lambda: None)

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_omitted_socket_cannot_hide_stale_regular_cache_content(self):
        sha = self.prepare()
        sock = socket.socket(socket.AF_UNIX)
        self.addCleanup(sock.close)
        path = self.source / 'reviewed.sock'
        sock.bind(str(path))
        self.cached(path).write_bytes(b'stale regular data')
        info = path.lstat()
        review = [{'path': str(path), 'inode': info.st_ino, 'device': info.st_dev, 'reason': 'Fixture'}]
        with self.assertRaisesRegex(RuntimeError, 'stale cached data'):
            w.seal(self.cache, sha, self.roots, self.cache / 'sealed', lambda: None, review)
        self.assertFalse((self.cache / 'consumed.json').exists())

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_private_implied_ancestor_symlink_refused(self):
        sha = self.prepare()
        top = self.tree / Path(str(self.source)).parts[1]
        moved = self.cache / 'moved'
        top.rename(moved)
        top.symlink_to(moved, target_is_directory=True)
        with self.assertRaisesRegex(RuntimeError, 'Unsafe cache ancestor'):
            self.seal(sha)
        self.assertEqual((moved / str(self.file).split('/', 2)[2]).read_bytes(), b'original')

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_changed_frozen_source_never_publishes_seal(self):
        sha = self.prepare()
        invoke = w.invoke
        def change_after_copy(args, log, inherited=()):
            invoke(args, log, inherited)
            if log.name == 'verify.PRIVATE.log':
                self.file.write_bytes(b'changed!')
        with mock.patch.object(w, 'invoke', side_effect=change_after_copy), self.assertRaisesRegex(RuntimeError, 'Source changed'):
            self.seal(sha)
        self.assertFalse((self.cache / 'sealed/sealed.json').exists())

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_permissions_xattrs_and_acl_reconciled(self):
        sha = self.prepare()
        self.file.chmod(0o600)
        os.setxattr(self.file, 'user.fixture', b'owner')
        if shutil.which('setfacl'):
            subprocess.run(['setfacl', '-m', 'u:12345:r--', str(self.file)], check=True)
        self.seal(sha)
        self.assertEqual(stat.S_IMODE(self.cached(self.file).stat().st_mode), stat.S_IMODE(self.file.stat().st_mode))
        self.assertEqual(os.getxattr(self.cached(self.file), 'user.fixture'), b'owner')
        self.assertEqual(set(os.listxattr(self.file)), set(os.listxattr(self.cached(self.file))))
        for key in os.listxattr(self.file):
            self.assertEqual(os.getxattr(self.file, key), os.getxattr(self.cached(self.file), key))

    @unittest.skipUnless(NATIVE, 'real Linux rsync required')
    def test_prepared_database_mutation_refused(self):
        sha = self.prepare()
        with open(self.cache / 'prepared.sqlite', 'ab') as stream:
            stream.write(b'tamper')
        with self.assertRaisesRegex(RuntimeError, 'evidence changed'):
            self.seal(sha)


if __name__ == '__main__':
    unittest.main()
