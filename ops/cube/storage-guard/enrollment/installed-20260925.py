#!/usr/bin/env python3
"""Reviewed observer-only installation; no controller/worker/guest operations."""
import contextlib,fcntl,hashlib,importlib.util,json,os,pathlib,stat,subprocess,time
P=pathlib.Path
BASE=P('/opt/baarcha-bench/cube-storage-enrollment-reviewed-20260925-01')
RECEIPT=BASE/'installation-01'
CP='54f9d073a7c2739b55481d5bbbb80f22847cb1e1d3225fe5af9590f2f48de5f3'
IMAGE='sha256:14e9b9244729875d15b95082c100b61582c929b48deba1ecd60b38729f344f82'
FILES=[
(BASE/'observe.py',P('/opt/baarcha-cube/storage-guard/observe.py'),'b5e32483aeb77b170cc49cc0c429630f2ba3d3fad880c7c80472e2e278541ed3',0o644),
(BASE/'reviewed-configs/storage-guard.json',P('/etc/baarcha-cube/storage-guard.json'),'9df51e0cf0af1735388506192ba7758214a683c81da2536224f998ffa6bf3bd8',0o600),
(BASE/'baarcha-cube-storage-observer.service',P('/etc/systemd/system/baarcha-cube-storage-observer.service'),'dca21f6d32a809cd37c7eeb27f5e7898b104acb960270175fa8a5dd6fa5d3c25',0o644),
(BASE/'baarcha-cube-storage-observer.timer',P('/etc/systemd/system/baarcha-cube-storage-observer.timer'),'452820725ae1c963ec0b94ddb1cefedfdbd710eb9b44393c7a3e337418379de1',0o644)]
DIRS=[P('/opt/baarcha-cube/storage-guard'),P('/var/lib/sandboxd/cube-storage-observer'),P('/run/sandboxd-cube-storage')]
def need(v,m):
 if not v:raise RuntimeError(m)
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def safe(p):
 for item in [p,*p.parents]:
  s=item.lstat();sticky_lock_root=(item==P('/run/lock') and stat.S_ISDIR(s.st_mode) and s.st_mode&0o7777==0o1777)
  need(not stat.S_ISLNK(s.st_mode) and s.st_uid==0 and (not s.st_mode&0o022 or sticky_lock_root),'unsafe root-owned path')
