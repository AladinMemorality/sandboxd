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
        with mock.patch.object(life,'STOP_COORDINATOR_IMPLEMENTED',False), self.assertRaises(life.Blocked):
            life.host_preflight({"version":1,"reviewed":True,"drain_integration_reviewed":True})
    def test_implemented_coordinator_still_requires_reviewed_drained_empty_enrollment(self):
        self.assertTrue(life.STOP_COORDINATOR_IMPLEMENTED)
        status=mock.Mock();status.exists.return_value=False
        with mock.patch.object(life,'STATUS',status):
            for config,message in (
                ({},'reviewed lifecycle manifest required'),
                ({'version':1,'reviewed':True},'production drain integration is not installed'),
                ({'version':1,'reviewed':True,'drain_integration_reviewed':True},'initial boot needs explicit empty-worker review'),
            ):
                with self.subTest(config=config), self.assertRaisesRegex(life.Blocked,message):
                    life.host_preflight(config)
    def test_first_boot_flag_cannot_bypass_existing_unclean_generation(self):
        status=mock.Mock();status.exists.return_value=True
        config={'version':1,'reviewed':True,'drain_integration_reviewed':True,'first_boot_empty_reviewed':True}
        with mock.patch.object(life,'STATUS',status), mock.patch.object(life,'private_json',return_value={'state':'worker-lost'}):
            with self.assertRaisesRegex(life.Blocked,'unclean previous worker exit'):
                life.host_preflight(config)
    def test_stop_proof_requires_exact_qemu_generation_and_paused_set(self):
        identity={'qemu_pid':42,'qemu_start_time':'123'}
        good={'version':1,'verified':True,'qemu_pid':42,'qemu_start_time':'123','guest_states':{'fixture':'paused'},'provider_jobs':0}
        self.assertEqual(life.validate_stop_proof(identity,good),good)
        for key,value in (('qemu_start_time','124'),('qemu_pid',43),('provider_jobs',1),('guest_states',{'fixture':'running'}),('verified',False)):
            changed=dict(good);changed[key]=value
            with self.assertRaises(life.Blocked):life.validate_stop_proof(identity,changed)
    def test_unwired_production_coordinator_always_refuses(self):
        with mock.patch.object(life,'STOP_COORDINATOR_IMPLEMENTED',False), mock.patch.object(life.subprocess,'Popen') as child:
            with self.assertRaises(life.Blocked):life.prepare_stop({'qemu_pid':12345,'paused':True})
            child.assert_not_called()
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
        with mock.patch.object(life,'verify_registry'),mock.patch.object(life.socket,'gethostname',return_value='baarcha-cube-worker-01'),mock.patch.object(Path,'read_text',return_value='fixture-machine'),mock.patch.object(life,'run',return_value=b'fixture-uuid\n'),mock.patch.object(life,'digest',return_value='fixed'),mock.patch.object(life.os,'statvfs',return_value=mock.Mock(f_bavail=64*1024**3,f_frsize=1)):
            self.assertFalse(life.nested_preflight(value)['tenant_ready'])
            value['data_filesystem_uuid']='wrong'
            with self.assertRaises(life.Blocked):life.nested_preflight(value)
            value['data_filesystem_uuid']='fixture-uuid';value['binaries']['cubelet']['sha256']='unreviewed'
            with self.assertRaises(life.Blocked):life.nested_preflight(value)

if __name__=='__main__':unittest.main()

class CleanReceiptTests(unittest.TestCase):
    def test_failed_clean_receipt_persistence_prevents_clean_state(self):
        child=Child();child.code=None;rows=[]
        state=life.Supervisor(child,{'qemu_pid':child.pid},lambda _: {'verified':True,'qemu_pid':child.pid},lambda:None,rows.append,retain_clean=mock.Mock(side_effect=OSError('disk')))
        state.signal(signal.SIGTERM);state.tick();child.code=0
        self.assertFalse(state.tick());self.assertFalse(state.clean);self.assertEqual(state.state,'worker-lost')
    def test_clean_receipt_published_once_after_actual_exit(self):
        child=Child();child.code=None;save=mock.Mock();proof={'verified':True,'qemu_pid':child.pid}
        state=life.Supervisor(child,{'qemu_pid':child.pid},lambda _:proof,lambda:None,lambda _:None,retain_clean=save)
        state.signal(signal.SIGTERM);state.tick();save.assert_not_called();child.code=0;self.assertFalse(state.tick());save.assert_called_once_with(proof)

