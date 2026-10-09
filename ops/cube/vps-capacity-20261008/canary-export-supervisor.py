"""Verify the static supervisor update on one stopped VPS canary; no model tasks."""
import contextlib,importlib.util,json,os,pathlib,sqlite3,subprocess,sys,time,urllib.request
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M16MV7KZSF3YNAJ1VKWKYED5'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
def rows(sql,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:return db.execute(sql,args).fetchall()
if not (root/'stored-runtime-limit-512/applied.json').exists():
 subprocess.run(['/usr/bin/python3',str(root/'raise-vps-stored-runtime-limit-512.py')],check=True,timeout=180)
deadline=time.monotonic()+1800
while True:
 stack=contextlib.ExitStack()
 try:stack.enter_context(b.locked());break
 except BlockingIOError:stack.close();assert time.monotonic()<deadline;time.sleep(2)
with stack:
 out=root/'supervisor-canary-2c7e700';out.mkdir(mode=0o700)
 stack.enter_context(account_maintenance([sid],out))
 spec=importlib.util.spec_from_file_location('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
 runtime=rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))[0][0]
 origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
 request={'worker':'vps','id':'supervisor-canary-2c7e700','sandbox_id':sid,'runtime_id':runtime,'headers':headers,'web_port':3000}
 worker=transport.Worker(request)
 assert rows('select s.status,a.worker_id,a.charged from sandbox s join runtime_binding r on r.sandbox_id=s.id join cube_admission a on a.runtime_id=r.runtime_id where s.id=? and r.runtime_id=?',(sid,runtime))==[('stopped','vps',0)]
 tasks=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))
 assert all(s not in ('running','queued') for _,s in tasks)
 env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
 def api(action):
  req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
  with urllib.request.urlopen(req,timeout=180) as r:assert r.status==200;r.read()
 def healthy():
  deadline=time.monotonic()+60
  while True:
   try:
    status=worker.control('GET','/status');assert not status['active_task'] and all(p['running'] for p in status['processes'])
    worker.http('GET','/',headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)},timeout=10);return
   except (OSError,RuntimeError,AssertionError):assert time.monotonic()<deadline;time.sleep(1)
 try:
  api('start');healthy()
  result=subprocess.run(ssh+['python3','/opt/baarcha-vps-export-recovery-2c7e700/worker.py','--container',runtime],capture_output=True,timeout=260)
  (out/'native-output.log').write_bytes(result.stdout+result.stderr);assert result.returncode==0
  receipts=[json.loads(l) for l in result.stdout.splitlines()];assert len(receipts)==1 and receipts[0]['status']=='updated' and receipts[0]['config_preserved']
  healthy();api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;healthy()
  assert rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==tasks
  b.atomic(out/'passed.json',b.encoded({'passed':True,'revision':'2c7e700','sandbox_id':sid,'runtime_id':runtime,'wake_seconds':wake,'config_preserved':True,'task_history_unchanged':True,'model_calls':False,'receipt':receipts[0]}));print('Supervisor canary passed',flush=True)
 finally:
  assert rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==tasks,'User task started; preserve runtime for review'
  api('stop')
