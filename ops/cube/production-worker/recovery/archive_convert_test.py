import io
import json
import os
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest
import zipfile
from archive_validate import InvalidArchive
from archive_validate_test import archive, file
from archive_convert import MANIFEST, PRESERVE, conversion_plan, convert

MARKER = dict(fixture='a'*32, phase='latest', nonce='b'*32)

def owned_entries():
    return [
        ('workspace', b'', tarfile.DIRTYPE, ''), ('workspace/app', b'', tarfile.DIRTYPE, ''),
        file('workspace/app/server.mjs', b'// synthetic', mode=0o640),
        file('workspace/app/crash-fixture-data/marker.json', json.dumps(MARKER).encode()),
        file('.bashrc'), file('.bash_logout'), file('.profile'),
        ('.cache', b'', tarfile.DIRTYPE, ''), ('.baarcha-postgres', b'', tarfile.DIRTYPE, ''),
        ('.cube-crash-fixture', b'', tarfile.DIRTYPE, ''),
        file('.cube-crash-fixture/marker.json', json.dumps(MARKER).encode()),
        file('.baarcha-postgres/data/PG_VERSION', b'18\n'),
        file('.baarcha-postgres/data/global/pg_control', bytes(8192)),
        file('.baarcha-postgres/data/pg_wal/'+'A'*24, b'WAL synthetic only'),
        file('.baarcha-postgres/data/postmaster.pid', b'123\n'),
        ('.runtimed', b'', tarfile.DIRTYPE, ''), file('.runtimed/old-runtime-token', b'do not copy'),
    ]

class ConvertTests(unittest.TestCase):
    def convert_entries(self, entries, root):
        source = Path(root)/'source.tar'
        source.write_bytes(archive(entries))
        return convert(source, Path(root)/'converted', MARKER)

    def test_split_modes_links_pid_preservation_no_runtime_identity(self):
        with tempfile.TemporaryDirectory() as root:
            entries = owned_entries() + [file('workspace/app/target'),
                ('workspace/app/hard', b'', tarfile.LNKTYPE, 'workspace/app/target'),
                ('workspace/app/symbolic', b'', tarfile.SYMTYPE, 'target')]
            result = self.convert_entries(entries, root)
            self.assertEqual(result['flattened_hardlinks'], 1)
            self.assertFalse(result['extracted_host_paths'])
            self.assertFalse(result['go_import_contract_validated'])
            self.assertFalse(result['task_history_converted'])
            with zipfile.ZipFile(Path(root)/'converted/app.zip') as app:
                self.assertEqual(app.read('hard'), b'owned')
                self.assertEqual(app.read('symbolic'), b'target')
                self.assertEqual(app.getinfo('server.mjs').external_attr >> 16 & 0o777, 0o640)
                self.assertTrue(stat.S_ISLNK(app.getinfo('symbolic').external_attr >> 16))
            with zipfile.ZipFile(Path(root)/'converted/home.zip') as home:
                self.assertEqual(home.read('.baarcha-postgres/data/postmaster.pid'), b'123\n')
                self.assertFalse(any(name.startswith('.runtimed') for name in home.namelist()))
            self.assertEqual(json.loads((Path(root)/'converted/home-manifest.json').read_text()), MANIFEST)

    def test_unknown_home_scope_and_runtime_identity_hardlinks_rejected(self):
        for extra in ([file('.unknown/tenant-state')],
                      [('workspace/app/leak', b'', tarfile.LNKTYPE, '.runtimed/old-runtime-token')]):
            with tempfile.TemporaryDirectory() as root:
                with self.assertRaises(InvalidArchive): self.convert_entries(owned_entries()+extra, root)
                self.assertFalse((Path(root)/'converted').exists())

    def test_expansion_accounts_for_all_hardlink_copies(self):
        index = {root:dict(kind='dir',size=0,mode=0o700,target='') for root in (*PRESERVE, 'workspace', 'workspace/app')}
        index['workspace/app/big'] = dict(kind='file',size=1<<30,mode=0o600,target='',offset=0)
        for i in range(8): index[f'workspace/app/copy{i}'] = dict(kind='hardlink',size=0,mode=0o600,target='workspace/app/big')
        with self.assertRaisesRegex(InvalidArchive,'expanded_zip_limit'): conversion_plan(index)

    def test_previous_output_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as root:
            self.convert_entries(owned_entries(), root)
            before=(Path(root)/'converted/app.zip').read_bytes()
            with self.assertRaises(FileExistsError): self.convert_entries(owned_entries(), root)
            self.assertEqual((Path(root)/'converted/app.zip').read_bytes(),before)

if __name__ == '__main__': unittest.main()
