import fcntl,pathlib,tempfile,threading,time,unittest
import worker

class UpdateLock(unittest.TestCase):
    def test_waits_for_other_update_then_acquires(self):
        with tempfile.TemporaryDirectory() as d:
            path=pathlib.Path(d)/'lock'
            with path.open('a+') as first,path.open('a+') as second:
                fcntl.flock(first,fcntl.LOCK_EX)
                timer=threading.Timer(.05,lambda:fcntl.flock(first,fcntl.LOCK_UN));timer.start()
                try:self.assertTrue(worker.acquire_update_lock(second,.5))
                finally:timer.join()
                with self.assertRaises(BlockingIOError):fcntl.flock(first,fcntl.LOCK_EX|fcntl.LOCK_NB)
    def test_busy_has_bounded_wait(self):
        with tempfile.TemporaryDirectory() as d:
            path=pathlib.Path(d)/'lock'
            with path.open('a+') as first,path.open('a+') as second:
                fcntl.flock(first,fcntl.LOCK_EX);began=time.monotonic()
                self.assertFalse(worker.acquire_update_lock(second,.03))
                self.assertLess(time.monotonic()-began,.5)
                with self.assertRaises(BlockingIOError):fcntl.flock(second,fcntl.LOCK_EX|fcntl.LOCK_NB)
if __name__=='__main__':unittest.main()
