"""Discard only free blocks on the pinned VPS data filesystem."""
import contextlib,fcntl,json,os,pathlib,subprocess,time
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
disk=pathlib.Path('/mnt/nvme/baarcha-cube/worker-01/data.qcow2')
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def run(args):return subprocess.check_output(args,timeout=1230).decode()
with contextlib.ExitStack() as stack:
 for path in ['/run/lock/baarcha-vps-fstrim.lock']:
  f=stack.enter_context(open(path,'a'));fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
 before=disk.stat();assert before.st_ino==3932163
 pid=int(run(['systemctl','show','baarcha-cube-worker-01','-p','MainPID','--value']))
 status=json.loads(pathlib.Path('/opt/baarcha-cube/worker-01/lifecycle-status.json').read_text())
 qemu=status['qemu_pid'];args=pathlib.Path('/proc/'+str(qemu)+'/cmdline').read_bytes().split(b'\0')
 assert ('file='+str(disk)+',if=virtio,format=qcow2,discard=unmap').encode() in args
 code='''import fcntl,json,os,subprocess,pathlib
f=open('/run/lock/cube-operator-acceptance.lock','a');fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
assert subprocess.check_output(['findmnt','-n','-o','UUID','--target','/data']).decode().strip()=='793c3349-db9c-4815-9842-989ed484f1f8'
boot=pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()
r=subprocess.check_output(['fstrim','--verbose','--minimum','16777216','/data'],timeout=1200).decode().strip()
print(json.dumps({'boot_id':boot,'result':r}))
'''
 inner=json.loads(subprocess.check_output(ssh+['python3 -'],input=code.encode(),timeout=1230))
 after=disk.stat();assert after.st_ino==before.st_ino and pathlib.Path('/proc/'+str(qemu)).exists()
 result={'at':time.time(),'worker_boot_id':inner['boot_id'],'before_allocated_bytes':before.st_blocks*512,'after_allocated_bytes':after.st_blocks*512,'reclaimed_bytes':(before.st_blocks-after.st_blocks)*512,'trim':inner['result'],'minimum_free_extent_bytes':16777216,'same_disk_inode':True,'qemu_pid':qemu}
 (root/'trim-result.json').write_text(json.dumps(result));print(json.dumps(result))
