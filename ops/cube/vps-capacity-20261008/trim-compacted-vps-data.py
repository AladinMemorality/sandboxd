"""Return freed blocks on the pinned worker data disk to its NVMe backing file."""
import contextlib,importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def stats():
 p=P('/mnt/nvme/baarcha-cube/worker-01/data.qcow2');s=p.stat();assert s.st_ino==3932163
 fs=os.statvfs(p.parent);free=fs.f_bavail*fs.f_frsize
 return {'allocated_bytes':s.st_blocks*512,'nvme_free_bytes':free,'headroom_if_data_full_bytes':free-max(0,784*1024**3-s.st_blocks*512)}
with contextlib.ExitStack() as stack:
 deadline=time.monotonic()+1800
 while True:
  try:stack.enter_context(b.locked());break
  except BlockingIOError:
   assert time.monotonic()<deadline;time.sleep(2)
 out=root/('data-trim-'+str(time.time_ns()));out.mkdir(mode=0o700);before=stats();assert before['headroom_if_data_full_bytes']>128*1024**3
 code="""import pathlib,subprocess,json,os
assert pathlib.Path('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
assert subprocess.check_output(['findmnt','-n','-o','UUID','--target','/data'],text=True).strip()=='793c3349-db9c-4815-9842-989ed484f1f8'
result=subprocess.check_output(['fstrim','--verbose','--minimum','1MiB','/data'],text=True,timeout=300)
s=os.statvfs('/data');print(json.dumps({'trim':result.strip(),'data_used_bytes':(s.f_blocks-s.f_bfree)*s.f_frsize,'data_total_bytes':s.f_blocks*s.f_frsize}))
"""
 b.atomic(out/'before.json',b.encoded(before));result=json.loads(subprocess.check_output(ssh+['python3','-'],input=code.encode(),timeout=360));after=stats();assert after['headroom_if_data_full_bytes']>128*1024**3
 report={'complete':True,'before':before,'after':after,'worker':result,'minimum_extent_bytes':1048576,'b200_contacted':False,'at':time.time()};b.atomic(out/'complete.json',b.encoded(report));print(json.dumps(report))
