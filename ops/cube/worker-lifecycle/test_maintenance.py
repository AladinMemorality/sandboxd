import copy
import importlib.util
from pathlib import Path
import unittest

spec=importlib.util.spec_from_file_location('maintenance',Path(__file__).with_name('maintenance.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)


def plan():
    return {'version':1,'kind':'current-generation-external-recovery','expected':{'controller_id':'1'*64,'controller_image':'sha256:'+'2'*64,'outer_machine_id':'a'*32,'worker_machine_id':'b'*32,'outer_boot_id':'11111111-1111-1111-1111-111111111111','worker_boot_id':'22222222-2222-2222-2222-222222222222','data_uuid':'33333333-3333-3333-3333-333333333333','qemu_pid':43,'qemu_start_time':'101','supervisor_pid':42,'supervisor_start_time':'100'},'files':{str(p):'a'*64 for p in m.FILES},'bindings':[{'sandbox_id':'stable','app_id':'app','runtime_id':'provider','template_id':'reviewed','config_revision':8}], 'routing':{'server':'srv0','online_sha256':'0'*64,'platform_hosts':['baarcha.tn','www.baarcha.tn'],'preview_hosts':['*.preview.example','*.preview.other'],'preview_probe_hosts':['s-stable-3000.preview.example'],'motion_hosts':['S-MOTION-3000.legacy.example'],'unaffected_hosts':['bp.tn','www.bp.tn','hh1.dovisual.com']},'motion':{'proxy_id':'3'*64,'proxy_image':'sha256:'+'4'*64,'worker_pid':100,'worker_start_time':'123'},'provider_terminal_counts':{t:{} for t in m.TABLES}}

class MaintenanceTests(unittest.TestCase):
    def test_explicit_nonempty_schema_refuses_identity_and_pending_drift(self):
        p=plan();m.validate_plan(p)
        for kind in ('empty','duplicate','pending','tablemissing','samepid','unknown','previewescape','unaffectedoverlap','backup'):
            q=copy.deepcopy(p)
            if kind=='empty':q['bindings']=[]
            elif kind=='duplicate':q['bindings']*=2
            elif kind=='pending':q['provider_terminal_counts']['t_cube_pause_snapshot']={'PAUSING':1}
            elif kind=='tablemissing':q['provider_terminal_counts'].pop('t_cube_snapshot')
            elif kind=='samepid':q['expected']['supervisor_pid']=q['expected']['qemu_pid']
            elif kind=='unknown':q['anything']='extra'
            elif kind=='previewescape':q['routing']['preview_probe_hosts']=['outside.example']
            elif kind=='backup':q['backup']={}
            else:q['routing']['unaffected_hosts']=['foo.preview.example']
            with self.subTest(kind=kind),self.assertRaises(m.b.Refused):m.validate_plan(q)
    def test_prepend_fence_before_exact_motion_alias_preserves_other_servers(self):
        online={'apps':{'http':{'servers':{'srv0':{'routes':[{'@id':'motion-exact','match':[{'host':['S-MOTION-3000.legacy.example']}],'handle':[{'handler':'reverse_proxy','upstreams':[{'dial':'motion'}]}]},{'match':[{'host':['bp.tn','hh1.dovisual.com']}],'handle':[{'handler':'reverse_proxy'}]}]},'srv1':{'routes':[{'http_only':'unchanged'}]}}}}}
        scope=plan()['routing'];scope['online_sha256']=m.b.sha(m.json.dumps(online,sort_keys=True,separators=(',',':')).encode());variants=m.routing_variants(online,scope)
        self.assertEqual(variants['online'],online)
        for mode in ('drain','offline'):
            routes=variants[mode]['apps']['http']['servers']['srv0']['routes'];self.assertEqual(routes[3:],online['apps']['http']['servers']['srv0']['routes'])
            self.assertEqual(variants[mode]['apps']['http']['servers']['srv1'],online['apps']['http']['servers']['srv1'])
            self.assertEqual(routes[0]['match'][0]['host'],scope['motion_hosts']);self.assertEqual(routes[0]['handle'][0]['status_code'],503)
            for unaffected in scope['unaffected_hosts']:
                self.assertFalse(any(any(m.host_matches(h,unaffected) for h in r['match'][0]['host']) for r in routes[:3]))
        self.assertNotIn('/api/bridge',variants['drain']['apps']['http']['servers']['srv0']['routes'][2]['match'][0]['path'])
        self.assertIn('/api/bridge',variants['offline']['apps']['http']['servers']['srv0']['routes'][2]['match'][0]['path'])
        for routes in variants.values():
            for rule in routes['apps']['http']['servers']['srv0']['routes'][:3]:
                for matcher in rule.get('match',[]):self.assertNotIn('/api/v1/messages',matcher.get('path',[]))
        self.assertEqual(online['apps']['http']['servers']['srv0']['routes'][0]['@id'],'motion-exact')
    def test_real_native_inventory_projection_does_not_drop_or_add_binding(self):
        wanted=plan()['bindings'];actual=[{v:wanted[0][k] for k,v in m.BINDING_MAP.items()}]
        actual[0].update(ConfigSHA256='x',OwnerSHA256='y',Domain='cube.app')
        m.verify_bindings(actual,wanted)
        for mutation in ([dict(actual[0],RuntimeID='other')],actual*2,[]):
            with self.assertRaises(m.b.Refused):m.verify_bindings(mutation,wanted)
    def test_motion_queue_is_bounded_terminal_only_not_full_library_quiescence(self):
        body={'projects':[{'id':'p','jobs':[{'status':s} for s in ('ready','failed','cancelled','interrupted')]}]}
        value=m.motion_job_summary(body);self.assertEqual(value['active_jobs'],0);self.assertEqual(value['terminal_jobs'],4)
        for bad in ('queued','running','pending',None,'unknown'):
            changed=copy.deepcopy(body);changed['projects'][0]['jobs'][0]['status']=bad
            with self.subTest(status=bad),self.assertRaises(m.b.Refused):m.motion_job_summary(changed)
        with self.assertRaises(m.b.Refused):m.motion_job_summary({'projects':[{'id':'missing-jobs'}]})
    def test_failure_never_retries_power_or_reopens(self):
        class Host:
            def __init__(self,fail):self.fail=fail;self.calls=[]
            def __getattr__(self,name):
                def call(*args):
                    self.calls.append(name)
                    if name==self.fail:raise m.b.Refused('injected '+name)
                    return {'tenant_ready':True,'routing_changed':False}
                return call
        for failure in ('drain','capture_before','pause','retained_stop','external_stop','authorize_start','start_worker','transition','ready_fence'):
            with self.subTest(failure=failure):
                host=Host(failure);events=[];sequence=m.Sequence(host,lambda phase,value=None:events.append(phase))
                with self.assertRaises(m.b.Refused):receipt=sequence.stop();sequence.start(receipt)
                self.assertEqual(host.calls.count('external_stop'),1 if failure in ('external_stop','authorize_start','start_worker','transition','ready_fence') else 0)
                self.assertNotIn('reopen',host.calls);self.assertNotIn('complete',events)
        host=Host(None);events=[];sequence=m.Sequence(host,lambda phase,value=None:events.append(phase));sequence.start(sequence.stop())
        self.assertLess(host.calls.index('prepare_stop_config'),host.calls.index('capture_before'));self.assertLess(host.calls.index('capture_before'),host.calls.index('pause'));self.assertLess(host.calls.index('transition'),host.calls.index('reopen'));self.assertEqual(events[-1],'complete')

if __name__=='__main__':unittest.main()

class RealEntryPointTests(unittest.TestCase):
    def test_real_host_constructor_loads_fixed_helpers_from_string_paths(self):
        import tempfile
        from unittest import mock
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);drain=root/'drain.py';life=root/'life.py';drain.write_text('fixture_kind="drain"\n');life.write_text('fixture_kind="life"\n')
            original=m.b.load_module
            def redirected(path):
                fixed={"/usr/local/libexec/baarcha-cube-drain-observe.py":drain,str(m.b.LIFECYCLE):life}
                self.assertIn(str(path),fixed)
                return original(str(fixed[str(path)]))
            with mock.patch.object(m.b,'load_module',side_effect=redirected):host=m.Host(plan(),root,[10,11,12,13],lambda *a:None)
            self.assertEqual(host.observer.fixture_kind,'drain');self.assertEqual(host.life.fixture_kind,'life');self.assertIsNone(host.nested_context)

    def test_already_stopped_motion_proxy_is_not_stopped_or_started(self):
        from unittest import mock
        host=m.Host.__new__(m.Host);host.plan=plan();host.baseline={'motion_running':False};host.command=mock.Mock()
        row={'Id':'3'*64,'Image':'sha256:'+'4'*64,'State':{'Running':False},'HostConfig':{'RestartPolicy':{'Name':'no'}}};host.inspect=lambda ident:row
        host.stop_motion_proxy();host.command.assert_not_called()
        row['State']['Running']=True
        with self.assertRaises(m.b.Refused):host.stop_motion_proxy()
        host.command.assert_not_called()
    def test_busy_customer_task_is_deferred_without_cancellation(self):
        from unittest import mock
        host=m.Host.__new__(m.Host);host.observer=mock.Mock();host.observer.sqlite_observation.return_value={'active_tasks':1};host.pg_counts=lambda:{'thumbnail':0,'env_pending':0}
        self.assertFalse(host.quiet_tasks(permit_busy=True)['quiet'])
        with self.assertRaises(m.b.Refused):host.quiet_tasks()
        host.observer.sqlite_observation.return_value={'active_tasks':0}
        self.assertTrue(host.quiet_tasks()['quiet'])
    @unittest.skipUnless(__import__('os').geteuid()==0 and __import__('sys').platform=='linux','real root-only CLI journal')
    def test_check_main_persists_real_atomic_event_and_releases_without_mutation(self):
        import tempfile,contextlib,io,json,sys
        from unittest import mock
        with tempfile.TemporaryDirectory(prefix='cube-maintenance-check-',dir='/root') as tmp:
            root=Path(tmp);(root/'maintenance').mkdir(mode=0o700);config=root/'plan.json';m.x.publish(config,plan());job=root/'maintenance/check-01'
            host=mock.Mock();host.preflight.return_value={'version':1,'ready':False,'deferred':True,'work':{'runtime':{'active_tasks':1}},'bindings':1};host.nested_context=mock.MagicMock();closed=[]
            @contextlib.contextmanager
            def locks():
                try:yield [10,11,12,13]
                finally:closed.append(True)
            output=io.StringIO()
            with mock.patch.object(m,'ROOT',root),mock.patch.object(m,'Host',return_value=host),mock.patch.object(m.b,'locked',locks),mock.patch.object(sys,'argv',['maintenance','--plan',str(config),'--directory',str(job),'--check']),contextlib.redirect_stdout(output):m.main()
            result=json.loads(output.getvalue());self.assertFalse(result['ready']);self.assertTrue(result['deferred']);self.assertFalse(result['lifecycle_mutations']);self.assertEqual(closed,[True])
            current=json.loads((job/'current.json').read_bytes());self.assertEqual(current['phase'],'read-only-preflight-deferred')
            events=list(job.glob('0*.json'));self.assertEqual(len(events),1);self.assertEqual(json.loads(events[0].read_bytes())['value']['work']['runtime']['active_tasks'],1)
            host.preflight.assert_called_once_with(defer_busy=True);host.drain.assert_not_called();host.pause.assert_not_called();host.start_worker.assert_not_called();host.nested_context.__exit__.assert_called_once()

@unittest.skipUnless(__import__('os').geteuid()==0 and __import__('sys').platform=='linux','native ownership namespace fixture')
class MotionFingerprintTests(unittest.TestCase):
    def test_exact_service_owned_source_layout_and_generic_root_boundary(self):
        import tempfile,os,hashlib
        from unittest import mock
        with tempfile.TemporaryDirectory(prefix='motion-review-',dir='/root') as temp:
            root=Path(temp);app=root/'app';server=app/'server';server.mkdir(parents=True)
            os.chown(app,985,985);os.chown(server,985,985);app.chmod(0o755);server.chmod(0o755)
            source=server/'index.mjs';source.write_bytes(b'// reviewed source\n');source.chmod(0o644);os.chown(source,985,985)
            with mock.patch.object(m,'MOTION_SOURCE_ROOT',server):
                self.assertEqual(m.file_digest(source),hashlib.sha256(source.read_bytes()).hexdigest())
                for path in (source,server,app):
                    os.chown(path,986,986)
                    with self.subTest(owner=str(path)),self.assertRaises(m.b.Refused):m.file_digest(source)
                    os.chown(path,985,985)
                    mode=path.stat().st_mode&0o777;path.chmod(mode|0o020)
                    with self.subTest(mode=str(path)),self.assertRaises(m.b.Refused):m.file_digest(source)
                    path.chmod(mode)
                extra=server/'unreviewed.mjs';extra.write_bytes(b'not an allowlisted input');os.chown(extra,985,985)
                with self.assertRaises(m.b.Refused):m.file_digest(extra)
                target=server/'jobs.mjs';target.symlink_to(source)
                with self.assertRaises(OSError):m.file_digest(target)
                source.write_bytes(b'x'*(m.MOTION_SOURCE_LIMIT+1))
                with self.assertRaises(m.b.Refused):m.file_digest(source)
    def test_intermediate_symlink_and_concurrent_file_replacement_refused(self):
        import tempfile,os
        from unittest import mock
        with tempfile.TemporaryDirectory(prefix='motion-review-',dir='/root') as temp:
            root=Path(temp);app=root/'app';server=app/'server';server.mkdir(parents=True);source=server/'index.mjs';source.write_bytes(b'first')
            with mock.patch.object(m,'MOTION_SOURCE_ROOT',server):
                original=os.read;changed=[False]
                def race(fd,size):
                    raw=original(fd,size)
                    if raw and not changed[0]:
                        changed[0]=True;replacement=server/'replacement';replacement.write_bytes(b'other');os.replace(replacement,source)
                    return raw
                with mock.patch.object(m.os,'read',side_effect=race),self.assertRaises(m.b.Refused):m.file_digest(source)
                moved=root/'moved';server.rename(moved);server.symlink_to(moved,target_is_directory=True)
                with self.assertRaises(OSError):m.file_digest(source)
