"""Install the exact tested Cubelet only; preserve every existing guest process."""
import fcntl,hashlib,importlib.util,json,os,pathlib,subprocess,time,signal
P=pathlib.Path;os.umask(0o077)
r=P('/root/vps-full-pause-20261009-02');out=r/'install-02';out.mkdir(mode=0o700)
oldsha='de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b'
newsha='43d8c020a949569aa546f2eebea5d632c17ba1231b7c330fd4c31d7a4f72337f'
unit='cube-sandbox-cubelet.service';binary=P('/usr/local/services/cubetoolbox/Cubelet/bin/cubelet');config=P('/etc/baarcha-cube/lifecycle.json')
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
lock=open('/run/lock/cube-operator-acceptance.lock','a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
def run(args,timeout=45):return subprocess.check_output(args,stderr=subprocess.STDOUT,timeout=timeout)
def digest(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def save(name,value):(out/name).write_text(json.dumps(value,sort_keys=True)+'\n')
def atomic(p,data,mode):
 temp=p.with_name(p.name+'.full-pause-new');fd=os.open(temp,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,mode)
 with os.fdopen(fd,'wb') as f:f.write(data);f.flush();os.fsync(f.fileno())
 os.replace(temp,p)
 fd=os.open(p.parent,os.O_DIRECTORY);os.fsync(fd);os.close(fd)
def guests():
 result={}
 for proc in P('/proc').glob('[0-9]*'):
  try:
   exe=str((proc/'exe').resolve(strict=True))
   if exe.startswith('/data/cubelet/root/component_versions/cube-shim/'):
    result[proc.name]={'exe':exe,'start_ticks':(proc/'stat').read_text().rsplit(')',1)[1].split()[19]}
  except (FileNotFoundError,ProcessLookupError):pass
 return result
assert digest(binary)==oldsha and digest(r/'cubelet-candidate')==newsha
proof=json.loads((r/'regression-passed.json').read_text());assert proof['passed'] and proof['candidate_sha256']==newsha and proof['embedded_bpf_preserved']
run(['/usr/bin/python3','/usr/local/libexec/baarcha-cube-worker-lifecycle.py','nested-ready'])
original=config.read_bytes();cfg=json.loads(original);assert cfg['binaries']['cubelet']=={'path':str(binary),'sha256':oldsha}
old=binary.read_bytes();new=(r/'cubelet-candidate').read_bytes();(out/'cubelet-before').write_bytes(old);(out/'cubelet-before').chmod(0o700);(out/'lifecycle-before.PRIVATE.json').write_bytes(original)
before=guests();assert before,'Expected existing production guest'
assert json.loads((r/'direct-stop-witness.json').read_text())['passed']
dependents=['cube-sandbox-cube-egress.service','cube-sandbox-cube-egress-net.service']
def dependency_state():
 return {u:run(['systemctl','show',u,'-p','ActiveState','-p','MainPID']).decode() for u in dependents}
deps=dependency_state();assert all('ActiveState=active' in v for v in deps.values())
assert not run(['systemctl','show',unit,'-p','BoundBy','--value']).strip()

save('intent.json',{'old_sha256':oldsha,'new_sha256':newsha,'guest_processes_before':before,'at':time.time()})
override=P('/run/systemd/system/'+unit+'.d/zz-full-pause-maintenance.conf');assert not override.exists()
def install(data,configuration):
 # TERM only, main process only. Never use the whole-worker stop hook or KILL.
 override.parent.mkdir(parents=True,exist_ok=True)
 override.write_text('[Service]\nExecStop=\nKillMode=process\nSendSIGKILL=no\nTimeoutStopSec=infinity\nRestart=no\n')
 run(['systemctl','daemon-reload'])
 # A systemd StopUnit job cascades to Requires dependents. Signal the exact
 # reviewed main process instead: Requires (without BindsTo) preserves them
 # when the required service exits on its own. Tested on this worker first.
 pid=int(run(['systemctl','show',unit,'-p','MainPID','--value']))
 if pid:
  assert P('/proc',str(pid),'exe').resolve(strict=True)==binary
  assert hashlib.sha256(P('/proc',str(pid),'exe').read_bytes()).hexdigest() in (oldsha,newsha)
  fd=os.pidfd_open(pid)
  try:signal.pidfd_send_signal(fd,signal.SIGTERM)
  finally:os.close(fd)
 deadline=time.monotonic()+60
 while True:
  state=dict(v.split('=',1) for v in run(['systemctl','show',unit,'-p','ActiveState','-p','MainPID']).decode().splitlines())
  if state=={'MainPID':'0','ActiveState':'inactive'}:break
  assert time.monotonic()<deadline,'Graceful stop pending; no escalation permitted'
  time.sleep(.25)
 assert guests()==before,'Guest processes changed during management stop'
 assert dependency_state()==deps,'Dependent service changed during Cubelet exit'
 if not (out/'persistent-metadata-before.PRIVATE').exists():
  run(['cp','-a','--reflink=auto','/data/cubelet/persistent-metadata',str(out/'persistent-metadata-before.PRIVATE')],60)
 atomic(binary,data,0o755);atomic(config,configuration,0o600)
 override.unlink();run(['systemctl','daemon-reload']);run(['systemctl','start',unit],90)
 run(['/usr/bin/python3','/usr/local/libexec/baarcha-cube-worker-lifecycle.py','nested-ready'])
 assert guests()==before,'Guest processes changed during management start'
 assert dependency_state()==deps,'Dependent service changed during Cubelet start'
try:
 cfg['binaries']['cubelet']['sha256']=newsha
 if str(binary) in cfg['artifacts']:
  assert cfg['artifacts'][str(binary)]==oldsha;cfg['artifacts'][str(binary)]=newsha
 began=time.monotonic();install(new,(json.dumps(cfg,indent=2)+'\n').encode())
 assert digest(binary)==newsha
 save('deployed.json',{'deployed':True,'sha256':newsha,'old_sha256':oldsha,'guest_processes_preserved':len(before),'seconds':time.monotonic()-began,'full_pause_snapshot_policy':True,'at':time.time()})
 print((out/'deployed.json').read_text())
except BaseException:
 # Do not replace a binary until the previous process has exited. The same
 # graceful, bounded stop path is required for rollback.
 try:
  install(old,original);save('rolled-back.json',{'restored':True,'sha256':oldsha})
 except BaseException:save('manual-reconciliation-required.json',{'guest_disks_untouched':True,'no_forced_stop':True})
 raise
