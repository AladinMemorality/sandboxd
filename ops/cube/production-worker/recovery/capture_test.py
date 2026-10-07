import unittest
from capture import validate_fence, copy_immutable, sha256
from plan import Invalid
class FenceTests(unittest.TestCase):
    def test_exact_changed_boot_and_bounded_exclusive_receipt(self):
        f={'purpose':'CUBE_CURRENT_DISK_CAPTURE','sandbox_id':'owned','worker_machine_id':'1234567890abcdef1234567890abcdef','current_boot_id':'new','previous_boot_id':'old','expires_at':110,'no_task_verified':True,'management_fenced':True}
        validate_fence(f,{'sandbox_id':'owned'},'new','1234567890abcdef1234567890abcdef',100)
        for key,value in [('sandbox_id','other'),('worker_machine_id','other'),('current_boot_id','old'),('previous_boot_id','new'),('expires_at',99),('expires_at',2000),('no_task_verified',False),('management_fenced',False)]:
            with self.subTest(key=key),self.assertRaises(Invalid):validate_fence(dict(f,**{key:value}),{'sandbox_id':'owned'},'new','1234567890abcdef1234567890abcdef',100)
    def test_trusted_image_copy_is_independent_and_exact(self):
        import tempfile, os
        from pathlib import Path
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);src=root/'image';dst=root/'copy'
            src.write_bytes(b'trusted lower'*8192)
            identity=copy_immutable(src,dst)
            self.assertEqual(identity['sha256'],sha256(dst))
            self.assertNotEqual(src.stat().st_ino,dst.stat().st_ino)
            self.assertEqual(dst.stat().st_mode & 0o777,0o600)
            with self.assertRaises(FileExistsError):copy_immutable(src,dst)
            sym=root/'link';sym.symlink_to(src)
            with self.assertRaises(OSError):copy_immutable(sym,root/'rejected')

    def test_individual_runtime_fence_does_not_invent_worker_reboot(self):
        f={'purpose':'CUBE_FENCED_RUNTIME_DISK_CAPTURE','sandbox_id':'owned','worker_machine_id':'1234567890abcdef1234567890abcdef','current_boot_id':'same','expires_at':110,'no_task_verified':True,'management_fenced':True,'provider_requests_drained':True,'no_disk_handles_verified':True,'source_identity':{'device':1,'inode':2,'bytes':3,'mtime_ns':4}}
        validate_fence(f,{'sandbox_id':'owned'},'same',f['worker_machine_id'],100)
        for key,value in [('provider_requests_drained',False),('no_disk_handles_verified',False),('source_identity',{}),('source_identity',{'device':True,'inode':2,'bytes':3,'mtime_ns':4}),('current_boot_id','different'),('purpose','CUBE_CURRENT_DISK_CAPTURE')]:
            with self.subTest(key=key),self.assertRaises(Invalid):
                validate_fence(dict(f,**{key:value}),{'sandbox_id':'owned'},'same',f['worker_machine_id'],100)

    def test_b200_image_alias_requires_exact_root_and_rejects_nested_links(self):
        import tempfile, os, capture
        from pathlib import Path
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp).resolve();cache=root/'cache';cache.mkdir();alias=root/'images';alias.symlink_to(cache)
            image=cache/'image.ext4';image.write_bytes(b'immutable');image.chmod(0o600)
            with patch.object(capture,'IMAGE_ALIAS',alias), patch.object(capture,'FLEET_IMAGE_CACHE',cache), patch.object(capture.socket,'gethostname',return_value='baarcha-cube-worker-b200-01'):
                self.assertEqual(capture.lower_image_source(alias/image.name),image)
                image.chmod(0o666)
                with self.assertRaises(Invalid):capture.lower_image_source(alias/image.name)
                image.chmod(0o600)
                link=cache/'nested.ext4';link.symlink_to(image)
                with self.assertRaises(Invalid):capture.lower_image_source(alias/link.name)
                with patch.object(capture.socket,'gethostname',return_value='other'):
                    with self.assertRaises(Invalid):capture.lower_image_source(alias/image.name)
                alias.unlink();alias.symlink_to(root)
                with self.assertRaises(Invalid):capture.lower_image_source(alias/image.name)

if __name__=='__main__':unittest.main()
