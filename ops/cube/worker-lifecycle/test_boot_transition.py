import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('boot_transition',Path(__file__).with_name('boot_transition.py'))
b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)

OLD='11111111-1111-1111-1111-111111111111'; NEW='22222222-2222-2222-2222-222222222222'
MACHINE='a'*32; OUTER='33333333-3333-3333-3333-333333333333'; FS='44444444-4444-4444-4444-444444444444'

class Fixture:
    def __init__(self,root):
        self.root=root; self.stop=root/'stop.json'; self.guard=root/'guard.json';self.compose=root/'compose.json';self.activefile=root/'active.json';self.start=root/'start.json';self.sequence=root/'sequence.json';self.offline=root/'offline.json';self.job=root/'job';self.job.mkdir()
        self.g={'worker_machine_id':MACHINE,'expected_boot_id':OLD,'outer_boot_id':OUTER,'inner_fs_uuid':FS,'outer_fs_uuid':FS,'observer_id':'b'*32,'observation_path':str(root/'observation.json')}
        policy={'max_active':4,'cpu_count':2,'memory_mb':2048,'writable_disk_mb':10240,'templates':{'reviewed':{'cpu_count':2,'memory_mb':2048}},'storage_guard':self.g}
        self.s={'controller_id':'c'*64,'admission':policy,'worker_machine_id':MACHINE,'worker_boot_id':OLD,'data_uuid':FS,'qemu_pid':10,'qemu_start_time':'123','database':str(root/'database'),'receipt':str(root/'drain.json'),'evidence_directory':str(root)}
        self.c={'services':{'sandboxd':{'image':'sha256:'+'d'*64,'environment':{'SANDBOXD_CUBE_ADMISSION':json.dumps(policy),'API_SECRET':'opaque-secret','ROLLOUT_ALLOWLIST':'unchanged'}},'cube-management-api':{'network_mode':'service:sandboxd'},'cube-management-proxy':{'network_mode':'service:sandboxd'}}}
        self.a={'services':{'sandboxd':{'image':self.c['services']['sandboxd']['image'],'environment':copy.deepcopy(self.c['services']['sandboxd']['environment'])}}}
        self.gen={'worker_machine_id':MACHINE,'inner_fs_uuid':FS,'worker_boot_id':NEW,'outer_boot_id':OUTER,'qemu_pid':20,'qemu_start_time':'456'}
        bindings=[dict(SandboxID='stable',AppID='app',RuntimeID='provider',TemplateID='reviewed',Domain='private.invalid',ConfigSHA256='e'*64,OwnerSHA256='f'*64,ConfigRevision=8)]
        self.marker=dict(version=1,phase='preparing',receipt_sha256='1'*64,inventory_sha256='2'*64,bindings=bindings,qemu_pid=10,qemu_start_time='123',worker_boot_id=OLD,worker_machine_id=MACHINE,data_uuid=FS)
        self.pause={k:self.marker[k] for k in ('worker_machine_id','data_uuid','version','qemu_pid','qemu_start_time','worker_boot_id','inventory_sha256','receipt_sha256')}
        self.pause.update(verified=True,provider_jobs=0,guest_states={'provider':'paused'},generated_at='2026-01-01T00:00:00Z')
        self.clean={'version':1,'state':'stopped-clean','generated_at':1767225601,'proof':self.pause}
        self.drain={k:self.s[k] for k in ('controller_id','worker_boot_id','qemu_pid','qemu_start_time')}
        self.drain.update(version=1,inventory_sha256=self.marker['inventory_sha256'],traffic_fenced=True,existing_requests_drained=True,direct_writers_fenced=True,provider_jobs_drained=True)
        self.sc={'version':1,'pause_proof':str(root/'pause.json'),'clean_receipt':str(root/'clean.json')}
        for path,value in [(self.stop,self.s),(self.guard,self.g),(self.compose,self.c),(self.activefile,self.a),(self.start,self.sc),(self.sequence,{'observer_id':'b'*32,'generation':30}), (root/'database.worker-stop.json',self.marker),(root/'pause.json',self.pause),(root/'clean.json',self.clean),(root/'drain.json',self.drain),(self.offline,{'offline':True})]:self.write(path,b.encoded(value))
        self.plan={'version':1,'outer_machine_id':MACHINE,'files':{str(self.start):b.sha(self.start.read_bytes()),str(self.offline):b.sha(self.offline.read_bytes())},'initial':{str(p):b.sha(p.read_bytes()) for p in (self.stop,self.guard,self.compose,self.activefile)},'controller_image':'sha256:'+'d'*64,'disk_identity':{n:{'inode':i+1,'virtual_bytes':4096,'filesystem_uuid':FS} for i,n in enumerate(('root.qcow2','data.qcow2','seed.img'))}}
        self.activate_count=0;self.reconcile_count=0;self.generation_count=0;self.fail=None;self.active=0
    @staticmethod
    def write(path,raw):Path(path).write_bytes(raw)
    def patches(self):
        from contextlib import ExitStack
        stack=ExitStack()
        for name,value in [('STOP',self.stop),('GUARD',self.guard),('COMPOSE',self.compose),('ACTIVE',self.activefile),('START',self.start),('SEQUENCE',self.sequence),('OFFLINE',self.offline),('PINNED',(self.start,self.offline))]:stack.enter_context(patch.object(b,name,value))
        stack.enter_context(patch.object(b,'trusted',lambda p,*a,**kw:Path(p).read_bytes()))
        stack.enter_context(patch.object(b,'atomic',self.write)); return stack
    def fence(self):
        if self.fail=='fence':raise b.Refused('fence absent')
    def old_controller(self,stop):return {'Config':{'Env':[k+'='+v for k,v in self.c['services']['sandboxd']['environment'].items()]}}
    def generation(self,stop):self.generation_count+=1;return self.gen
    def refresh_observer(self):
        if self.fail=='observer':raise b.Refused('stale observer')
    def fresh(self,guard,minimum):
        assert minimum==30 and guard['expected_boot_id']==NEW
    def reconcile(self):
        self.reconcile_count+=1
        proof={'tenant_ready':True,'guests_woken':0,'routing_changed':False,'inventory_sha256':self.marker['inventory_sha256'],'worker_boot_id':NEW,'qemu_pid':20,'qemu_start_time':'456','stop_marker_sha256':b.go_marker_hash(self.marker)}
        self.write(self.root/'startup-current.json',b.encoded(proof));(self.root/'database.worker-stop.json').unlink()
        if self.fail=='after-marker':raise b.Refused('crash after exact marker removal')
        return proof
    def activate(self):
        self.activate_count+=1
        if self.fail=='activation':raise b.Refused('ambiguous compose result')
    def recreated(self,env):
        assert env['API_SECRET']=='opaque-secret' and env['ROLLOUT_ALLOWLIST']=='unchanged'
        assert json.loads(env['SANDBOXD_CUBE_ADMISSION'])['storage_guard']['expected_boot_id']==NEW
        return '9'*64
    def observe(self):
        if self.fail=='observe':raise b.Refused('changed bindings')
        return {'consistent':True,'worker_boot_id':NEW,'active':self.active,'bindings':1}