class RetainedStopTests(unittest.TestCase):
    def test_container_stop_uses_only_frozen_id_infinite_timeout_and_retains(self):
        identity='a'*64;calls=[];current={'id':identity,'name':'/cube-sandbox-mysql','running':True,'paused':False,'restarting':False,'oom':False,'exit':0}
        def run(argv,timeout=10):
            calls.append((argv,timeout))
            if argv[1]=='inspect':return json.dumps(current).encode()
            self.assertEqual(argv,['/usr/bin/docker','stop','--time=-1',identity]);self.assertIsNone(timeout);current['running']=False;return identity.encode()
        with mock.patch.object(life,'run',side_effect=run):self.assertEqual(life.retained_container_stop('cube-sandbox-mysql',identity),identity)
        self.assertEqual(sum(args[1]=='stop' for args,_ in calls),1)
        self.assertFalse(any('rm' in args or 'kill' in args for args,_ in calls))
    def test_replaced_container_or_oom_cannot_pass(self):
        identity='a'*64
        with mock.patch.object(life,'inspect_container_id',side_effect=life.Blocked('replacement')),mock.patch.object(life,'run') as run:
            with self.assertRaises(life.Blocked):life.retained_container_stop('cube-sandbox-mysql',identity)
            run.assert_not_called()
        with mock.patch.object(life,'inspect_container_id',return_value={'running':False,'paused':False,'restarting':False,'oom':True,'exit':137}):
            with self.assertRaises(life.Blocked):life.retained_container_stop('cube-sandbox-mysql',identity)
    def test_native_signal_is_pidfd_term_once_and_generation_checked(self):
        path=Path('/usr/local/services/Cubelet/bin/cubelet');expected={'pid':55,'start_time':'77','path':str(path)};poll=mock.Mock();poll.poll.return_value=[(99,1)]
        with mock.patch.object(life,'native_identity',return_value=(55,'77',path)),mock.patch.object(life,'process_start_time',return_value='77'),mock.patch.object(Path,'resolve',return_value=path),mock.patch.object(life,'unit_state',return_value={'MainPID':'55'}),mock.patch.object(life.os,'pidfd_open',return_value=99,create=True),mock.patch.object(life.signal,'pidfd_send_signal',create=True) as send,mock.patch.object(life.select,'poll',return_value=poll),mock.patch.object(life.os,'close') as close:
            life.retained_native_stop('cube-sandbox-cubelet.service',{},expected)
            send.assert_called_once_with(99,signal.SIGTERM);close.assert_called_once_with(99)
            send.reset_mock()
            with self.assertRaises(life.Blocked):life.retained_native_stop('cube-sandbox-cubelet.service',{},dict(expected,pid=56))
            send.assert_not_called()
    def test_stop_plan_never_overwrites_prior_attempt(self):
        with tempfile.TemporaryDirectory() as d,mock.patch.object(life,'STOP_PLAN',Path(d).resolve()/'plan.json'):
            life.publish_stop_plan({'version':1,'attempt':'old'})
            with self.assertRaises(FileExistsError):life.publish_stop_plan({'version':1,'attempt':'new'})
            self.assertEqual(json.loads(life.STOP_PLAN.read_text())['attempt'],'old')
    def test_contract_refuses_upstream_compose_and_force_timeout(self):
        def state(unit):
            name=life.component_name(unit) if unit.startswith('cube-') else ''
            return {'Id':unit,'LoadState':'loaded','ActiveState':'inactive','MainPID':'0','Restart':'no','SendSIGKILL':'no','TimeoutStopUSec':'infinity','KillMode':'process','KillSignal':'15', 'ExecStop': '' if unit=='docker.service' or name=='s3lvol' else '{ path=/usr/bin/python3 ; argv[]=/usr/bin/python3 '+life.SCRIPT+' stop-component --unit '+unit+' ; ignore_errors=no ; }'}
        with mock.patch.object(life,'unit_state',side_effect=state):life.graceful_contract({})
        for field,value in [('ExecStop','{ path=/bin/bash ; argv[]=/bin/bash upstream-compose-down ; }'),('TimeoutStopUSec','30s'),('SendSIGKILL','yes'),('Restart','on-failure')]:
            def changed(unit,field=field,value=value):
                row=state(unit)
                if unit=='cube-sandbox-mysql.service':row[field]=value
                return row
            with mock.patch.object(life,'unit_state',side_effect=changed):
                with self.assertRaises(life.Blocked):life.graceful_contract({})
    def test_failed_nested_shutdown_prevents_qmp_powerdown(self):
        child=Child();child.code=None;power=mock.Mock()
        def drain(_):raise life.Blocked('nested stop incomplete')
        state=life.Supervisor(child,{'qemu_pid':child.pid},drain,power,lambda _:None)
        state.signal(signal.SIGTERM);state.tick();self.assertEqual(state.state,'stop-blocked');power.assert_not_called();self.assertIsNone(child.code)
    def test_retained_shutdown_order_and_docker_last(self):
        from types import SimpleNamespace
        args=SimpleNamespace(machine_id='a'*32,boot_id='b'*32,data_uuid='c'*32)
        active={'cube-sandbox-cube-api.service','cube-sandbox-cubelet.service','cube-sandbox-mysql.service','docker.service','docker.socket'}
        rows={unit:{'ActiveState':'active' if unit in active else 'inactive','MainPID':'0'} for unit in ['cube-sandbox-'+x+'.service' for x in life.STOP_ORDER]+['docker.service','docker.socket']}
        rows['cube-sandbox-cube-api.service']['MainPID']='51';rows['cube-sandbox-cubelet.service']['MainPID']='52'
        containers={'cube-sandbox-mysql':'a'*64,'cube-production-registry':'b'*64};stops=[];saved=[];events=[]
        def run(argv,timeout=10):
            events.append(argv)
            if argv[0]=='/usr/bin/docker' and argv[1]=='ps':return ('\n'.join(containers)+'\n').encode()
            if argv[:3]==['/usr/bin/systemctl','stop','--no-block']:
                self.assertTrue(saved,'component stop before frozen plan')
                for unit in argv[3:]:rows[unit]={'ActiveState':'inactive','MainPID':'0'};stops.append(unit)
                return b''
            if argv[0]=='/usr/bin/sync':return b''
            self.fail('unexpected mutation '+str(argv))
        process=mock.Mock();process.poll.return_value=0;process.returncode=0
        native=lambda unit,config:(int(rows[unit]['MainPID']),'1',Path('/usr/local/services/bin/'+('cubelet' if 'cubelet' in unit else 'cube-api')))
        with mock.patch.object(life,'private_json',return_value={}),mock.patch.object(life,'verify_start'),mock.patch.object(life,'graceful_contract'),mock.patch.object(life,'unit_state',side_effect=lambda unit:rows[unit]),mock.patch.object(life,'run',side_effect=run),mock.patch.object(life,'docker_identity',side_effect=lambda name:{'id':containers[name]}),mock.patch.object(life,'native_identity',side_effect=native),mock.patch.object(life,'publish_stop_plan',side_effect=saved.append),mock.patch.object(life,'inspect_container_id',return_value={'running':False,'oom':False,'exit':0}),mock.patch.object(life,'no_guest_processes'),mock.patch.object(life.subprocess,'Popen',return_value=process):
            result=life.shutdown_components(args)
        self.assertTrue(result['stopped']);self.assertEqual(saved[0]['containers'],containers)
        self.assertEqual(stops,['cube-sandbox-cube-api.service','cube-sandbox-cubelet.service','cube-sandbox-mysql.service','docker.service','docker.socket'])
        self.assertEqual(events[-2:],[['/usr/bin/sync','-f','/data'],['/usr/bin/sync','-f','/']])
    @unittest.skipUnless(sys.platform=='linux' and hasattr(os,'pidfd_open') and hasattr(signal,'pidfd_send_signal'),'real Linux pidfd fixture')
    def test_real_pidfd_stops_only_owned_synthetic_child(self):
        child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(20)'])
        try:
            path=Path(f'/proc/{child.pid}/exe').resolve(strict=True);generation=life.process_start_time(child.pid)
            expected={'pid':child.pid,'start_time':generation,'path':str(path)}
            with mock.patch.object(life,'native_identity',return_value=(child.pid,generation,path)),mock.patch.object(life,'unit_state',return_value={'MainPID':str(child.pid)}):
                life.retained_native_stop('cube-sandbox-cubelet.service',{},expected)
            self.assertEqual(child.wait(timeout=2),-signal.SIGTERM)
        finally:
            if child.poll() is None:child.terminate();child.wait(timeout=2)
    def test_nested_shutdown_receipt_and_identity_gate_before_qmp(self):
        proof={'worker_machine_id':'a'*32,'worker_boot_id':'b'*32,'data_uuid':'c'*32}
        with mock.patch.object(life,'run',return_value=json.dumps({'stopped':True,'boot_id':'b'*32}).encode()) as run:
            life.shutdown_nested(proof)
            args=run.call_args.args[0]
            self.assertEqual(args[0],'/usr/bin/ssh');self.assertIn('shutdown-components',args[-1]);self.assertEqual(run.call_args.kwargs['timeout'],240)
            with self.assertRaises(life.Blocked):life.shutdown_nested(dict(proof,data_uuid='; arbitrary shell'))
            self.assertEqual(run.call_count,1)
        with mock.patch.object(life,'run',return_value=b'{"stopped":false}'):
            with self.assertRaises(life.Blocked):life.shutdown_nested(proof)

