"""Recover only the identified unresponsive old checkpoint from retained source."""
import contextlib,hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
sid='01M3MHG8KQHE8BM4JF8W7YAZ4Q';old='b11475484a4544fbb48dad0660e7d0f7';out=root/'finish-stalled3-recovery-34';out.mkdir(mode=0o700)
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def run(name,*args):
 print(json.dumps({'stage':name,'at':time.time()}),flush=True)
 subprocess.run(['/usr/bin/python3',str(root/name),*args],check=True)
deadline=time.monotonic()+1800
while subprocess.check_output(['systemctl','show','baarcha-vps-stalled-resume-33','-p','ActiveState','--value'],text=True).strip() in ('active','activating'):
 assert time.monotonic()<deadline;time.sleep(2)
assert json.loads((root/'stalled-resume-03/terminated.json').read_text())['terminated']
code=(root/'capture-stalled3-source.py').read_bytes();sha=hashlib.sha256(code).hexdigest()
install="import pathlib,hashlib,os; p=pathlib.Path('/root/baarcha-source-backup-tools/capture-stalled3-20261009.py'); data="+repr(code)+"; assert hashlib.sha256(data).hexdigest()=="+repr(sha)+"; f=p.open('xb'); f.write(data); f.close(); p.chmod(0o600)"
subprocess.run(ssh+['python3','-'],input=install.encode(),check=True,timeout=30)
run('backup-stalled3-source.py')
run('reconcile-stalled3-resume.py')
run('prepare-stalled3-source.py',sid)
run('restore-stalled3-source.py',sid,'balanced','stalled3a')
job=root/'recovery-moves'/('vps-restore-'+sid.lower()+'-stalled3a')
receipt=json.loads((job/'complete.json').read_text());assert receipt['restored'] and receipt['source_retained'] and receipt['all_content_verified']
request=json.loads((job/'worker-job.PRIVATE.json').read_text());new=request['runtime_id'];assert new!=old
for cycle in [1,2]:
 v=json.loads((job/('repeat-wake-'+str(cycle)+'.json')).read_text());assert v['passed'] and v['runtime_id']==new
assert len(list(job.glob('module-health-*.json')))>=4
supervisor=json.loads((job/'supervisor-update.json').read_text());expected=json.loads((root/'supervisor-canary-2c7e700/passed.json').read_text())['receipt']['sha256'];assert supervisor['sha256']==expected
with contextlib.ExitStack() as stack:
 deadline=time.monotonic()+1800
 while True:
  try:stack.enter_context(b.locked());break
  except BlockingIOError:
   assert time.monotonic()<deadline;time.sleep(2)
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute('select s.status,b.runtime_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,)).fetchone()==('stopped',new,'released',0)
  assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
  assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
  history=[{'task_id':r[0],'status':r[1]} for r in db.execute('select task_id,status from task where sandbox_id=? order by task_id',(sid,))]
 case=root/'fleet-wake-validation-01'/(sid+'-'+new);case.mkdir(mode=0o700)
 result={'sandbox_id':sid,'runtime_id':new,'verification':'source-recovery-full-wakes','original_running_preserved':True,'supervisor_sha256':expected}
 b.atomic(case/'passed.json',b.encoded({'tasks':history,'result':result,'source_recovery_journal':str(job),'at':time.time()}))
 failed=root/'fleet-wake-validation-01'/(sid+'-'+old);assert (failed/'failed.json').exists()
 v={'passed':True,'sandbox_id':sid,'old_runtime_id':old,'runtime_id':new,'source_retained':True,'all_content_verified':True,'native_cycles':3,'b200_contacted':False,'model_calls':False,'at':time.time()}
 b.atomic(failed/'source-recovery.json',b.encoded(v));b.atomic(out/'complete.json',b.encoded(v));print(json.dumps(v),flush=True)
