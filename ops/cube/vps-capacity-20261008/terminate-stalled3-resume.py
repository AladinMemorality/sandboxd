"""Terminate only the identified shim after its failed resume and native shutdown."""
import contextlib,hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
sid='01M3MHG8KQHE8BM4JF8W7YAZ4Q';rid='b11475484a4544fbb48dad0660e7d0f7';snap='snap-733b1dac377d4e48b701ccef'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
with contextlib.ExitStack() as stack:
 deadline=time.monotonic()+1800
 while True:
  try:stack.enter_context(b.locked());break
  except BlockingIOError:
   assert time.monotonic()<deadline;time.sleep(2)
 out=root/'stalled-resume-03';out.mkdir(mode=0o700)
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
  assert db.execute('select s.status,b.runtime_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,)).fetchone()==('stopped',rid,'pending',1)
  tasks=db.execute('select * from task where sandbox_id=? order by task_id',(sid,)).fetchall()
  with sqlite3.connect('file:/var/backups/baarcha-vps-source/20261009T162045Z/controller.PRIVATE.sqlite?mode=ro',uri=True) as saved:assert tasks==saved.execute('select * from task where sandbox_id=? order by task_id',(sid,)).fetchall()
 records=[]
 for p in (root/'memory-compaction/fleet-1791568404355196454').glob('batch-[0-9][0-9][0-9].json'):
  v=json.loads(p.read_text());assert v['passed'] and v['contents_identical'];records += [r for r in v['files'] if r['runtime_id']==rid and r['snapshot_id']==snap]
 assert len(records)==1;record=records[0]
 code='RID='+repr(rid)+'\nSNAP='+repr(snap)+'\nEXPECTED='+repr(record)+'\n'+r'''
import hashlib,json,os,pathlib,signal,subprocess,time,urllib.request
P=pathlib.Path;assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
req=urllib.request.Request('http://127.0.0.1:8089/cube/sandbox/info?sandbox_id='+RID+'&instance_type=cubebox',headers={'X-Caller':'baarcha-controller'})
x=json.load(urllib.request.urlopen(req,timeout=15))['data'][0];a=x['annotations'];a=json.loads(a) if isinstance(a,str) else a
assert x['status']==5 and x['host_id']=='10.0.2.15' and a['cube.master.pause.snapshot.id']==SNAP
memory=P(EXPECTED['path']);assert memory.stat().st_ino==EXPECTED['inode'] and memory.stat().st_size==EXPECTED['bytes']
with memory.open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==EXPECTED['sha256']
logs=[]
for line in subprocess.check_output(['tail','-n','25000','/data/log/CubeShim/cube-shim-req.log'],text=True).splitlines():
 try:v=json.loads(line)
 except ValueError:continue
 if v.get('InstanceId')==RID and v.get('Timestamp','')>'2026-10-09T18:47:20Z':logs.append(v.get('LogContent',''))
assert 'wait ch exit' in logs and any('shutdown req start' in v for v in logs)
pid=3347588;proc=P('/proc')/str(pid);fd=os.pidfd_open(pid)
try:
 start=proc.joinpath('stat').read_text().split()[21]
 assert os.readlink(proc/'exe')=='/data/cubelet/root/component_versions/cube-shim/v0.7.1/bin/containerd-shim-cube-rs'
 assert RID.encode() in (proc/'cmdline').read_bytes().split(b'\0')
 assert any(os.readlink(p)==str(memory) for p in (proc/'fd').iterdir())
 current=subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list','-q'],text=True,timeout=15).split();assert RID not in current
 signal.pidfd_send_signal(fd,signal.SIGTERM)
 deadline=time.monotonic()+20
 while proc.exists() and proc.joinpath('stat').read_text().split()[2]!='Z' and time.monotonic()<deadline:time.sleep(.25)
 if proc.exists() and proc.joinpath('stat').read_text().split()[2]!='Z':
  assert proc.joinpath('stat').read_text().split()[21]==start
  signal.pidfd_send_signal(fd,signal.SIGKILL)
  deadline=time.monotonic()+10
  while proc.exists() and proc.joinpath('stat').read_text().split()[2]!='Z' and time.monotonic()<deadline:time.sleep(.25)
 assert not proc.exists() or proc.joinpath('stat').read_text().split()[2]=='Z'
finally:os.close(fd)
assert memory.exists() and memory.stat().st_ino==EXPECTED['inode']
print(json.dumps({'terminated':True,'runtime_id':RID,'snapshot_id':SNAP,'snapshot_matches_precompaction_hash':True,'source_snapshot_retained':True,'pid':pid,'at':time.time()}))
'''
 b.atomic(out/'worker.py',code.encode())
 result=json.loads(subprocess.check_output(ssh+['python3','-'],input=code.encode(),timeout=120));b.atomic(out/'terminated.json',b.encoded(result));print(json.dumps(result))
