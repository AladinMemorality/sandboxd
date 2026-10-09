"""Run two independent VPS migrations under continuously inherited operator locks.

Stop scheduling on a failure; let already-started migrations reach a safe result.
The storage/memory admission transactions still enforce the full resource budget.
"""
import contextlib,importlib.util,json,os,pathlib,signal,sqlite3,subprocess,sys,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
kind,name=sys.argv[1:];assert kind in ('reprofile','restore') and name.isalnum()
out=root/('parallel-'+kind+'-'+name);barrier=root/'restore-barrier.json'
def rows(sql,args=()):
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:return db.execute(sql,args).fetchall()
with b.locked() as locks:
 assert not rows("select id from cube_relocation where phase='fenced'")
 assert not rows("select task_id from task where status in ('running','queued')")
 if barrier.exists():assert json.loads(barrier.read_text())['owner']=='parallel-transition'
 if kind=='reprofile':
  selected=[{'sandbox_id':r[0],'profile':'balanced'} for r in rows("select b.sandbox_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id join sandbox s on s.id=b.sandbox_id where a.worker_id='vps' and b.template_id='tpl-78e4edb3d629465e9d8372c1' and s.status='stopped' and a.state='released' and a.charged=0 order by b.sandbox_id")]
 else:
  prepared=root/('vite-batch-'+name);assert json.loads((prepared/'prepared.json').read_text())['prepared']
  selected=json.loads((prepared/'scope.json').read_text())['selected']
 assert selected and len({x['sandbox_id'] for x in selected})==len(selected)
 for item in selected:
  sid=item['sandbox_id'];assert len(sid)==26 and sid.isalnum()
  item['journal']=('vps-reprofile-'+sid.lower()+'-768-01') if kind=='reprofile' else ('vps-restore-'+sid.lower())
  item['journals']=['vps-reprofile-'+sid.lower()+'-'+str(memory)+'-01' for memory in [768,1024,2048]] if kind=='reprofile' else [item['journal'],item['journal']+'-memory1024',item['journal']+'-memory2048']
  assert all(not (root/'recovery-moves'/j).exists() for j in item['journals'])
 out.mkdir(mode=0o700)
 scope={'parent_pid':os.getpid(),'concurrency':2,'kind':kind,'selected':selected,'model_calls':False,'b200_contacted':False}
 b.atomic(out/'scope.json',b.encoded(scope))
 gate={'purpose':'reviewed-restore-barrier','allowed_sandboxes':[x['sandbox_id'] for x in selected],'owner':out.name};b.atomic(barrier,b.encoded(gate))
 env={**os.environ,'BAARCHA_VPS_BATCH_SCOPE':str(out/'scope.json'),'BAARCHA_VPS_BATCH_LOCK_FDS':json.dumps(locks)}
 pending=list(selected);active=[];results=[];halt=False
 def interrupted(*args):
  global halt
  halt=True
 signal.signal(signal.SIGTERM,interrupted);signal.signal(signal.SIGINT,interrupted)
 while pending or active:
  if rows("select task_id from task where status in ('running','queued')"):halt=True
  while pending and len(active)<2 and not halt:
   item=pending.pop(0);sid=item['sandbox_id'];log=(out/(sid+'.PRIVATE.log')).open('wb')
   args=['/usr/bin/python3',str(root/('reprofile-vps.py' if kind=='reprofile' else 'restore-from-backup.py')),sid,'01' if kind=='reprofile' else item['profile']]
   process=subprocess.Popen(args,stdout=log,stderr=subprocess.STDOUT,env=env,pass_fds=locks)
   active.append((item,process,log));b.atomic(out/'active.json',b.encoded([{'sandbox_id':i['sandbox_id'],'pid':p.pid} for i,p,_ in active]))
  for item,process,log in list(active):
   code=process.poll()
   if code is None:continue
   log.close();active.remove((item,process,log));result={'sandbox_id':item['sandbox_id'],'passed':code==0};results.append(result)
   if code!=0:halt=True
   b.atomic(out/'progress.json',b.encoded({'results':results,'total':len(selected),'halted':halt,'at':time.time()}));print(json.dumps({'completed':len(results),'total':len(selected),**result}),flush=True)
  if halt and not active:break
  time.sleep(1)
 complete=len(results)==len(selected) and all(r['passed'] for r in results)
 b.atomic(out/('complete.json' if complete else 'paused.json'),b.encoded({'complete':complete,'results':results,'remaining':pending,'model_calls':False,'b200_contacted':False}))
 if complete and json.loads(barrier.read_text())==gate:barrier.unlink()
 assert complete,'Batch paused for reconciliation; no further migrations scheduled'
