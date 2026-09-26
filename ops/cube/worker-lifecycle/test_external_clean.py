import copy
import importlib.util
from pathlib import Path
import unittest

spec=importlib.util.spec_from_file_location('external_clean',Path(__file__).with_name('external_clean.py'));x=importlib.util.module_from_spec(spec);spec.loader.exec_module(x)

class ExternalTests(unittest.TestCase):
    def fixture(self):
        i={'version':1,'method':x.METHOD,'supervisor_pid':42,'supervisor_start_time':'100','qemu_pid':43,'qemu_start_time':'101','worker_boot_id':'boot','worker_machine_id':'machine','data_uuid':'data','outer_boot_id':'outer','qmp_socket_inode':99}
        p={k:i[k] for k in ('worker_machine_id','worker_boot_id','data_uuid','qemu_pid','qemu_start_time')};p.update(version=1,verified=True,provider_jobs=0,guest_states={'one':'paused'})
        r={'version':1,'stopped':True,'boot_id':'boot'}
        a={'version':1,'qmp_socket_inode':99,'peer_pid':43,'attached_at':1.0,'sent_at':2.0,'finished_at':4.0,'messages':[{'direction':'send','message':{'execute':'system_powerdown','id':'system_powerdown'}},{'direction':'receive','message':{'event':'SHUTDOWN','data':{'guest':True,'reason':'guest-shutdown'},'timestamp':{'seconds':3,'microseconds':0}}}]}
        trace=b'1.100000 wait4(43, 0x7fff, WNOHANG, NULL) = 0\n3.100000 wait4(43, [{WIFEXITED(s) && WEXITSTATUS(s) == 0}], WNOHANG, NULL) = 43\n'
        status={'state':'worker-lost','qemu_pid':43,'supervisor_pid':42}
        return i,p,r,a,trace,status
    def test_exact_external_exit_is_distinct_and_retains_supervisor_loss(self):
        f=self.fixture();result=x.validate_witness(*f)
        self.assertEqual(result['qemu_exit_code'],0);self.assertEqual(result['supervisor_reported_state'],'worker-lost');self.assertEqual(f[-1]['state'],'worker-lost')
    def test_poll_is_not_completion(self):
        raw=b'1.100000 wait4(43, 0x7fff, WNOHANG, NULL) = 0\n'
        self.assertEqual(x.parse_wait4(raw,42,43)['exits'],[])
        f=list(self.fixture());f[4]=raw
        with self.assertRaises(x.Refused):x.validate_witness(*f)
    def test_no_exit_reclassification_on_missing_wrong_or_partial_witness(self):
        for change in ('ECHILD','signal','exit1','wrongchild','wrongthread','partial','duplicate','lateattach','waitid'):
            with self.subTest(change=change):
                f=list(self.fixture());raw=f[4]
                if change=='ECHILD':raw=raw.replace(b'= 43',b'= -1 ECHILD (No child processes)')
                elif change=='signal':raw=raw.replace(b'WIFEXITED(s) && WEXITSTATUS(s) == 0',b'WIFSIGNALED(s) && WTERMSIG(s) == SIGKILL')
                elif change=='exit1':raw=raw.replace(b'WEXITSTATUS(s) == 0',b'WEXITSTATUS(s) == 1')
                elif change=='wrongchild':raw=raw.replace(b'wait4(43',b'wait4(44')
                elif change=='wrongthread':raw=b'[pid 99] '+raw
                elif change=='partial':raw=raw.replace(b' = 43',b' <unfinished ...>')
                elif change=='duplicate':raw+=raw.splitlines(keepends=True)[1]
                elif change=='lateattach':raw=raw.splitlines(keepends=True)[1]
                else:raw=raw.replace(b'wait4(',b'waitid(')
                f[4]=raw
                with self.assertRaises(x.Refused):x.validate_witness(*f)
    def test_identity_receipt_and_qmp_causes_fail_closed(self):
        for change in ('hostquit','noevent','wrongpeer','wrongboot','runningguest','unverifiedpause','oldproof','clock','statusrewritten','duplicatepower','reset'):
            with self.subTest(change=change):
                f=list(self.fixture());i,p,r,a,trace,status=f
                if change=='hostquit':a['messages'][1]['message']['data']={'guest':False,'reason':'host-qmp-quit'}
                elif change=='noevent':a['messages'].pop()
                elif change=='wrongpeer':a['peer_pid']=44
                elif change=='wrongboot':r['boot_id']='other'
                elif change=='runningguest':p['guest_states']['one']='running'
                elif change=='unverifiedpause':p['verified']=False
                elif change=='oldproof':p['qemu_start_time']='old'
                elif change=='clock':a['sent_at']=4.1
                elif change=='statusrewritten':status['state']='stopped-clean'
                elif change=='duplicatepower':a['messages'].append(copy.deepcopy(a['messages'][0]))
                else:a['messages'].insert(0,{'direction':'send','message':{'execute':'system_reset'}})
                with self.assertRaises(x.Refused):x.validate_witness(*f)
    def test_polling_waits_for_full_tail_but_final_validation_refuses_it(self):
        prefix=b'1.100000 wait4(43, 0x7fff, WNOHANG, NULL) = 0\n'
        partial=prefix+b'3.100000 wait4(43, [{WIFEXITED(s) && WEXITSTATUS(s) == 0}]'
        self.assertEqual(x.complete_trace(partial),prefix)
        self.assertEqual(x.parse_wait4(x.complete_trace(partial),42,43)['exits'],[])
        with self.assertRaises(x.Refused):x.parse_wait4(partial,42,43)
        self.assertEqual(x.complete_trace(b'1.100000 wait'),b'')

    def test_backup_validator_returns_finite_closed_rehashed_evidence(self):
        import tempfile,json
        with tempfile.TemporaryDirectory() as tmp:
            d=Path(tmp);i,p,r,a,trace,status=self.fixture()
            values={'identity.json':x.canonical(i),'pause.json':x.canonical(p),'retained-stop.json':x.canonical(r),'wait4.trace':trace,'strace.stderr':b'','qmp.json':x.canonical(a),'supervisor-actual.json':x.canonical(status)}
            for name,raw in values.items():(d/name).write_bytes(raw)
            manifest={'version':1,'method':x.METHOD,'validated':x.validate_witness(i,p,r,a,trace,status),'artifacts':{n:x.sha(v) for n,v in values.items()}}
            (d/'manifest.json').write_bytes(x.canonical(manifest))
            external={k:i[k] for k in ('outer_boot_id','supervisor_pid','supervisor_start_time','qemu_pid','qemu_start_time','worker_boot_id')};external.update(version=1,method=x.METHOD,evidence_directory=str(d),evidence_sha256=x.sha((d/'manifest.json').read_bytes()))
            receipt={'version':1,'state':'externally-stopped-clean','generated_at':5.0,'proof':p,'external':external};(d/'receipt.json').write_bytes(x.canonical(receipt))
            read=lambda path:Path(path).read_bytes()
            result=x.validate_receipt(d/'receipt.json',read)
            self.assertEqual(len(result['closure']),9)
            self.assertEqual({Path(v['path']).name for v in result['closure']},set(x.EVIDENCE_FILES)|{'manifest.json','receipt.json'})
            # Later live status is unrelated; verifier consumes the retained
            # original worker-lost artifact, never assumes current boot identity.
            (d/'unrelated-current-status.json').write_text('{"state":"running-unreconciled"}')
            self.assertEqual(x.validate_receipt(d/'receipt.json',read)['closure'],result['closure'])
            for name in x.EVIDENCE_FILES:
                original=(d/name).read_bytes();(d/name).write_bytes(original+b' ')
                with self.assertRaises(x.Refused):x.validate_receipt(d/'receipt.json',read)
                (d/name).write_bytes(original)
            manifest['artifacts']['arbitrary-file']='0'*64;(d/'manifest.json').write_bytes(x.canonical(manifest));receipt['external']['evidence_sha256']=x.sha((d/'manifest.json').read_bytes());(d/'receipt.json').write_bytes(x.canonical(receipt))
            with self.assertRaises(x.Refused):x.validate_receipt(d/'receipt.json',read)

    def test_exact_main_thread_prefix_supported(self):
        f=list(self.fixture());f[4]=b'\n'.join(b'[pid 42] '+line for line in f[4].splitlines())+b'\n'
        self.assertTrue(x.validate_witness(*f)['guest_shutdown'])

