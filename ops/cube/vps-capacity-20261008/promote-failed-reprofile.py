"""Reconcile the first proven 768 MiB OOM before general promotion was installed."""
import importlib.util,json,pathlib,subprocess,sys,time
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('copy','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
sid='01M1HH34KHGPFSPKT3ATC87E93';job=root/'recovery-moves'/('vps-reprofile-'+sid.lower()+'-768-01')
with b.locked():
 assert json.loads((root/'restore-barrier.json').read_text())['owner']=='parallel-transition'
 assert copy.rows("select id from cube_relocation where phase='fenced'")==[{'id':job.name}]
 assert not copy.rows("select task_id from task where status in ('running','queued')")
 request=json.loads((job/'worker-job.PRIVATE.json').read_text());worker=transport.Worker(request)
 assert (worker.root/'content-verified.json').exists()
 memory=assets.guest_memory(request['runtime_id']);assert memory['oom_kill']>0
 assert not (job/'promotion.json').exists()
 b.atomic(job/'failed-guest-memory.json',b.encoded(memory))
 cmd={'Action':'discard-target','ID':job.name,'Directory':str(job),'Migrations':str(root/'queue-release-d463b2d/source/control-plane/migrations')}
 result=json.loads(subprocess.check_output([str(root/'cube-relocate-reprofile')],input=json.dumps(cmd).encode(),timeout=200));assert result['relocation_aborted'] and result['source_retained']
 assert copy.api('POST','/v1/sandboxes/'+sid+'/start')[0]==200
 origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
 source=transport.Worker({'worker':'vps','id':job.name+'-resume-source','sandbox_id':sid,'runtime_id':request['source']['runtime_id'],'headers':headers})
 source.control('POST','/workspace/resume');assert copy.api('POST','/v1/sandboxes/'+sid+'/stop')[0]==200
 b.atomic(job/'promotion.json',b.encoded({'from':'balanced','to':'standard','proven_oom_kills':memory['oom_kill'],'unused_target_discarded':True,'source_preserved':True,'at':time.time()}))
 gate=json.loads((root/'restore-barrier.json').read_text());gate['allowed_sandboxes']=[sid];b.atomic(root/'restore-barrier.json',b.encoded(gate))
 print(json.dumps({'promote_to_mb':1024,'old_source_preserved':True,'unused_target_discarded':True}))
