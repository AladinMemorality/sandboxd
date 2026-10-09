"""Check remaining old checkpoints after a fresh fleet source backup; no model calls."""
import argparse,importlib.util,json,os,pathlib,sqlite3,subprocess,time,hashlib
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'fleet-wake-validation-01'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
import sys;sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def rows(query,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(query,args)]
def tasks(sid):return rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))
def bindings():return {r['sandbox_id']:r['runtime_id'] for r in rows('select sandbox_id,runtime_id from runtime_binding')}
parser=argparse.ArgumentParser();parser.add_argument('--resume',action='store_true');parser.add_argument('--max-new',type=int,default=0);args=parser.parse_args();assert args.max_new>=0
resume=args.resume
with b.locked():
 native=json.loads((root/'full-pause-release-20261009/deployed.json').read_text());assert native['deployed'] and native['full_pause_snapshot_policy']
 actual=subprocess.check_output(ssh+['sha256sum /usr/local/services/cubetoolbox/Cubelet/bin/cubelet'],timeout=30).decode().split()[0];assert actual==native['cubelet_sha256']
 expected=json.loads((root/'supervisor-canary-2c7e700/passed.json').read_text())['receipt']['sha256']
 assert not rows("select task_id from task where status in ('running','queued')")
 assert not rows("select id from cube_relocation where phase='fenced'") and not rows("select admission_key from cube_admission where state='pending'")
 assert not (root/'restore-barrier.json').exists()
 assert json.loads(subprocess.check_output(['/usr/local/libexec/baarcha-cube-worker-start','--observe'],timeout=60))['consistent']
 if resume:assert out.is_dir() and not (out/'complete.json').exists()
 else:out.mkdir(mode=0o700)
 scope=rows("select s.id,s.status,s.web_port,b.runtime_id,a.worker_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id order by s.id")
 assert len(scope)>=135 and all(r['worker_id']=='vps' and (r['status'],r['state'],r['charged']) in [('stopped','released',0),('running','active',1)] for r in scope)
 initial=bindings();attempt=out/('attempt-'+str(time.time_ns()));attempt.mkdir(mode=0o700);b.atomic(attempt/'scope.json',b.encoded(scope))
 backup_root=P('/var/backups/baarcha-vps-source');backup=json.loads((backup_root/'latest.json').read_text());assert backup['verified'] and time.time()-backup['completed_at']<6*3600
 saved=json.loads((backup_root/backup['generation']/'scope.json').read_text());backup_map={r['sandbox_id']:r['runtime_id'] for r in saved['bindings']}
 assert set(initial)<=set(backup_map),'New apps need a source backup before lifecycle validation'
 # Only successful checkpoints produced after the native full-pause release
 # can replace another wake test. Old supervisor receipts cannot do that.
 proven={}
 receipts=list((root/'recovery-moves').glob('*/worker-job.PRIVATE.json'))+list(root.glob('exited-recovery-*/worker-job.PRIVATE.json'))
 jobs=[json.loads(p.read_text()) for p in receipts]
 for density in root.glob('real-preview-density-50-balanced-*'):
  if not (density/'result.json').exists() or not (density/'cleanup.json').exists():continue
  report=json.loads((density/'result.json').read_text());cleanup=json.loads((density/'cleanup.json').read_text())
  if not report.get('passed') or not cleanup.get('complete'):continue
  fifty=json.loads((density/'fifty-running.json').read_text())
  if fifty['at']<=native['at']:continue
  for sid in [r['sandbox_id'] for r in cleanup['runtimes'] if r.get('stopped')]:
   for job in jobs:
    if job['sandbox_id']==sid and job['runtime_id']==initial.get(sid) and job['runtime_id'] in fifty['runtime_ids'] and job['source']['task_ids']==[t['task_id'] for t in tasks(sid)]:
     update=json.loads((density/(sid+'-supervisor.json')).read_text());assert update['runtime_id']==job['runtime_id'] and update['sha256']==expected
     proven[sid]='density-full-snapshot'
 for receipt in root.glob('exited-recovery-*/wake-check.json'):
  proof=json.loads(receipt.read_text());sid=proof['sandbox_id']
  if not proof.get('passed') or proof['at']<=native['at'] or proof['runtime_id']!=initial.get(sid):continue
  job=json.loads((receipt.parent/'worker-job.PRIVATE.json').read_text())
  if job['source']['task_ids']==[t['task_id'] for t in tasks(sid)]:
   update=json.loads((receipt.parent/'supervisor-update.json').read_text());assert update['runtime_id']==job['runtime_id'] and update['sha256']==expected
   proven[sid]='source-recovery-full-wakes'
 results=[];new_checks=0
 for row in scope:
  sid=row['id'];runtime=row['runtime_id'];history=tasks(sid)
  case=out/(sid+'-'+runtime)
  if case.exists():
   assert resume and (case/'passed.json').exists(),'Unresolved previous probe: inspect its journal before any retry'
   previous=json.loads((case/'passed.json').read_text());assert previous['tasks']==history,'New user work needs a new reviewed probe generation'
   results.append(previous['result']);continue
  if sid in proven:
   results.append({'sandbox_id':sid,'runtime_id':runtime,'verification':proven[sid],'original_running_preserved':True,'supervisor_sha256':expected});continue
  if args.max_new and new_checks>=args.max_new:
   b.atomic(attempt/'bounded-stop.json',b.encoded({'checked':len(results),'new_checks':new_checks,'total':len(scope),'complete':False,'reason':'bounded batch completed','at':time.time()}));print(json.dumps({'bounded_stop':True,'checked':len(results),'new_checks':new_checks}),flush=True);sys.exit(0)
  disk=json.loads(subprocess.check_output(ssh+['python3 -c '+__import__('shlex').quote("import os,json;s=os.statvfs('/data');print(json.dumps({'used':s.f_blocks-s.f_bfree,'total':s.f_blocks}))")],timeout=30))
  if disk['used']/disk['total']>=0.635:
   b.atomic(attempt/'bounded-stop.json',b.encoded({'checked':len(results),'new_checks':new_checks,'total':len(scope),'complete':False,'reason':'storage compaction required','at':time.time()}));print(json.dumps({'storage_stop':True,'checked':len(results),'new_checks':new_checks}),flush=True);sys.exit(0)
  assert backup_map[sid]==runtime,'Unverified replacement needs a matching backup or completed source-recovery wake proof'
  assert not rows("select task_id from task where status in ('running','queued')"),'User work started; stop verification'
  assert bindings()==initial,'Placement changed; preserve new user work'
  case.mkdir(mode=0o700);b.atomic(case/'before.json',b.encoded(row));own_activity=None;started=False;success=False
  with account_maintenance([sid],case):
   def api(action):
    code,_=copy.api('POST','/v1/sandboxes/'+sid+'/'+action);assert code==200
   try:
    if row['status']=='stopped':
     b.atomic(case/'start-intent.json',b.encoded({'runtime_id':runtime,'at':time.time()}));started=True;api('start')
     own_activity=rows('select last_active_at from sandbox where id=?',(sid,))[0]['last_active_at']
    origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
    worker=transport.Worker({'worker':'vps','id':'fleet-wake-'+sid,'sandbox_id':sid,'runtime_id':runtime,'headers':headers,'web_port':row['web_port'] or 3000})
    def healthy():
     memory_before=assets.guest_memory(runtime)
     status=worker.control('GET','/status');assert not status['active_task'] and all(p['running'] for p in status['processes'])
     web={**headers,'Host':headers['Host'].replace('3031-',str(row['web_port'] or 3000)+'-',1)}
     base,html=assets.page(worker.origin,web,timeout=15);static=base.rsplit('/',1)[0]+'/';queue=assets.entries(html,base,static);seen=set();total=0
     while queue:
      path=queue.pop(0)
      if path in seen:continue
      seen.add(path);assert len(seen)<=512
      data=worker.http('GET',path,headers=web,timeout=45);total+=len(data);assert total<=64*1024**2
      queue.extend(p for p in assets.imports(path,data,static) if p not in seen)
     after=worker.control('GET','/status');assert not after['active_task'] and all(p['running'] for p in after['processes'])
     assert {p['name']:p['restarts'] for p in status['processes']}=={p['name']:p['restarts'] for p in after['processes']}
     memory=assets.guest_memory(runtime);assert memory['oom_kill']==memory_before['oom_kill'],'New guest OOM during preview validation'
     return {'modules':len(seen),'bytes':total,'guest_memory':memory,'new_oom_kills':memory['oom_kill']-memory_before['oom_kill']}
    update=subprocess.run(ssh+['python3','/opt/baarcha-vps-export-recovery-2c7e700/worker.py','--container',runtime],capture_output=True,timeout=460)
    b.atomic(case/'supervisor.PRIVATE.log',update.stdout+update.stderr);assert update.returncode==0
    receipt=[json.loads(line) for line in update.stdout.splitlines()];assert len(receipt)==1 and receipt[0]['status'] in ('updated','current') and receipt[0]['sha256']==expected
    first=healthy();wake=None
    if row['status']=='stopped':
     api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began
     own_activity=rows('select last_active_at from sandbox where id=?',(sid,))[0]['last_active_at'];again=healthy()
     assert again['guest_memory']['oom_kill']==first['guest_memory']['oom_kill'],'Guest OOM counter changed during full-snapshot wake'
    success=True
   except BaseException as error:
    b.atomic(case/'failed.json',b.encoded({'type':type(error).__name__,'reason':str(error)[:250],'at':time.time()}));raise
   finally:
    unchanged=tasks(sid)==history
    activity=rows('select last_active_at from sandbox where id=?',(sid,))[0]['last_active_at']
    if started and unchanged and (own_activity is None or activity<=own_activity):
     api('stop')
     assert rows('select s.status,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,))==[{'status':'stopped','state':'released','charged':0}]
    assert unchanged,'User task changed; runtime preserved for review'
  assert success
  result={'sandbox_id':sid,'runtime_id':runtime,'verification':'full-wake-and-modules' if started else 'existing-live-guest-and-modules','wake_seconds':wake,'original_running_preserved':True,'supervisor_sha256':expected,**first}
  b.atomic(case/'passed.json',b.encoded({'tasks':history,'result':result}));results.append(result);new_checks+=1
  b.atomic(attempt/'progress.json',b.encoded({'checked':len(results),'total':len(scope),'at':time.time()}));print(json.dumps({'checked':len(results),'total':len(scope)}),flush=True)
 assert bindings()==initial and not rows("select task_id from task where status in ('running','queued')")
 b.atomic(out/'complete.json',b.encoded({'complete':True,'results':results,'count':len(results),'native_sha256':native['cubelet_sha256'],'model_calls':False,'b200_contacted':False,'at':time.time()}))
