import importlib.util
import os
from pathlib import Path
import stat
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('fixture_run',Path(__file__).with_name('run.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

class Locks(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.path=Path(self.tmp.name).resolve()/'acceptance.lock'
        self.path.write_text('preserve');self.path.chmod(0o600)
    def tearDown(self):self.tmp.cleanup()
    def owned(self,fd):
        s=os.stat(self.path)
        return type('Stat',(),{'st_mode':s.st_mode,'st_uid':0,'st_nlink':1})()
    def test_real_lock_contention_and_no_truncation(self):
        import fcntl
        with patch.object(m.os,'fstat',side_effect=self.owned):
            handles=m.acquire((str(self.path),))
            try:
                fd=os.open(self.path,os.O_RDWR)
                try:
                    with self.assertRaises(BlockingIOError):fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
                finally:os.close(fd)
            finally:
                for fd in handles:os.close(fd)
        self.assertEqual(self.path.read_text(),'preserve')
    def test_refuses_symlink_and_missing_lock(self):
        link=self.path.with_name('link');link.symlink_to(self.path)
        with self.assertRaises(ValueError):m.acquire((str(link),))
        with self.assertRaises(FileNotFoundError):m.acquire((str(self.path.with_name('missing')),))
    def test_wrong_owner_and_hardlink_refused(self):
        for uid,nlink in ((1000,1),(0,2)):
            with patch.object(m.os,'fstat',return_value=type('Stat',(),{'st_mode':stat.S_IFREG|0o600,'st_uid':uid,'st_nlink':nlink})()):
                with self.assertRaises(ValueError):m.acquire((str(self.path),))
    def test_exact_supported_actions_do_not_include_recovery_deletion(self):
        self.assertEqual(m.ACTIONS,('prepare','resume-rejected-app','create','fund','task','complete-timed-out-task','complete-after-credit','verify','inspect'))
        self.assertNotIn('cleanup',m.ACTIONS)

if __name__=='__main__':unittest.main()
