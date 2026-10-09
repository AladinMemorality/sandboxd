"""Recover the exact observed stale export socket under scoped maintenance."""
import importlib.util,json,pathlib,shlex,subprocess,sys
from maintenance_account import account_maintenance
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M1XFEHGCQ2HV5NNJBXNQK3WE';runtime='19886838497240a5a507b8ac4d3fe35e'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('copy','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
out=root/'source-control-probe-06';out.mkdir(mode=0o700)
guest="""import pathlib,os,json,time,urllib.request,subprocess
p=pathlib.Path;live=[]
for d in p('/proc').iterdir():
 if not d.name.isdigit():continue
 try:
  if os.readlink(d/'exe').removesuffix(' (deleted)')=='/usr/local/bin/runtimed':live.append(d)
 except OSError:pass
assert len(live)==1
env=dict(v.split('=',1) for v in (live[0]/'environ').read_bytes().decode().split('\\0') if '=' in v)
req=urllib.request.Request('http://127.0.0.1:3031/status',headers={'Authorization':'Bearer '+env['RUNTIMED_HTTP_TOKEN']})
with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=5) as r:status=json.load(r)
assert not status.get('active_task')
files=[{'name':f.name,'bytes':f.stat().st_size,'age':time.time()-f.stat().st_mtime} for f in p('/home/sandbox/.runtimed').glob('.private-workspace-v2-*')]
tcp=[line for table in ['/proc/net/tcp','/proc/net/tcp6'] for line in p(table).read_text().splitlines()[1:] if line.split()[1].endswith(':0BD7')]
assert p('/home/sandbox/.runtimed/workspace-quiesced').exists()
try:
 import ctypes,socket
 assert os.uname().machine=='x86_64'
 fds=[int(f.name) for f in (live[0]/'fd').iterdir() if os.readlink(f)=='socket:[954]']
 assert len(fds)==1
 pidfd=os.pidfd_open(int(live[0].name))
 libc=ctypes.CDLL(None,use_errno=True)
 fd=libc.syscall(438,pidfd,fds[0],0)
 os.close(pidfd)
 if fd<0:raise OSError(ctypes.get_errno(),'pidfd_getfd rejected')
 with socket.socket(fileno=fd) as sock:
  assert sock.getsockname()[1]==3031 and sock.getpeername()[1]==0x2CCE
  sock.shutdown(socket.SHUT_RDWR)
 result={'inode':954,'aborted':True,'method':'pidfd_getfd_shutdown'}
except Exception as error:result={'error':type(error).__name__,'detail':str(error)}
time.sleep(1)
try:
 request=urllib.request.Request('http://127.0.0.1:3031/workspace/quiesce',data=b'{}',method='POST',headers={'Authorization':'Bearer '+env['RUNTIMED_HTTP_TOKEN'],'Content-Type':'application/json'})
 with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request,timeout=5) as response:quiesce=response.status
except Exception as error:quiesce=type(error).__name__
print('RUNTIME_RECEIPT='+json.dumps({'active_task':False,'quiesced_marker':p('/home/sandbox/.runtimed/workspace-quiesced').exists(),'export_files':files,'control_connections':tcp,'aborted_control_connections':result,'quiesce_response':quiesce}))
"""
with b.locked(),account_maintenance([sid],out):
    try:
        code,_=copy.api('POST','/v1/sandboxes/'+sid+'/start');assert code==200
        inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-process-recovery-a583d45');import worker;print(json.dumps(worker.execute("+repr(runtime)+","+repr(guest)+",'probe',b'')))"
        ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
        p=subprocess.run(ssh+['python3 -'],input=inner.encode(),capture_output=True,timeout=45)
        b.atomic(out/'guest.PRIVATE.stdout',p.stdout);b.atomic(out/'guest.PRIVATE.stderr',p.stderr)
        assert p.returncode==0,'Scoped stale socket recovery failed'
        result=json.loads(p.stdout);b.atomic(out/'result.json',b.encoded(result));print(json.dumps(result))
    finally:
        code,_=copy.api('POST','/v1/sandboxes/'+sid+'/stop');assert code==200
