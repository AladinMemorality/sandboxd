"""Read the exact older Motion supervisor binary; preserve app lifecycle state."""
import base64,hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,time
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('copy','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
sid='01M3CKN99ZF90BEEA4DS66YAQV';rid='6516e4dc614d4a1990360e186344c709';expected='b300f9ce69e2377851c978bb546ac30e553abae388f7ac38807764189eb78434'
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def rows(query,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:return db.execute(query,args).fetchall()
with b.locked():
 assert not rows("select task_id from task where status in ('running','queued')")
 before=rows('select s.status,b.runtime_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,))[0]
 assert before[1]==rid and (before[0],before[2],before[3]) in [('stopped','released',0),('running','active',1)]
 history=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,));out=root/'review-motion-supervisor'/'capture-02';out.mkdir(mode=0o700)
 b.atomic(out/'before.json',b.encoded({'state':before,'tasks':history}));started=False;activity=None
 with account_maintenance([sid],out):
  try:
   if before[0]=='stopped':
    started=True;assert copy.api('POST','/v1/sandboxes/'+sid+'/start')[0]==200
    activity=rows('select last_active_at from sandbox where id=?',(sid,))[0][0]
   # Cube's exec transport requires its PTY path. Keep the binary in an
   # explicitly delimited base64 stream, separate from terminal diagnostics.
   remote='RID='+repr(rid)+'\n'+'''import base64,json,os,pathlib,pty,select,subprocess,time
guest="import base64,pathlib,tty;tty.setraw(0);print('BINARY_BEGIN');print(base64.b64encode(pathlib.Path('/usr/local/bin/runtimed').read_bytes()).decode());print('BINARY_END',flush=True)"
master,slave=pty.openpty();p=subprocess.Popen(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','exec','--tty','--exec-id','review-motion-supervisor-02','--user','0',RID,'python3','-c',guest],stdin=slave,stdout=slave,stderr=slave);os.close(slave)
output=bytearray();deadline=time.monotonic()+60
try:
 while time.monotonic()<deadline:
  if not select.select([master],[],[],.2)[0]:continue
  try:data=os.read(master,65536)
  except OSError:break
  if not data:break
  output.extend(data);assert len(output)<24*1024**2
  if b'BINARY_END' in data:break
 assert output.count(b'BINARY_BEGIN')==1 and output.count(b'BINARY_END')==1
 payload=bytes(output).split(b'BINARY_BEGIN',1)[1].split(b'BINARY_END',1)[0].strip();binary=base64.b64decode(payload,validate=True)
 print(json.dumps({'binary':base64.b64encode(binary).decode()}))
finally:
 os.close(master)
 if p.poll() is None:p.terminate()
 p.wait(timeout=5)
'''
   response=json.loads(subprocess.check_output(ssh+['python3','-'],input=remote.encode(),timeout=90));data=base64.b64decode(response['binary'],validate=True)
   b.atomic(out/'binary-observation.json',b.encoded({'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()}))
   assert hashlib.sha256(data).hexdigest()==expected
   b.atomic(out/'runtimed',data)
  finally:
   assert rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==history,'User activity changed; preserve app'
   current=rows('select last_active_at from sandbox where id=?',(sid,))[0][0]
   if started and (activity is None or current<=activity):assert copy.api('POST','/v1/sandboxes/'+sid+'/stop')[0]==200
 b.atomic(out/'captured.json',b.encoded({'sandbox_id':sid,'runtime_id':rid,'sha256':expected,'bytes':len(data),'model_calls':False,'b200_contacted':False,'at':time.time()}))
 print(json.dumps({'captured':True,'sha256':expected,'bytes':len(data)}))