if __name__=='__main__':unittest.main()

class StartAuthorizationTests(unittest.TestCase):
    def test_consumption_is_no_replace_and_one_use(self):
        import tempfile
        from unittest import mock
        with tempfile.TemporaryDirectory() as tmp:
            d=Path(tmp);auth=d/'authorization';target=d/'consumed';raw=b'authorized'
            auth.write_bytes(raw)
            value={'authorization_sha256':x.sha(raw),'consumed':str(target)}
            with mock.patch.object(x,'START_AUTH',auth),mock.patch.object(x,'WORKER',d),mock.patch.object(x,'private_bytes',lambda p:Path(p).read_bytes()):
                x.consume_start_authorization(value)
                self.assertFalse(auth.exists());self.assertEqual(target.read_bytes(),raw)
                auth.write_bytes(raw)
                with self.assertRaises(x.Refused):x.consume_start_authorization(value)
                self.assertEqual(auth.read_bytes(),raw)
                target.unlink();auth.write_bytes(b'changed')
                with self.assertRaises(x.Refused):x.consume_start_authorization(value)
                self.assertFalse(target.exists())
    def test_crash_after_durable_link_is_fail_closed(self):
        import tempfile
        from unittest import mock
        with tempfile.TemporaryDirectory() as tmp:
            d=Path(tmp);auth=d/'authorization';target=d/'consumed';raw=b'authorized';auth.write_bytes(raw)
            value={'authorization_sha256':x.sha(raw),'consumed':str(target)}
            with mock.patch.object(x,'START_AUTH',auth),mock.patch.object(x,'WORKER',d),mock.patch.object(x,'private_bytes',lambda p:Path(p).read_bytes()):
                original=Path.unlink
                def fail(path,*a,**kw):
                    if path==auth:raise OSError('simulated crash after link fsync')
                    return original(path,*a,**kw)
                with mock.patch.object(Path,'unlink',fail),self.assertRaises(OSError):x.consume_start_authorization(value)
                self.assertTrue(target.exists());self.assertTrue(auth.exists())
                with self.assertRaises(x.Refused):x.consume_start_authorization(value)