class RegistryTests(unittest.TestCase):
    def definition(self):
        return {'id':'a'*64,'name':'/cube-production-registry','image':'sha256:'+'b'*64,'restart':{'Name':'always','MaximumRetryCount':0},'ports':life.REGISTRY_PORTS,'mounts':[{'Type':'bind','Source':'/data/registry','Destination':'/var/lib/registry','RW':True}]}
    def test_registry_definition_requires_daemon_restart_and_private_port(self):
        with mock.patch.object(life,'real_path',return_value=mock.Mock(is_dir=lambda:True,stat=lambda:mock.Mock(st_dev=1))),mock.patch.object(Path,'stat',return_value=mock.Mock(st_dev=1)):
            life.validate_registry_definition(self.definition())
            for key,value in [('restart',{'Name':'unless-stopped','MaximumRetryCount':0}),('ports',{}),('mounts',[]),('image','registry:latest'),('id','short')]:
                changed=self.definition();changed[key]=value
                with self.assertRaises(life.Blocked):life.validate_registry_definition(changed)
    def test_readiness_checks_exact_retained_container_and_http(self):
        value=self.definition()
        with mock.patch.object(life,'validate_registry_definition'),mock.patch.object(life,'registry_definition',return_value=value),mock.patch.object(life,'inspect_container_id',return_value={'running':True,'oom':False}) as state,mock.patch.object(life,'registry_health') as health:
            life.verify_registry({'registry':value},True)
            state.assert_called_once_with('a'*64,'cube-production-registry');health.assert_called_once()
            state.return_value={'running':False,'oom':False}
            with self.assertRaises(life.Blocked):life.verify_registry({'registry':value},True)
    def test_registry_replacement_refused_before_health(self):
        value=self.definition();replacement=dict(value,id='c'*64)
        with mock.patch.object(life,'validate_registry_definition'),mock.patch.object(life,'registry_definition',return_value=replacement),mock.patch.object(life,'registry_health') as health:
            with self.assertRaises(life.Blocked):life.verify_registry({'registry':value},True)
            health.assert_not_called()
    def test_health_has_fixed_target_and_rejects_redirect_or_wrong_service(self):
        for status,header,body,success in [(200,'registry/2.0',b'{}',True),(302,'registry/2.0',b'{}',False),(200,None,b'{}',False),(200,'registry/2.0',b'[]',False)]:
            connection=mock.Mock();response=connection.getresponse.return_value;response.status=status;response.getheader.return_value=header;response.read.return_value=body
            with mock.patch.object(life.http.client,'HTTPConnection',return_value=connection) as factory:
                if success:life.registry_health()
                else:
                    with self.assertRaises(life.Blocked):life.registry_health()
                factory.assert_called_once_with('127.0.0.1',5000,timeout=5);connection.request.assert_called_once_with('GET','/v2/');connection.close.assert_called_once()

