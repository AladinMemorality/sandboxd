#!/usr/bin/env python3
"""Incident 2026-10-13: install the VPS scheduler-aware observation, retaining pins.

Run on project-x with the tested observe.py alongside this file. No guest or
controller restart; CubeMaster watches its config every ten seconds.
"""
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent
DEST = Path('/opt/baarcha-cube/storage-guard/observe.py')
BASE = 'b5e32483aeb77b170cc49cc0c429630f2ba3d3fad880c7c80472e2e278541ed3'
LOCKS = ['/opt/baarcha/deploy-release.lock', '/opt/sandboxd/deploy-state/deploy.lock',
         '/run/lock/cube-operator-acceptance.lock', '/opt/baarcha-bench/cube-workload-operator.lock']
SSH = ['ssh', '-i', '/opt/baarcha-cube/worker-01/operator-key', '-o', 'BatchMode=yes',
       '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts',
       '-p', '20222', 'root@127.0.0.1']
WORKER = r'''
import hashlib,json,os,pathlib
root=pathlib.Path('/root/cube-production/scheduler-budget-20261013')
config=pathlib.Path('/usr/local/services/cubetoolbox/CubeMaster/conf.yaml')
pin=pathlib.Path('/etc/baarcha-cube/lifecycle.json')
before=config.read_bytes();old_pin=pin.read_bytes();p=json.loads(old_pin)
assert p['artifacts'][str(config)]==hashlib.sha256(before).hexdigest(),'Lifecycle baseline mismatch'
assert before.count(b'disk_usage_max_percent: 65.0')==1,'Scheduler baseline changed'
v=os.statvfs('/data');assert v.f_bavail*v.f_frsize>250*(1<<30),'Disk headroom changed'
root.mkdir(mode=0o700,exist_ok=False)
(root/'conf.yaml.before').write_bytes(before);(root/'lifecycle.json.before').write_bytes(old_pin)
after=before.replace(b'disk_usage_max_percent: 65.0',b'disk_usage_max_percent: 80.0')
def put(path,data,mode):
 tmp=path.with_name(path.name+'.capacity-new');tmp.write_bytes(data);tmp.chmod(mode);os.replace(tmp,path)
try:
 put(config,after,0o644)
 p['artifacts'][str(config)]=hashlib.sha256(after).hexdigest()
 put(pin,json.dumps(p,indent=2).encode()+b'\n',0o600)
 assert config.read_bytes()==after
 print(json.dumps({'scheduler_limit_percent':80,'physical_free_gib':round(v.f_bavail*v.f_frsize/(1<<30),2),'lifecycle_pin_updated':True}))
except BaseException:
 put(config,before,0o644);put(pin,old_pin,0o600);raise
'''

def main():
    os.umask(0o077)
    with contextlib.ExitStack() as stack:
        for path in LOCKS:
            fd=stack.enter_context(open(path,'a+'));fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
        before=DEST.read_bytes()
        assert hashlib.sha256(before).hexdigest()==BASE,'Observer baseline changed'
        candidate=(ROOT/'observe.py').read_bytes();compile(candidate,str(DEST),'exec')
        with (ROOT/'observe.py.before').open('xb') as out:out.write(before)
        # Raising the provider's ceiling alone remains bounded by the existing
        # physical-space guard. Installing the tighter observer follows it.
        subprocess.run(SSH+['python3 -'],input=WORKER,text=True,check=True,timeout=25)
        temp=DEST.with_name('observe.py.capacity-new');temp.write_bytes(candidate);temp.chmod(0o755);os.replace(temp,DEST)
        subprocess.run(['systemctl','start','baarcha-cube-storage-observer.service'],check=True,timeout=25)
        observation=json.loads(Path('/run/sandboxd-cube-storage/observation.json').read_bytes())
        assert 96*(1<<30)<=observation['inner_free_bytes']<150*(1<<30),'Unexpected effective budget'
        receipt={'installed':True,'observer_sha256':hashlib.sha256(candidate).hexdigest(),
                 'inner_admissible_gib':round(observation['inner_free_bytes']/(1<<30),2),
                 'generation':observation['generation'],'controller_restarted':False}
        (ROOT/'deployed.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt))

if __name__=='__main__':main()
