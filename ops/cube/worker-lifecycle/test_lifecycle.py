import fcntl
import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest import mock

spec=importlib.util.spec_from_file_location('lifecycle',Path(__file__).with_name('lifecycle.py'))
life=importlib.util.module_from_spec(spec);spec.loader.exec_module(life)

class Child:
    pid=12345
    code=None
    def poll(self):return self.code

class LifecycleTests(unittest.TestCase):
    def setup_state(self,drain=None,publish=None):
        self.child=Child();self.child.code=None;self.now=0.;self.rows=[];self.power=mock.Mock()
        if drain is None:drain=mock.Mock(return_value={'verified':True,'qemu_pid':self.child.pid})
        self.drain=drain
        self.state=life.Supervisor(self.child,{'qemu_pid':self.child.pid},drain,self.power,publish or self.rows.append,lambda:self.now)
        return self.state
    def test_signals_never_forward_and_failed_drain_keeps_running(self):
        state=self.setup_state(mock.Mock(side_effect=life.Blocked('busy tasks')))
        for sig in (signal.SIGTERM,signal.SIGINT):state.signal(sig)
        self.assertTrue(state.tick());self.assertEqual(state.state,'stop-blocked')
        for _ in range(20):self.now+=60;self.assertTrue(state.tick());state.signal(signal.SIGTERM)
        self.drain.assert_called_once();self.power.assert_not_called();self.assertIsNone(self.child.code)
    def test_success_waits_for_actual_qemu_exit(self):
        state=self.setup_state();state.signal(signal.SIGTERM);self.assertTrue(state.tick())
        self.assertEqual(state.state,'powerdown-wait');self.power.assert_called_once()
        self.child.code=0;self.assertFalse(state.tick());self.assertEqual(state.state,'stopped-clean')
    def test_shutdown_timeout_never_kills_or_reissues_powerdown(self):
        state=self.setup_state();state.signal(signal.SIGTERM);state.tick();self.now=181
        for _ in range(4):self.assertTrue(state.tick())
        self.assertEqual(state.state,'stop-blocked');self.power.assert_called_once();self.assertIsNone(self.child.code)
    def test_unexpected_exit_never_declared_clean(self):
        state=self.setup_state();self.child.code=0;self.assertFalse(state.tick());self.assertFalse(state.clean)
        self.assertEqual(state.state,'worker-lost')
    def test_missing_or_wrong_generation_proof_refuses_powerdown(self):
        for proof in (None,{}, {'verified':True,'qemu_pid':12346}):
            with self.subTest(proof=proof):
                state=self.setup_state(mock.Mock(return_value=proof));state.signal(signal.SIGTERM);self.assertTrue(state.tick());self.power.assert_not_called()
    def test_configuration_cannot_enable_unimplemented_coordinator(self):
        with self.assertRaises(life.Blocked):
            life.host_preflight({"version":1,"reviewed":True,"drain_integration_reviewed":True})
    def test_stop_proof_requires_exact_qemu_generation_and_paused_set(self):
        identity={'qemu_pid':42,'qemu_start_time':'123'}
        good={'version':1,'verified':True,'qemu_pid':42,'qemu_start_time':'123','guest_states':{'fixture':'paused'},'provider_jobs':0}
        self.assertEqual(life.validate_stop_proof(identity,good),good)
        for key,value in (('qemu_start_time','124'),('qemu_pid',43),('provider_jobs',1),('guest_states',{'fixture':'running'}),('verified',False)):
            changed=dict(good);changed[key]=value
            with self.assertRaises(life.Blocked):life.validate_stop_proof(identity,changed)
    def test_unwired_production_coordinator_always_refuses(self):
        with self.assertRaises(life.Blocked):life.prepare_stop({'qemu_pid':12345,'paused':True})
    def test_status_write_failure_retains_supervisor(self):
        state=self.setup_state(publish=mock.Mock(side_effect=OSError('disk full')))
        state.signal(signal.SIGTERM);self.assertTrue(state.tick());self.assertEqual(state.state,'powerdown-wait')
    def test_qmp_only_sends_capabilities_and_graceful_powerdown(self):
        with tempfile.TemporaryDirectory() as directory:
            path=str(Path(directory)/'qmp');server=socket.socket(socket.AF_UNIX);server.bind(path);server.listen(1);commands=[];errors=[]
            def accept():
                try:
                    connection,_=server.accept()
                    with connection:
                        stream=connection.makefile('rwb');stream.write(b'{"QMP":{}}\n');stream.flush()
                        for _ in range(2):
                            item=json.loads(stream.readline());commands.append(item['execute']);stream.write(json.dumps({'return':{},'id':item['id']}).encode()+b'\n');stream.flush()
                except Exception as e:errors.append(e)
            thread=threading.Thread(target=accept);thread.start()
            try:life.qmp_powerdown(path)
            finally:thread.join(2);server.close()
            self.assertFalse(thread.is_alive());self.assertEqual(errors,[]);self.assertEqual(commands,['qmp_capabilities','system_powerdown'])
    def test_lifetime_shared_backup_and_exclusive_instance_fence(self):
        with tempfile.TemporaryDirectory() as directory:
            directory=Path(directory).resolve();backup=directory/'backup';instance=directory/'instance'
            with life.lifetime_lock(instance,life.INSTANCE_MARKER,True),life.lifetime_lock(backup,life.BACKUP_MARKER,False):
                for path,marker in ((backup,life.BACKUP_MARKER),(instance,life.INSTANCE_MARKER)):
                    with self.assertRaises(BlockingIOError):
                        with life.lifetime_lock(path,marker,True):pass
                state=self.setup_state(mock.Mock(side_effect=life.Blocked('unwired')));state.signal(signal.SIGTERM);state.tick()
                with self.assertRaises(BlockingIOError):
                    with life.lifetime_lock(backup,life.BACKUP_MARKER,True):pass
            with life.lifetime_lock(backup,life.BACKUP_MARKER,True):pass
    def test_child_inherits_lock_after_parent_copy_closes(self):
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory).resolve()/'backup'
            with life.lifetime_lock(path,life.BACKUP_MARKER,False) as fd:
                child=subprocess.Popen([sys.executable,'-c','import sys;sys.stdin.buffer.read(1)'],stdin=subprocess.PIPE,pass_fds=(fd,))
            try:
                with self.assertRaises(BlockingIOError):
                    with life.lifetime_lock(path,life.BACKUP_MARKER,True):pass
            finally:child.communicate(b'x',timeout=3)
            with life.lifetime_lock(path,life.BACKUP_MARKER,True):pass
    def test_symlink_and_wrong_protocol_locks_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory).resolve();path=root/'wrong';path.write_bytes(b'wrong');path.chmod(0o600)
            with self.assertRaises(life.Blocked):
                with life.lifetime_lock(path,life.BACKUP_MARKER,False):pass
            link=root/'link';link.symlink_to(path)
            with self.assertRaises(OSError):
                with life.lifetime_lock(link,life.BACKUP_MARKER,False):pass
    def test_unit_does_not_force_kill_or_restart_worker(self):
        unit=Path(__file__).with_name('baarcha-cube-worker-01.service').read_text()
        for required in ('KillMode=process','SendSIGKILL=no','TimeoutStopSec=infinity','Restart=no'):
            self.assertIn(required,unit)
        for forbidden in ('ExecStop=','WatchdogSec=','RuntimeMaxSec=','Restart=always','KillMode=control-group'):
            self.assertNotIn(forbidden,unit)
    def test_qemu_arguments_stay_equal_to_reviewed_disk_and_resource_scope(self):
        argv=life.fixed_qemu();self.assertEqual(argv[0],'/usr/bin/qemu-system-x86_64')
        self.assertEqual(argv[argv.index('-m')+1],'40960');self.assertEqual(argv[argv.index('-smp')+1],'12')
        self.assertEqual(argv.count('-drive'),3);self.assertNotIn('-daemonize',argv)
    def test_nested_identity_and_hash_checks_fail_closed(self):
        value={'version':1,'reviewed':True,'machine_id':'fixture-machine','data_filesystem_uuid':'fixture-uuid','binaries':{name:{'path':'/usr/local/services/'+name,'sha256':'fixed'} for name in ('cubelet','cubemaster','cube-api')},'artifacts':{'/etc/systemd/system/cube-sandbox-cube-api.service':'fixed'}}
        with mock.patch.object(life.socket,'gethostname',return_value='baarcha-cube-worker-01'),mock.patch.object(Path,'read_text',return_value='fixture-machine'),mock.patch.object(life,'run',return_value=b'fixture-uuid\n'),mock.patch.object(life,'digest',return_value='fixed'),mock.patch.object(life.os,'statvfs',return_value=mock.Mock(f_bavail=64*1024**3,f_frsize=1)):
            self.assertFalse(life.nested_preflight(value)['tenant_ready'])
            value['data_filesystem_uuid']='wrong'
            with self.assertRaises(life.Blocked):life.nested_preflight(value)
            value['data_filesystem_uuid']='fixture-uuid';value['binaries']['cubelet']['sha256']='unreviewed'
            with self.assertRaises(life.Blocked):life.nested_preflight(value)

if __name__=='__main__':unittest.main()
