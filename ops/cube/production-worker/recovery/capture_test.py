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

if __name__=='__main__':unittest.main()