@unittest.skipUnless(__import__('os').geteuid()==0 and __import__('sys').platform=='linux','native root secure-path fixture')
class NativeStartAuthorizationTests(unittest.TestCase):
    def test_closed_evidence_exact_disks_status_and_dead_generations_required(self):
        import tempfile,json,os
        from unittest import mock
        with tempfile.TemporaryDirectory(prefix='cube-external-auth-',dir='/root') as temp:
            root=Path(temp);evidence=root/'evidence';evidence.mkdir(mode=0o700)
            i,p,r,a,trace,status=ExternalTests().fixture()
            values={'identity.json':x.canonical(i),'pause.json':x.canonical(p),'retained-stop.json':x.canonical(r),'wait4.trace':trace,'strace.stderr':b'','qmp.json':x.canonical(a),'supervisor-actual.json':x.canonical(status)}
            for name,raw in values.items():x.publish(evidence/name,raw)
            manifest={'version':1,'method':x.METHOD,'validated':x.validate_witness(i,p,r,a,trace,status),'artifacts':{n:x.sha(v) for n,v in values.items()}}
            x.publish(evidence/'manifest.json',manifest)
            external={k:i[k] for k in ('outer_boot_id','supervisor_pid','supervisor_start_time','qemu_pid','qemu_start_time','worker_boot_id')};external.update(version=1,method=x.METHOD,evidence_directory=str(evidence),evidence_sha256=x.sha((evidence/'manifest.json').read_bytes()))
            receipt={'version':1,'state':'externally-stopped-clean','generated_at':5.0,'proof':p,'external':external};x.publish(evidence/'receipt.json',receipt)
            helper=root/'external.py';x.publish(helper,b'# exact reviewed fixture\n');x.publish(root/'lifecycle-status.json',status);x.publish(root/'stop.json',p)
            disks=[root/n for n in ('root.qcow2','data.qcow2','seed.img')];identities={}
            for disk in disks:
                x.publish(disk,b'immutable retained source');s=disk.stat();identities[str(disk)]={'device':s.st_dev,'inode':s.st_ino,'size':s.st_size,'mtime_ns':s.st_mtime_ns}
            auth={'version':1,'purpose':'external-clean-one-use-start','receipt':str(evidence/'receipt.json'),'receipt_sha256':x.sha((evidence/'receipt.json').read_bytes()),'actual_status_sha256':x.sha((root/'lifecycle-status.json').read_bytes()),'stop_config_sha256':x.sha((root/'stop.json').read_bytes()),'verifier_sha256':x.sha(helper.read_bytes()),'disk_identities':identities}
            authpath=root/'authorization';x.publish(authpath,auth)
            with mock.patch.object(x,'START_AUTH',authpath),mock.patch.object(x,'START_HELPER',helper),mock.patch.object(x,'WORKER',root),mock.patch.object(x,'STOP_CONFIG',root/'stop.json'),mock.patch.object(x,'EXPECTED_DISKS',tuple(disks)):
                verified=x.validate_start_authorization(process_exists=lambda pid:False);self.assertEqual(verified['receipt_sha256'],auth['receipt_sha256'])
                for pid in (42,43):
                    with self.assertRaises(x.Refused):x.validate_start_authorization(process_exists=lambda p:p==pid)
                for name in ('lifecycle-status.json','stop.json','external.py','root.qcow2'):
                    path=root/name;raw=path.read_bytes();st=path.stat();path.write_bytes(raw+b' ')
                    with self.subTest(name=name),self.assertRaises(x.Refused):x.validate_start_authorization(process_exists=lambda pid:False)
                    path.write_bytes(raw);os.utime(path,ns=(st.st_atime_ns,st.st_mtime_ns))
                x.consume_start_authorization(verified)
                self.assertFalse(authpath.exists());self.assertTrue(Path(verified['consumed']).exists())
                x.publish(authpath,auth)
                with self.assertRaises(x.Refused):x.consume_start_authorization(x.validate_start_authorization(process_exists=lambda pid:False))
