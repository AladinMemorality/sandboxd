"""Repair a proven old export socket inside one already-maintained source VM.

No start/stop, process signals, file replacement, or provider operations occur.
The running migration retains its account guard and four maintenance locks.
"""
import importlib.util,json,pathlib,sqlite3,subprocess,sys,time
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
sid='01M2EAAQ1M9FZDY0F1BQPRM1CA';runtime='c1e08df51db3411fbfa991199f4a6cbb'
job=root/'recovery-moves'/('vps-reprofile-'+sid.lower()+'-768-02')
assert job.is_dir() and not (job/'export-result.PRIVATE.json').exists()
assert json.loads((job/'source-before.json').read_text())['runtime_id']==runtime
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
 assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()==(runtime,)
 assert not db.execute("select task_id from task where sandbox_id=? and status in ('running','queued')",(sid,)).fetchall()
 assert not db.execute("select id from cube_relocation where sandbox_id=? and phase='fenced'",(sid,)).fetchall()
guest=r'''import pathlib,os,json,time,urllib.request,ctypes,socket
p=pathlib.Path;live=[]
for d in p('/proc').iterdir():
 if not d.name.isdigit():continue
 try:
  if os.readlink(d/'exe').removesuffix(' (deleted)')=='/usr/local/bin/runtimed':live.append(d)
 except OSError:pass
assert len(live)==1 and os.uname().machine=='x86_64', 'supervisor identity'
env=dict(v.split('=',1) for v in (live[0]/'environ').read_bytes().decode().split('\0') if '=' in v)
req=urllib.request.Request('http://127.0.0.1:3031/status',headers={'Authorization':'Bearer '+env['RUNTIMED_HTTP_TOKEN']})
with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=5) as r:status=json.load(r)
assert not status.get('active_task') and p('/home/sandbox/.runtimed/workspace-quiesced').exists(), 'quiescence guard'
files=list(p('/home/sandbox/.runtimed').glob('.private-workspace-v2-*'))
assert len(files)==1 and time.time()-files[0].stat().st_mtime>60, json.dumps({'archives':[{'name':f.name,'age':time.time()-f.stat().st_mtime} for f in files], 'control':[l for table in ['/proc/net/tcp','/proc/net/tcp6'] for l in p(table).read_text().splitlines()[1:] if l.split()[1].endswith(':0BD7')]})
stale=[]
for table in ['/proc/net/tcp','/proc/net/tcp6']:
 for line in p(table).read_text().splitlines()[1:]:
  f=line.split()
  if f[1].endswith(':0BD7') and f[3]=='01' and int(f[4].split(':')[0],16)>0 and int(f[6],16)>=5 and f[7]=='1000':
   stale.append((int(f[9]),int(f[2].split(':')[1],16)))
assert len(stale)==1,'No unique old export connection; no sockets changed'
inode,peerport=stale[0]
fds=[int(f.name) for f in (live[0]/'fd').iterdir() if os.readlink(f)=='socket:['+str(inode)+']']
assert len(fds)==1
pidfd=os.pidfd_open(int(live[0].name));libc=ctypes.CDLL(None,use_errno=True)
fd=libc.syscall(438,pidfd,fds[0],0);os.close(pidfd)
if fd<0:raise OSError(ctypes.get_errno(),'pidfd_getfd rejected')
with socket.socket(fileno=fd) as sock:
 assert sock.getsockname()[1]==3031 and sock.getpeername()[1]==peerport
 sock.shutdown(socket.SHUT_RDWR)
print('RUNTIME_RECEIPT='+json.dumps({'inode':inode,'aborted':True,'active_task':False,'quiesced':True,'old_archive_age_seconds':time.time()-files[0].stat().st_mtime if files[0].exists() else None}))
'''
# Always retain a bounded diagnostic if the guest refuses the exact repair.
guest='try:\n'+''.join(' '+line+'\n' for line in guest.splitlines())+'except Exception as e:\n print("RUNTIME_RECEIPT="+json.dumps({"error":type(e).__name__,"detail":str(e)}))\n'
inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-process-recovery-a583d45');import worker;print(json.dumps(worker.execute("+repr(runtime)+","+repr(guest)+",'probe',b'')))"
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
p=subprocess.run(ssh+['python3 -'],input=inner.encode(),capture_output=True,timeout=45)
assert p.returncode==0,'Guest repair receipt unavailable'
result=json.loads(p.stdout);print(json.dumps(result));assert result.get('aborted'),result
(job/'stale-export-repaired.json').write_text(json.dumps(result)+'\n')
