import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from capture import sha256
from plan import Invalid
from rescue_export import verify_manifest, reject_unexportable_metadata, export, repair_current_clone

class RescueTests(unittest.TestCase):
    def test_explicit_repair_binds_fresh_clone_and_requires_clean_postcheck(self):
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
            root=Path(tmp).resolve(); disk=root/'clone'; disk.write_bytes(b'exact captured disk')
            expected=sha256(disk)
            with patch('rescue_export.subprocess.run') as run:
                with self.assertRaises(Invalid):repair_current_clone(disk,expected,'f'*64,root)
                run.assert_not_called()
            with patch('rescue_export.subprocess.run',side_effect=[SimpleNamespace(returncode=1),SimpleNamespace(returncode=0)]) as run:
                receipt=repair_current_clone(disk,expected,expected,root)
                self.assertEqual([c.args[0][1] for c in run.call_args_list],['-fy','-fn'])
                report=json.loads((root/'repair-receipt.json').read_text())
                self.assertTrue(report['clean_verified'])
                self.assertEqual(report['clone_before_sha256'],expected)
                self.assertEqual(receipt,sha256(root/'repair-receipt.json'))
                self.assertEqual(len(report['steps']),2)

    def test_repair_never_accepts_unresolved_fsck_errors(self):
        from types import SimpleNamespace
        for codes in ([4],[0,1],[1,4]):
            with self.subTest(codes=codes),tempfile.TemporaryDirectory(dir='/tmp') as tmp:
                root=Path(tmp).resolve();disk=root/'clone';disk.write_bytes(b'current')
                with patch('rescue_export.subprocess.run',side_effect=[SimpleNamespace(returncode=n) for n in codes]) as run:
                    with self.assertRaises(Invalid):repair_current_clone(disk,sha256(disk),sha256(disk),root)
                    self.assertEqual(run.call_count,len(codes))
                self.assertFalse(json.loads((root/'repair-receipt.json').read_text())['clean_verified'])

    def test_input_digest_order_and_no_symlink_substitution(self):
        with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
            p=Path(tmp).resolve(); a=p/'current.ext4';b=p/'lower-000.tar';a.write_bytes(b'current acknowledged state');b.write_bytes(b'exact image layer')
            m={'format':1,'purpose':'CUBE_CURRENT_DISK_RESCUE_INPUT','sandbox_id':'owned','upper_subdir':'disk/owned/upper','artifacts':[{'file':v.name,'bytes':v.stat().st_size,'sha256':sha256(v)} for v in (a,b)]}
            verify_manifest(m,p)
            a.write_bytes(b'previous old state')
            with self.assertRaises(Invalid):verify_manifest(m,p)
            a.unlink();a.symlink_to(b)
            with self.assertRaises(Invalid):verify_manifest(m,p)
            m['artifacts'].reverse()
            with self.assertRaises(Invalid):verify_manifest(m,p)
    def test_explicit_pmem_image_layout_never_inferred_from_extension(self):
        with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
            p=Path(tmp).resolve()
            for name in ('current.ext4','lower-000.ext4'):(p/name).write_bytes(b'opaque ext4 fixture')
            m={'format':1,'purpose':'CUBE_CURRENT_DISK_RESCUE_INPUT','sandbox_id':'owned','upper_subdir':'disk/owned/upper','lower_layout':'pmem_ext4','artifacts':[{'file':n,'bytes':(p/n).stat().st_size,'sha256':sha256(p/n)} for n in ('current.ext4','lower-000.ext4')]}
            verify_manifest(m,p)
            del m['lower_layout']
            with self.assertRaises(Invalid):verify_manifest(m,p)
            m['lower_layout']='pmem_ext4';m['artifacts'].append(dict(m['artifacts'][1]))
            with self.assertRaises(Invalid):verify_manifest(m,p)

    def test_snapshot_upper_requires_explicit_bound_template_identity(self):
        with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
            p=Path(tmp).resolve()
            for n in ('current.ext4','lower-000.ext4'):(p/n).write_bytes(b'opaque')
            digest='a'*64
            m={'format':1,'purpose':'CUBE_CURRENT_DISK_RESCUE_INPUT','sandbox_id':'new-id','upper_subdir':'disk/tpl-reviewed_0/upper','lower_layout':'pmem_ext4','source_metadata_sha256':{'template_snapshot_metadata':digest},'upper_identity':{'kind':'template_snapshot','container_id':'tpl-reviewed_0','template_id':'tpl-reviewed','metadata_path':'/data/cubelet/storage/xfs/snapshots/tpl-reviewed/metadata/metadata.json','metadata_sha256':digest},'artifacts':[{'file':n,'bytes':(p/n).stat().st_size,'sha256':sha256(p/n)} for n in ('current.ext4','lower-000.ext4')]}
            verify_manifest(m,p)
            m['upper_subdir']='disk/new-id/upper'
            with self.assertRaises(Invalid):verify_manifest(m,p)
            m['upper_subdir']='disk/tpl-reviewed_0/upper';m['source_metadata_sha256']['template_snapshot_metadata']='b'*64
            with self.assertRaises(Invalid):verify_manifest(m,p)

    def test_host_guard_precedes_any_disk_command(self):
        with patch('rescue_export.require_rescue',side_effect=Invalid('host')),patch('rescue_export.run') as run:
            with self.assertRaises(Invalid):export(Path('/input'),Path('/work'))
            run.assert_not_called()
    @patch('os.listxattr',return_value=[],create=True)
    def test_export_special_files_fail_instead_of_omission(self, _xattrs):
        import os
        with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
            p=Path(tmp).resolve(); (p/'file').write_bytes(b'ok');reject_unexportable_metadata(p)
            os.mkfifo(p/'pipe')
            with self.assertRaises(Invalid):reject_unexportable_metadata(p)

    @patch('os.listxattr',return_value=[],create=True)
    def test_hardlink_outside_home_rejected(self, _xattrs):
        import os
        with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
            root=Path(tmp).resolve();home=root/'home';home.mkdir();f=home/'one';f.write_bytes(b'x')
            os.link(f,home/'two');reject_unexportable_metadata(home)
            os.link(f,root/'outside')
            with self.assertRaises(Invalid):reject_unexportable_metadata(home)

    @patch('os.listxattr',return_value=[],create=True)
    def test_supervisor_socket_exception_is_exact_and_never_omits_regular_data(self, _xattrs):
        import socket
        with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
            p=Path(tmp).resolve();runtime=p/'.runtimed';runtime.mkdir()
            endpoint=runtime/'sock'
            endpoint.write_bytes(b'regular evidence must survive')
            self.assertEqual(reject_unexportable_metadata(p),[])
            endpoint.unlink()
            with socket.socket(socket.AF_UNIX) as sock:
                sock.bind(str(endpoint))
                self.assertEqual(reject_unexportable_metadata(p),['.runtimed/sock'])
            with socket.socket(socket.AF_UNIX) as sock:
                sock.bind(str(runtime/'another.sock'))
                with self.assertRaises(Invalid):reject_unexportable_metadata(p)

    @patch('os.listxattr',return_value=[],create=True)
    def test_only_reviewed_pg_runtime_socket_excluded(self, _xattrs):
        import socket
        with tempfile.TemporaryDirectory(dir='/tmp') as tmp:
            p=Path(tmp).resolve();run=p/'.baarcha-postgres/run';run.mkdir(parents=True)
            with socket.socket(socket.AF_UNIX) as sock:
                sock.bind(str(run/'.s.PGSQL.5432'))
                self.assertEqual(reject_unexportable_metadata(p),['.baarcha-postgres/run/.s.PGSQL.5432'])
            with socket.socket(socket.AF_UNIX) as sock:
                sock.bind(str(run/'unexpected'))
                with self.assertRaises(Invalid):reject_unexportable_metadata(p)

if __name__=='__main__':unittest.main()
