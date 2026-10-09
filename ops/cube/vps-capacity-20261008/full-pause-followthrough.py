"""Wait for user work, then execute the reviewed VPS rollout and acceptance once."""
import contextlib,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'full-pause-followthrough-02';out.mkdir(mode=0o700)
def save(name,v):(out/name).write_text(json.dumps(v)+'\n')
def quiet():
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
  return db.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0 and db.execute("select count(*) from cube_admission where state='pending'").fetchone()[0]==0

def wait_quiet(stage,capacity=False):
 deadline=time.monotonic()+7200;since=None
 while True:
  reason='active user task or provider operation'
  okay=quiet()
  if okay and capacity:
   p=subprocess.run(['/usr/bin/python3',str(root/'real-preview-density.py'),'--plan'],capture_output=True,timeout=90)
   okay=p.returncode==0 and json.loads(p.stdout).get('ready') is True
   reason='50-preview cohort does not currently fit the unchanged admission budget'
   if not okay:(out/'capacity-plan.PRIVATE.log').write_bytes(p.stdout+p.stderr)
  if okay:
   if since is None:since=time.monotonic()
   if time.monotonic()-since>=30:return
  else:since=None
  save('stage.json',{'stage':stage,'waiting':True,'reason':reason if not okay else 'quiet-period verification','at':time.time()})
  assert time.monotonic()<deadline,'Wait deadline reached; no forced user-task or app stop'
  time.sleep(10)
def run(stage,script,timeout):
 save('stage.json',{'stage':stage,'waiting':False,'at':time.time()});print(stage,flush=True)
 with (out/(stage+'.PRIVATE.log')).open('wb') as log:
  p=subprocess.run(['/usr/bin/python3',str(root/script)],stdout=log,stderr=subprocess.STDOUT,timeout=timeout)
 assert p.returncode==0,'Stage failed; no retry: '+stage
try:
 wait_quiet('await-deployment');run('deployment','deploy-full-pause.py',600)
 wait_quiet('await-canary');run('canary','full-pause-canary.py',1200)
 wait_quiet('await-capacity',capacity=True);run('fleet-acceptance','storage-density-followthrough.py',11*3600)
 save('complete.json',{'complete':True,'model_calls':False,'b200_contacted':False,'at':time.time()})
except BaseException as error:
 save('failed.json',{'type':type(error).__name__,'reason':str(error)[:300],'at':time.time()});raise