class BootOrderingTests(unittest.TestCase):
    def test_registry_inspection_is_ordered_after_docker_without_dependency_cycle(self):
        base=Path(__file__).parent
        preflight=(base/'baarcha-cube-preflight.service').read_text()
        self.assertIn('Requires=docker.service',preflight)
        self.assertIn('After=data.mount docker.service',preflight)
        # Only Cube component services receive the preflight dependency.
        import importlib.util
        spec=importlib.util.spec_from_file_location('generator_ordering',base/'render_nested.py')
        generator=importlib.util.module_from_spec(spec);spec.loader.exec_module(generator)
        docker=generator.overrides()['/etc/systemd/system/docker.service.d/99-baarcha-retained-stop.conf']
        self.assertNotIn('baarcha-cube-preflight',docker)

class ExternalStartupTests(unittest.TestCase):
    def test_consumption_precedes_new_qemu_and_failure_never_launches(self):
        import contextlib
        for refusal in (False,True):
            with self.subTest(refusal=refusal):
                calls=[];verifier=mock.Mock()
                def consume(value):
                    calls.append('consume')
                    if refusal:raise life.Blocked('authorization changed')
                verifier.consume_start_authorization.side_effect=consume
                child=mock.Mock(pid=123);state=mock.Mock(clean=True);state.tick.return_value=False
                def launch(*a,**kw):calls.append('launch');return child
                with mock.patch.object(life.sys,'platform','linux'),mock.patch.object(life.os,'geteuid',return_value=0),mock.patch.object(life,'private_json',return_value={}),mock.patch.object(life.signal,'signal'),mock.patch.object(life,'lifetime_lock',side_effect=lambda *a:contextlib.nullcontext(10)),mock.patch.object(life,'host_preflight',return_value=(verifier,{'token':'pinned'})),mock.patch.object(life.subprocess,'Popen',side_effect=launch),mock.patch.object(life,'process_start_time',return_value='123'),mock.patch.object(life,'Supervisor',return_value=state):
                    if refusal:
                        with self.assertRaises(life.Blocked):life.supervise(life.ROOT/'backup.lock')
                        self.assertEqual(calls,['consume'])
                    else:
                        self.assertEqual(life.supervise(life.ROOT/'backup.lock'),0);self.assertEqual(calls,['consume','launch'])
    def test_failed_final_host_preflight_does_not_consume_or_launch(self):
        import contextlib
        with mock.patch.object(life.sys,'platform','linux'),mock.patch.object(life.os,'geteuid',return_value=0),mock.patch.object(life,'private_json',return_value={}),mock.patch.object(life.signal,'signal'),mock.patch.object(life,'lifetime_lock',side_effect=lambda *a:contextlib.nullcontext(10)),mock.patch.object(life,'host_preflight',side_effect=life.Blocked('disk identity changed')),mock.patch.object(life.subprocess,'Popen') as launch:
            with self.assertRaises(life.Blocked):life.supervise(life.ROOT/'backup.lock')
            launch.assert_not_called()
