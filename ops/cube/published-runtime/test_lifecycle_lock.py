import unittest
import tempfile
from pathlib import Path
from lifecycle_lock import observe_retained_locks
from lifecycle_lock import guarded_execute, PREFIX

CID='a'*32
class Lease:
    def __init__(self):self.value=None;self.commands=[]
    def __call__(self,*args):
        self.commands.append(args)
        if args[0]=='SET':
            if self.value is not None:return None
            self.value=args[2];return 'OK'
        if args[0]=='EVAL':
            if self.value!=args[-1]:return 0
            self.value=None;return 1
        raise AssertionError(args)

class LifecycleLock(unittest.TestCase):
    def test_maintenance_holds_the_same_native_lifecycle_key_until_guest_exits(self):
        lock=Lease()
        def execute():
            self.assertIsNotNone(lock.value)
            self.assertIsNone(lock('SET',PREFIX+CID,'pause','NX','EX',180))
            return {'status':'current'}
        self.assertEqual(guarded_execute(CID,execute,lock,lambda _:True)['status'],'current')
        self.assertIsNone(lock.value)
        self.assertEqual(lock.commands[0][1],PREFIX+CID)
    def test_native_pause_wins_without_guest_exec(self):
        lock=Lease();lock.value='pause:another-owner'
        out=guarded_execute(CID,lambda:self.fail('exec raced pause'),lock,lambda _:True)
        self.assertEqual(out['status'],'busy');self.assertEqual(lock.value,'pause:another-owner')
    def test_state_is_rechecked_under_lock_and_stopped_guests_are_never_woken(self):
        lock=Lease()
        def running(_):self.assertIsNotNone(lock.value);return False
        out=guarded_execute(CID,lambda:self.fail('stopped guest executed'),lock,running)
        self.assertEqual(out['status'],'skipped');self.assertIsNone(lock.value)
    def test_uncertain_exec_retains_fence_without_timeout_release(self):
        lock=Lease()
        def execute():raise TimeoutError()
        with self.assertRaises(TimeoutError):guarded_execute(CID,execute,lock,lambda _:True)
        self.assertIsNotNone(lock.value);self.assertNotIn('EX',lock.commands[0])
    def test_observation_failure_before_exec_releases_fence(self):
        lock=Lease()
        def running(_):raise OSError()
        with self.assertRaises(OSError):guarded_execute(CID,lambda:self.fail('exec'),lock,running)
        self.assertIsNone(lock.value)
    def test_changed_lock_is_never_deleted(self):
        lock=Lease()
        def execute():lock.value='different-owner';return {}
        with self.assertRaises(RuntimeError):guarded_execute(CID,execute,lock,lambda _:True)
        self.assertEqual(lock.value,'different-owner')
class Observation(unittest.TestCase):
    def test_retained_fences_alert_without_exposing_tokens_or_releasing(self):
        commands=[]
        token='supervisor-maintenance:private'
        def redis(*args):
            commands.append(args)
            if args[0]=='SCAN':return ['0',[PREFIX+CID]]
            if args[0]=='GET':return token
            raise AssertionError('Observer must not write Redis')
        with tempfile.TemporaryDirectory() as root:
            path=Path(root)/'health.json'
            self.assertFalse(observe_retained_locks(redis,path,100)['issues'])
            self.assertTrue(observe_retained_locks(redis,path,400)['issues'])
            self.assertNotIn('private',path.read_text())
            token='supervisor-maintenance:new-generation'
            self.assertFalse(observe_retained_locks(redis,path,500)['issues'])
            token='native-pause-token'
            self.assertFalse(observe_retained_locks(redis,path,900)['tracked'])

if __name__=='__main__':unittest.main()