def write(p,raw,mode=0o600):
 fd=os.open(p,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,mode)
 with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
 d=os.open(p.parent,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
 try:os.fsync(d)
 finally:os.close(d)
def jwrite(p,x):write(p,(json.dumps(x,indent=2)+'\n').encode())
def ctl(*args):return subprocess.check_output(['systemctl',*args],text=True,timeout=35).strip()
def main():
 os.umask(0o077);need(os.geteuid()==0,'root required')
 with contextlib.ExitStack() as locks:
  for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
   p=P(name);safe(p);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);locks.callback(os.close,fd);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  for p in [BASE,RECEIPT.parent,*[d.parent for d in DIRS],*[dst.parent for _,dst,_,_ in FILES if dst.parent not in DIRS]]:safe(p)
  need(not RECEIPT.exists(),'new receipt directory required')
  for p in [*DIRS,*[dst for _,dst,_,_ in FILES]]:need(not os.path.lexists(p),'unexpected existing observer state or installation')
  for unit in ['baarcha-cube-storage-observer.service','baarcha-cube-storage-observer.timer']:need(ctl('show','--value','-p','LoadState',unit)=='not-found','unexpected loaded observer unit')
  for src,_,digest,_ in FILES:safe(src);need(sha(src)==digest,'reviewed source mismatch')
  cp=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
  env=dict(x.split('=',1) for x in cp['Config']['Env'] if '=' in x)
  need(cp['Id']==CP and cp['Image']==IMAGE and cp['State']['Running'],'controller generation drift')
  need(env.get('SANDBOXD_CUBE_ENABLED')=='false' and env.get('SANDBOXD_CUBE_REVERSE_EGRESS')=='false','Cube must remain disabled')
  spec=importlib.util.spec_from_file_location('observer',BASE/'observe.py');o=importlib.util.module_from_spec(spec);spec.loader.exec_module(o)
  c=o.trusted_read(BASE/'reviewed-configs/storage-guard.json');o.validate_config(c)
  need(o.read_outer_boot()==c['outer_boot_id'],'outer boot changed')
  need(P('/proc/3026420/stat').read_text().rsplit(')',1)[1].split()[19]=='483403245','worker process changed')
  inner=o.probe_inner();outer=o.probe_outer()
  need(inner['worker_boot_id']==c['expected_boot_id'] and inner['worker_machine_id']==c['worker_machine_id'] and inner['inner_fs_uuid']==c['inner_fs_uuid'] and outer['outer_fs_uuid']==c['outer_fs_uuid'],'worker/filesystem changed')
  need(min(inner['inner_free_bytes'],outer['outer_free_bytes'])>=96*1024**3,'insufficient reserve')
  RECEIPT.mkdir(mode=0o700)
  jwrite(RECEIPT/'before.json',{'controller_id':CP,'controller_image':IMAGE,'inner':inner,'outer':outer,'files_previously_absent':True,'installed_at_unix':int(time.time())})
  for d in DIRS:d.mkdir(mode=0o700)
  for src,dst,digest,mode in FILES:write(dst,src.read_bytes(),mode);need(sha(dst)==digest,'installed hash mismatch')
  ctl('daemon-reload')
  ctl('start','baarcha-cube-storage-observer.service')
  need(ctl('show','--value','-p','Result','baarcha-cube-storage-observer.service')=='success','first observer service failed')
  sample=o.trusted_read(P(c['observation_path']));sequence=o.trusted_read(o.STATE_DIR/'sequence.json')
  need(sample['generation']==sequence['generation']==1,'unexpected initial generation')
  for key,want in {'observer_id':c['observer_id'],'outer_boot_id':c['outer_boot_id'],'worker_boot_id':c['expected_boot_id'],'worker_machine_id':c['worker_machine_id'],'inner_fs_uuid':c['inner_fs_uuid'],'outer_fs_uuid':c['outer_fs_uuid']}.items():need(sample[key]==want,'first observation pin mismatch')
  now=o.boottime_ns()
  need(0<sample['started_boottime_ns']<=sample['completed_boottime_ns']<=now and now-sample['started_boottime_ns']<30_000_000_000,'first observation stale/future')
  need(min(sample['inner_free_bytes'],sample['outer_free_bytes'])>=96*1024**3,'first observation lacks reserve')
  safe(P(c['observation_path']));safe(o.STATE_DIR/'sequence.json')
  jwrite(RECEIPT/'first-observation.json',sample)
  ctl('enable','--now','baarcha-cube-storage-observer.timer')
  need(ctl('is-enabled','baarcha-cube-storage-observer.timer')=='enabled' and ctl('is-active','baarcha-cube-storage-observer.timer')=='active','timer not enabled/active')
  deadline=time.monotonic()+18
  while time.monotonic()<deadline:
   latest=o.trusted_read(P(c['observation_path']))
   if latest['generation']>sample['generation']:break
   time.sleep(1)
  need(latest['generation']>sample['generation'],'timer did not advance observation')
  need(ctl('show','--value','-p','Result','baarcha-cube-storage-observer.service')=='success','timer observation failed')
  need(0<=o.boottime_ns()-latest['started_boottime_ns']<30_000_000_000,'timer observation stale')
  cp2=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
  need(cp2['Id']==CP and cp2['State']['Running'],'controller changed during observer install')
  result={'version':1,'installed':True,'timer_enabled':True,'timer_active':True,'controller_unchanged':True,'controller_id':CP,'cube_enabled':False,'first_observation':sample,'timer_observation':latest,'file_sha256':{str(dst):sha(dst) for _,dst,_,_ in FILES},'coordinators_changed':False,'guests_created':0,'completed_at_unix':int(time.time())}
  jwrite(RECEIPT/'installed.json',result)
  print(json.dumps({'receipt':str(RECEIPT/'installed.json'),'sha256':sha(RECEIPT/'installed.json'),'first_generation':sample['generation'],'timer_generation':latest['generation'],'inner_free':latest['inner_free_bytes'],'outer_free':latest['outer_free_bytes']}),flush=True)
if __name__=='__main__':main()