class TransitionTests(unittest.TestCase):
    def fixture(self):
        tmp=tempfile.TemporaryDirectory();self.addCleanup(tmp.cleanup);return Fixture(Path(tmp.name))
    def test_actual_file_cas_full_flow_preserves_policy_and_scope(self):
        f=self.fixture()
        with f.patches():out=b.transition(f.plan,f.job,f)
        self.assertTrue(out['tenant_ready']);self.assertFalse(out['routing_changed']);self.assertEqual(f.activate_count,1)
        stop=json.loads(f.stop.read_bytes());self.assertEqual(stop['controller_id'],'9'*64)
        self.assertEqual(stop['worker_boot_id'],NEW);self.assertEqual(stop['admission']['max_active'],4)
        c=json.loads(f.compose.read_bytes());self.assertEqual(c['services']['sandboxd']['environment']['API_SECRET'],'opaque-secret')
        active=json.loads(f.activefile.read_bytes())
        self.assertEqual(active['services']['sandboxd']['image'],f.a['services']['sandboxd']['image'])
        self.assertEqual(active['services']['sandboxd']['environment']['API_SECRET'],'opaque-secret')
        self.assertEqual(active['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION'],c['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION'])
        self.assertEqual(c['services']['cube-management-api'],f.c['services']['cube-management-api'])
        self.assertEqual(json.loads(f.sequence.read_bytes())['generation'],30) # helper never resets/writes epochs
    def test_native_clear_before_journal_persist_recovers_exact_evidence(self):
        f=self.fixture();f.fail='after-marker'
        with f.patches():
            with self.assertRaises(b.Refused):b.transition(f.plan,f.job,f)
            self.assertFalse(json.loads((f.job/'transition.json').read_bytes())['tenant_ready'])
            f.fail=None;out=b.transition(f.plan,f.job,f)
        self.assertTrue(out['tenant_ready']);self.assertEqual(f.reconcile_count,1)
    def test_fake_startup_receipt_cannot_clear_interrupted_fence(self):
        f=self.fixture();f.fail='after-marker'
        with f.patches():
            with self.assertRaises(b.Refused):b.transition(f.plan,f.job,f)
            value=json.loads((f.root/'startup-current.json').read_bytes());value['stop_marker_sha256']='0'*64;f.write(f.root/'startup-current.json',b.encoded(value));f.fail=None
            with self.assertRaises(b.Refused):b.transition(f.plan,f.job,f)
        self.assertEqual(f.activate_count,0)
    def test_ambiguous_recreate_not_replayed_and_exact_result_can_finish(self):
        f=self.fixture();f.fail='activation'
        with f.patches():
            with self.assertRaises(b.Refused):b.transition(f.plan,f.job,f)
            f.fail=None;out=b.transition(f.plan,f.job,f)
        self.assertTrue(out['tenant_ready']);self.assertEqual(f.activate_count,1)
    def test_pin_save_before_cas_replays_only_exact_old_or_new(self):
        f=self.fixture()
        with f.patches():
            old={str(p):p.read_bytes() for p in (f.stop,f.guard,f.compose)};j=b.Journal(f.job,f.plan)
            j.begin(old,b.new_configs(f.s,f.g,f.c,f.gen),f.gen,{},{});j.apply()
            original_write=j.write
            def fault(path,raw):
                if Path(path)==f.stop:raise OSError('power interruption')
                original_write(path,raw)
            j.write=fault
            with self.assertRaises(OSError):j.controller_pin('9'*64)
            j=b.Journal(f.job,f.plan);j.controller_pin('9'*64)
            self.assertEqual(json.loads(f.stop.read_bytes())['controller_id'],'9'*64)
    def test_partial_pin_writes_roll_forward_no_rollback_or_rebase(self):
        f=self.fixture()
        with f.patches():
            old={str(p):p.read_bytes() for p in (f.stop,f.guard,f.compose)};wanted=b.new_configs(f.s,f.g,f.c,f.gen)
            j=b.Journal(f.job,f.plan);j.begin(old,wanted,f.gen,{},{});original=j.write;count=0
            def fail_second(path,raw):
                nonlocal count
                if str(path) in wanted:
                    count+=1
                    if count==2:raise OSError('interruption')
                original(path,raw)
            j.write=fail_second
            with self.assertRaises(OSError):j.apply()
            j=b.Journal(f.job,f.plan);j.apply()
            self.assertEqual(json.loads(f.guard.read_bytes())['expected_boot_id'],NEW)
            f.write(f.compose,b.encoded({'unrelated_operator_change':True}))
            with self.assertRaises(b.Refused):j.apply()
    def test_wrong_clean_proof_boot_disk_or_fence_never_changes_pins(self):
        for kind in ('clean','worker','disk','same-boot','same-qemu','drain','binding','fence'):
            with self.subTest(kind=kind):
                f=self.fixture();before=f.stop.read_bytes()
                if kind=='clean':f.clean['state']='worker-lost';f.write(f.root/'clean.json',b.encoded(f.clean))
                elif kind=='worker':f.gen['worker_machine_id']='e'*32
                elif kind=='disk':f.gen['inner_fs_uuid']=OLD
                elif kind=='same-boot':f.gen['worker_boot_id']=OLD
                elif kind=='same-qemu':f.gen.update(qemu_pid=10,qemu_start_time='123')
                elif kind=='drain':f.drain['direct_writers_fenced']=False;f.write(f.root/'drain.json',b.encoded(f.drain))
                elif kind=='binding':f.pause['guest_states']={};f.write(f.root/'pause.json',b.encoded(f.pause))
                else:f.fail='fence'
                with f.patches(),self.assertRaises(b.Refused):b.transition(f.plan,f.job,f)
                self.assertEqual(f.stop.read_bytes(),before);self.assertEqual(f.activate_count,0)
    def test_missing_or_stale_storage_keeps_pending_and_controller_stopped(self):
        f=self.fixture();f.fail='observer'
        with f.patches(),self.assertRaises(b.Refused):b.transition(f.plan,f.job,f)
        self.assertEqual(f.activate_count,0);self.assertEqual(f.reconcile_count,0)
        self.assertFalse(json.loads((f.job/'transition.json').read_bytes())['tenant_ready'])
    def test_final_binding_failure_retains_pending_and_reports_no_readiness(self):
        f=self.fixture();f.fail='observe'
        with f.patches(),self.assertRaises(b.Refused):b.transition(f.plan,f.job,f)
        self.assertFalse(json.loads((f.job/'transition.json').read_bytes())['tenant_ready'])
        self.assertEqual(json.loads(f.stop.read_bytes())['controller_id'],'9'*64)
    def test_normal_always_on_resume_reported_truthfully(self):
        f=self.fixture();f.active=1
        with f.patches():out=b.transition(f.plan,f.job,f)
        self.assertEqual(out['active_after_controller_start'],1)
        self.assertEqual(out['guests_woken_during_offline_reconciliation'],0)
    def test_go_marker_hash_is_stable_after_sorted_journal_roundtrip(self):
        f=self.fixture()
        self.assertEqual(b.go_marker_hash(f.marker),b.go_marker_hash(json.loads(b.encoded(f.marker))))
        self.assertEqual(b.go_marker_hash(f.marker),'0d97a7af2fbb666c75ba16dda56d4c38865ae28975320d30579587cab3e68fd1')
    def test_new_configs_reject_mismatched_native_or_controller_policy(self):
        f=self.fixture()
        for target in ('guard','compose'):
            g=copy.deepcopy(f.g);c=copy.deepcopy(f.c)
            if target=='guard':g['observer_id']='c'*32
            else:c['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION']='{}'
            with self.assertRaises(b.Refused):b.new_configs(f.s,g,c,f.gen)
    def test_active_overlay_conflicting_policy_refused_and_image_only_preserved(self):
        f=self.fixture();changed=copy.deepcopy(f.a)
        changed['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION']='{}'
        with self.assertRaises(b.Refused):b.new_configs(f.s,f.g,f.c,f.gen,changed)
        imageonly={'services':{'sandboxd':{'image':'sha256:'+'d'*64}}}
        with f.patches():
            wanted=b.new_configs(f.s,f.g,f.c,f.gen,imageonly)
            self.assertEqual(wanted[str(f.activefile)],imageonly)
    def test_active_overlay_partial_cas_rolls_forward_and_rejects_drift(self):
        f=self.fixture()
        with f.patches():
            originals={str(p):p.read_bytes() for p in (f.stop,f.guard,f.compose,f.activefile)}
            wanted=b.new_configs(f.s,f.g,f.c,f.gen,f.a);j=b.Journal(f.job,f.plan)
            j.begin(originals,wanted,f.gen,{},{})
            write=j.write
            def fail_active(path,raw):
                if Path(path)==f.activefile:raise OSError('interrupted before highest priority overlay')
                write(path,raw)
            j.write=fail_active
            with self.assertRaises(OSError):j.apply()
            self.assertEqual(f.activefile.read_bytes(),originals[str(f.activefile)])
            b.Journal(f.job,f.plan).apply()
            self.assertEqual(json.loads(json.loads(f.activefile.read_bytes())['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION'])['storage_guard']['expected_boot_id'],NEW)
            f.activefile.write_bytes(b'{}')
            with self.assertRaises(b.Refused):b.Journal(f.job,f.plan).apply()
    @unittest.skipUnless(os.geteuid()==0, 'native root lock ownership required')
    def test_inherited_flocks_remain_owned_by_parent_after_return(self):
        import fcntl
        f=self.fixture();paths=[str(f.root/('lock'+str(i))) for i in range(4)];fds=[]
        try:
            for path in paths:
                fd=os.open(path,os.O_CREAT|os.O_RDWR,0o600);fds.append(fd);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
            with patch.object(b,'LOCKS',tuple(paths)),b.locked(fds):pass
            for path,fd in zip(paths,fds):
                os.fstat(fd) # helper did not close inherited descriptor
                other=os.open(path,os.O_RDWR)
                try:
                    with self.assertRaises(BlockingIOError):fcntl.flock(other,fcntl.LOCK_EX|fcntl.LOCK_NB)
                finally:os.close(other)
            with patch.object(b,'LOCKS',tuple(paths)),self.assertRaises(b.Refused):
                with b.locked(fds[:-1]):pass
        finally:
            for fd in fds:os.close(fd)

    @unittest.skipUnless(os.geteuid()==0, 'native root lock ownership required')
    def test_inherited_unlocked_shared_or_other_holder_refused_without_upgrade(self):
        import fcntl
        for mode in ('unlocked','shared','other'):
            with self.subTest(mode=mode):
                f=self.fixture();path=str(f.root/'lock');fd=os.open(path,os.O_CREAT|os.O_RDWR,0o600);other=None
                try:
                    if mode=='shared':fcntl.flock(fd,fcntl.LOCK_SH|fcntl.LOCK_NB)
                    if mode=='other':other=os.open(path,os.O_RDWR);fcntl.flock(other,fcntl.LOCK_EX|fcntl.LOCK_NB)
                    with patch.object(b,'LOCKS',(path,)),self.assertRaises((b.Refused,BlockingIOError)):
                        with b.locked([fd]):pass
                    os.fstat(fd) # rejection never closes caller-owned FD
                    if mode!='other':
                        probe=os.open(path,os.O_RDONLY)
                        try:fcntl.flock(probe,fcntl.LOCK_SH|fcntl.LOCK_NB)
                        finally:os.close(probe) # no hidden EX upgrade
                finally:
                    os.close(fd)
                    if other is not None:os.close(other)

    def test_observer_refresh_uses_actual_installed_unit(self):
        host=b.Host({})
        with patch.object(host,'command',return_value=b'') as command:host.refresh_observer()
        command.assert_called_once_with(['/usr/bin/systemctl','start','baarcha-cube-storage-observer.service'],30)

    def test_closed_directory_symlink_input_refused(self):
        f=self.fixture(); link=f.root/'link';link.symlink_to(f.stop)
        with self.assertRaises(b.Refused):b.trusted(link)

class RealHTTPHelperTests(unittest.TestCase):
    def test_real_loopback_http_helper_and_status_refusal(self):
        import http.server
        import threading
        requests=[]
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                requests.append(self.path)
                self.send_response(200 if self.path=='/readyz' else 503)
                self.end_headers();self.wfile.write(b'ready' if self.path=='/readyz' else b'unavailable')
            def log_message(self,*args):pass
        server=http.server.HTTPServer(('127.0.0.1',0),Handler)
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        try:
            self.assertEqual(b.http('/readyz',server.server_port),b'ready')
            with self.assertRaises(b.Refused):b.http('/unavailable',server.server_port)
            self.assertEqual(requests,['/readyz','/unavailable'])
        finally:
            server.shutdown();server.server_close();thread.join(timeout=2)
        self.assertFalse(thread.is_alive())

if __name__=='__main__':unittest.main()
