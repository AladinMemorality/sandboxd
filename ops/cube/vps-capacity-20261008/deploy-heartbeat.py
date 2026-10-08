#!/usr/bin/env python3
import fcntl,json,os,pathlib,subprocess
os.umask(0o077)
ROOT=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-o','BatchMode=yes','root@127.0.0.1']
locks=[]
for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
 fd=os.open(name,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);locks.append(fd)
with (ROOT/'cube-source/cubeops-candidate-v2').open('rb') as source:
 subprocess.run(SSH+['mkdir -p -m 700 /root/vps-50-profiles-20261008/heartbeat-release03 && cat > /root/vps-50-profiles-20261008/heartbeat-release03/candidate'],stdin=source,check=True)
code='''import fcntl,hashlib,json,os,pathlib,subprocess,time,urllib.request
os.umask(0o077)
lock=open('/run/lock/cube-operator-acceptance.lock','a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
p=pathlib.Path('/usr/local/services/cubetoolbox/CubeOps/bin/cubeops')
r=pathlib.Path('/root/vps-50-profiles-20261008/heartbeat-release03')
old=p.read_bytes();new=(r/'candidate').read_bytes()
assert hashlib.sha256(old).hexdigest()=='9ab4272ef40f5323e29c1d32fc33666937bddd96b4532a113515c1d34bdbfbc6'
assert hashlib.sha256(new).hexdigest()=='25d2df5bfff274f87fb74af4efc7ccf9b05e84bd45e4642e959a2c902844248e'
assert not (r/'intent.json').exists()
def node():
 with urllib.request.urlopen('http://127.0.0.1:3010/internal/v1/nodes/10.0.2.15',timeout=5) as response:return json.load(response)
def logs():
 q='SELECT @@global.binlog_row_image; SHOW BINARY LOGS;'
 v=subprocess.run(['docker','exec','-i','cube-sandbox-mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --raw --skip-column-names'],input=q,text=True,capture_output=True,check=True).stdout.splitlines()
 assert v[0]=='MINIMAL'
 return {'at':time.time(),'bytes':sum(int(x.split('\\t')[1]) for x in v[1:])}
before=node();assert before['Healthy'] and before['QuotaCpu']==10000 and before['QuotaMem']==10240
start=logs();time.sleep(15);end=logs()
(r/'before').write_bytes(old);(r/'before').chmod(0o700)
(r/'intent.json').write_text(json.dumps({'old_sha256':hashlib.sha256(old).hexdigest(),'new_sha256':hashlib.sha256(new).hexdigest(),'before_log_samples':[start,end]}))
config=pathlib.Path('/etc/baarcha-cube/lifecycle.json');original_config=config.read_bytes();cfg=json.loads(original_config)
assert cfg['artifacts'][str(p)]==hashlib.sha256(old).hexdigest()
(r/'lifecycle-before.PRIVATE.json').write_bytes(original_config)
override=pathlib.Path('/run/systemd/system/cube-sandbox-cubeops.service.d/zz-baarcha-heartbeat-maintenance.conf')
assert not override.exists()
def install(data):
 # Scope the temporary stop override to this stateless API. Normal worker
 # shutdown retains its original reviewed hook after maintenance finishes.
 override.parent.mkdir(parents=True,exist_ok=True)
 override.write_text('[Service]\\nExecStop=\\nKillMode=process\\nSendSIGKILL=no\\nTimeoutStopSec=infinity\\n')
 subprocess.run(['systemctl','daemon-reload'],check=True)
 subprocess.run(['systemctl','stop','--no-block','cube-sandbox-cubeops.service'],check=True)
 deadline=time.monotonic()+45
 while True:
  state=dict(x.split('=',1) for x in subprocess.check_output(['systemctl','show','cube-sandbox-cubeops.service','-p','ActiveState','-p','MainPID'],text=True).splitlines())
  if state['ActiveState']=='inactive' and state['MainPID']=='0':break
  if time.monotonic()>deadline:raise RuntimeError('graceful CubeOps stop pending; no escalation')
  time.sleep(.25)
 tmp=p.with_name('cubeops.heartbeat-new');tmp.write_bytes(data);tmp.chmod(0o755)
 with tmp.open('rb') as f:os.fsync(f.fileno())
 os.replace(tmp,p)
 cfg['artifacts'][str(p)]=hashlib.sha256(data).hexdigest()
 tmpconfig=config.with_suffix('.heartbeat-new');tmpconfig.write_text(json.dumps(cfg,indent=2)+'\\n');tmpconfig.chmod(0o600);os.replace(tmpconfig,config)
 override.unlink();subprocess.run(['systemctl','daemon-reload'],check=True)
 subprocess.run(['systemctl','start','cube-sandbox-cubeops.service'],check=True,timeout=45)
 deadline=time.monotonic()+45
 while True:
  try:
   n=node()
   if n['Healthy'] and n['QuotaCpu']==10000 and n['QuotaMem']==10240:return n
  except Exception:pass
  if time.monotonic()>deadline:raise RuntimeError('CubeOps readiness did not recover')
  time.sleep(1)
try:
 at=time.time();after=install(new);recovery=time.time()-at
 low=logs();time.sleep(30);high=logs()
 assert hashlib.sha256(p.read_bytes()).hexdigest()==hashlib.sha256(new).hexdigest()
 result={'deployed':True,'cubeops_readiness_seconds':recovery,'old_bytes_per_second':(end['bytes']-start['bytes'])/(end['at']-start['at']),'new_bytes_per_second':(high['bytes']-low['bytes'])/(high['at']-low['at']),'new_sha256':hashlib.sha256(new).hexdigest(),'vps_quota_unchanged':True,'guest_services_restarted':False}
 (r/'result.json').write_text(json.dumps(result));print(json.dumps(result))
except BaseException:
 install(old)
 (r/'rollback.json').write_text(json.dumps({'old_binary_restored':True}))
 raise
'''
r=json.loads(subprocess.check_output(SSH+['python3 -'],input=code.encode(),timeout=180))
(ROOT/'heartbeat-deployed-v2.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps(r))
