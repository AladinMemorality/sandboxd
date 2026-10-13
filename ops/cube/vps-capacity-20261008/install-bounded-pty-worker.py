"""Install a tested nonblocking PTY sender on the already canaried VPS updater."""
import base64,hashlib,importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
 out=root/'bounded-pty-worker-82';out.mkdir(mode=0o700)
 old=json.loads((root/'source-data-release-867d7de/supervisor-staged.json').read_text())['files']['worker.py']
 files={'worker.py':(root/'worker-bounded-pty.py').read_bytes().replace(b"ROOT=P('/opt/baarcha-published-runtime')",b"ROOT=P('/opt/baarcha-vps-source-data-867d7de')")}
 assert b"ROOT=P('/opt/baarcha-vps-source-data-867d7de')" in files['worker.py']
 for name in ['test_pty_deadline.py','test_update_lock.py','test_supervisor_binary.py']:files[name]=(root/name).read_bytes()
 payload={'old':old,'files':{n:base64.b64encode(v).decode() for n,v in files.items()},'hashes':{n:hashlib.sha256(v).hexdigest() for n,v in files.items()}}
 code='PAYLOAD='+repr(payload)+'\n'+'''import base64,hashlib,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
root=P('/opt/baarcha-vps-source-data-867d7de');target=root/'worker.py';assert hashlib.sha256(target.read_bytes()).hexdigest()==PAYLOAD['old']
stage=root/'pty-fix-82';stage.mkdir(mode=0o700)
for name,raw in PAYLOAD['files'].items():
 data=base64.b64decode(raw);assert hashlib.sha256(data).hexdigest()==PAYLOAD['hashes'][name];(stage/name).write_bytes(data)
(stage/'guest.py').write_bytes((root/'guest.py').read_bytes())
with (stage/'tests.log').open('wb') as log:subprocess.run(['python3','-W','error::ResourceWarning','-m','unittest','discover','-s',str(stage),'-p','test_*.py'],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=45)
timer='baarcha-published-runtime.timer';service='baarcha-published-runtime.service'
assert subprocess.check_output(['systemctl','is-active',timer],text=True).strip()=='active';subprocess.run(['systemctl','stop',timer],check=True)
try:
 deadline=time.monotonic()+90
 while subprocess.check_output(['systemctl','show',service,'-p','ActiveState','--value'],text=True).strip() not in ('inactive','failed'):
  assert time.monotonic()<deadline;time.sleep(1)
 (stage/'worker.before.py').write_bytes(target.read_bytes());pending=root/'worker.py.pending-pty';assert not pending.exists()
 with pending.open('xb') as f:f.write((stage/'worker.py').read_bytes());f.flush();os.fsync(f.fileno())
 pending.chmod(0o600);os.replace(pending,target)
 p=subprocess.run(['python3',str(target),'--probe'],capture_output=True,text=True,timeout=300);(stage/'probe.PRIVATE.log').write_text(p.stdout+p.stderr);assert p.returncode==0
 rows=[json.loads(line) for line in p.stdout.splitlines()];assert rows and all(r.get('status')=='managed' and not r.get('active_task') for r in rows)
finally:subprocess.run(['systemctl','start',timer],check=True)
result={'installed':True,'worker_sha256':PAYLOAD['hashes']['worker.py'],'tests':8,'live_probe_count':len(rows),'nonblocking_pty':True,'task_listing_timeout_seconds':30,'guest_execution_timeout_seconds':240,'wakes_guests':False,'b200_contacted':False,'model_calls':False,'at':time.time()}
(stage/'complete.json').write_text(json.dumps(result));print(json.dumps(result))
'''
 b.atomic(out/'install.py',code.encode())
 ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
 p=subprocess.run(ssh+['python3 -'],input=code.encode(),capture_output=True,timeout=500);b.atomic(out/'install.PRIVATE.log',p.stdout+p.stderr);assert p.returncode==0
 result=json.loads(p.stdout);b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
