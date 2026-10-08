"""Reconcile individual custom restore receipts, then release the Vite barrier."""
import contextlib,importlib.util,json,pathlib,sqlite3,time
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
deadline=time.monotonic()+600
while True:
 stack=contextlib.ExitStack()
 try:stack.enter_context(b.locked());break
 except BlockingIOError:stack.close();assert time.monotonic()<deadline;time.sleep(2)
with stack:
 plan=json.loads((root/'custom-restore-plan.json').read_text());barrier=root/'restore-barrier.json';gate=json.loads(barrier.read_text());assert gate['purpose']=='reviewed-restore-barrier' and set(gate['allowed_sandboxes'])==set(plan['profiles'])
 results=[]
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute("select count(*) from cube_relocation where phase='fenced'").fetchone()==(0,)
  for sid,profile in plan['profiles'].items():
   j=root/'recovery-moves'/('vps-restore-'+sid.lower());receipt=json.loads((j/'complete.json').read_text());request=json.loads((j/'worker-job.PRIVATE.json').read_text())
   assert receipt['restored'] and receipt['source_retained'] and not receipt['source_contacted']
   assert db.execute('select b.runtime_id,a.worker_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id where b.sandbox_id=?',(sid,)).fetchall()==[(request['runtime_id'],'vps')]
   assert [r[0] for r in db.execute('select task_id from task where sandbox_id=? order by task_id',(sid,))]==request['source']['task_ids']
   results.append({'sandbox_id':sid,'profile':profile,'restored':True,'wake_seconds':receipt['wake_seconds']})
 value={'results':results,'total':len(results),'at':time.time(),'source_contacted':False,'model_calls':False}
 for name in ['progress.json','complete.json']:b.atomic(root/'custom-restore-batch'/name,b.encoded(value))
 assert json.loads(barrier.read_text())==gate;barrier.unlink();print(json.dumps({'custom_apps_restored':len(results),'standard_batch_released':True}),flush=True)
