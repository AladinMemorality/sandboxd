"""Compact current VPS pause images in bounded batches, without waking guests.

Only published XFS memory artifacts are eligible. The kernel compares blocks
before sharing them; SHA256 checks cover both zero-hole and dedupe operations.
No source, filesystem image, template, running guest, or B200 is selected.
"""
import argparse, contextlib, hashlib, importlib.util, json, os, pathlib, sqlite3, subprocess, sys, time, urllib.request
P = pathlib.Path
parser=argparse.ArgumentParser()
parser.add_argument('--scheduled',action='store_true',help='Skip busy operator locks; cap work at four batches')
args=parser.parse_args()
os.umask(0o077)
ROOT = P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec = importlib.util.spec_from_file_location('boot', '/usr/local/libexec/baarcha-cube-boot-transition.py')
b = importlib.util.module_from_spec(spec); spec.loader.exec_module(b)
SSH = ['ssh', '-i', '/opt/baarcha-cube/worker-01/operator-key', '-p', '20222',
       '-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts', '-oBatchMode=yes',
       '-oConnectTimeout=10', '-oServerAliveInterval=10', '-oServerAliveCountMax=3', 'root@127.0.0.1']
INNER = r'''
import hashlib,json,os,pathlib,re,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/data/cubelet/storage/xfs')
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
out=P('/data/baarcha-memory-dedupe')/GENERATION/('batch-%03d'%BATCH)
out.mkdir(mode=0o700,parents=True)
files=[];handles=[];skipped=[];names=[]
def sha(f):
 f.seek(0);return hashlib.file_digest(f,'sha256').hexdigest()
def free():
 s=os.statvfs('/data');return s.f_bavail*s.f_frsize
started=time.monotonic();free_before=free()
try:
 for row in SCOPE:
  sid=row['snapshot_id'];assert re.fullmatch('snap-[0-9a-f]{24}',sid)
  try:
   catalog=json.loads((root/'pause-snapshots'/sid/'metadata/catalog.json').read_text())
   assert catalog['snapshot_id']==sid and catalog['kind']=='pause_snapshot' and catalog['backend']=='xfs' and catalog['memory_kind']=='snapshot'
   name=catalog['memory_vol'];assert name=='tpl-'+sid+'-memory'
   paths=list((root/'objects/volumes').glob('*/'+name))
   if not paths:skipped.append(sid);continue
   assert len(paths)==1;p=paths[0]
   assert not p.is_symlink() and p.resolve().is_relative_to(root/'objects/volumes')
   # Pin before hashing: normal GC may retire the source name while a visitor
   # wakes and pauses this app. A vanished source is skipped, never recreated.
   name=out/('memory-%03d'%len(files))
   os.link(p,name,follow_symlinks=False)
   assert not name.is_symlink()
   f=name.open('rb');handles.append(f);s=os.fstat(f.fileno())
   assert s.st_uid==0 and s.st_size==row['memory_bytes']
   names.append(str(name))
   files.append({**row,'path':str(p),'inode':s.st_ino,'bytes':s.st_size,'sha256':sha(f)})
  except FileNotFoundError:skipped.append(sid)
 (out/'before.json').write_text(json.dumps(files))
 # Temporary hardlinks pin the reviewed inodes across normal snapshot GC.
 # They are private, bounded to eight images, and removed after verification.
 fds=[f.fileno() for f in handles]
 for name in names:
  subprocess.run(['nice','-n','15','ionice','-c','3','fallocate','--dig-holes','--',name],pass_fds=fds,check=True,timeout=120)
 if names:
  with (out/'duperemove.log').open('wb') as log:
   subprocess.run(['nice','-n','15','ionice','-c','3','duperemove','-d','-b','64K','--skip-zeroes','--io-threads=1','--cpu-threads=1','--dedupe-options=partial,same,nofiemap','--hashfile='+str(out/'hashes.db'),*names],pass_fds=fds,check=True,stdout=log,stderr=subprocess.STDOUT,timeout=600)
 for f,saved in zip(handles,files):
  assert sha(f)==saved['sha256'] and os.fstat(f.fileno()).st_ino==saved['inode'] and os.fstat(f.fileno()).st_size==saved['bytes']
  os.fsync(f.fileno());f.close()
 for name in names:P(name).unlink()
 fs=os.statvfs('/data');result={'passed':True,'files':files,'count':len(files),'retired_before_open':skipped,'contents_identical':True,'inodes_preserved':True,'block_size':65536,'filesystem_free_delta_bytes':free()-free_before,'data_used_bytes':(fs.f_blocks-fs.f_bfree)*fs.f_frsize,'data_total_bytes':fs.f_blocks*fs.f_frsize,'elapsed_seconds':time.monotonic()-started,'at':time.time()}
 (out/'complete.json').write_text(json.dumps(result));print(json.dumps(result))
finally:
 for f in handles:f.close()
'''
def rows():
    with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro', uri=True) as db:
        db.row_factory=sqlite3.Row
        return [dict(r) for r in db.execute("select b.sandbox_id,b.runtime_id,b.template_id from runtime_binding b join sandbox s on s.id=b.sandbox_id join cube_admission a on a.runtime_id=b.runtime_id where a.worker_id='vps' and s.status='stopped' and a.state='released' and a.charged=0 order by b.template_id,b.sandbox_id")]
def snapshot(row, policy):
    req=urllib.request.Request('http://10.254.240.1:18089/cube/sandbox/info?sandbox_id='+row['runtime_id']+'&instance_type=cubebox',headers={'X-Caller':'baarcha-controller'})
    info=json.load(urllib.request.urlopen(req,timeout=20))['data'][0]
    assert info['sandbox_id']==row['runtime_id'] and info['host_id']=='10.0.2.15'
    if info['status']!=5:return None
    a=info['annotations'];a=json.loads(a) if isinstance(a,str) else a
    return {**row,'snapshot_id':a['cube.master.pause.snapshot.id'],'memory_bytes':policy['templates'][row['template_id']]['memory_mb']*1024**2}
@contextlib.contextmanager
def operator_locks():
 # Wait only before the operation starts; never replay a failed mutation.
 deadline=time.monotonic()+1800
 with contextlib.ExitStack() as stack:
  while True:
   try:stack.enter_context(b.locked());break
   except BlockingIOError:
    if args.scheduled:
     print(json.dumps({'skipped':True,'reason':'operator lock busy'}),flush=True);sys.exit(0)
    assert time.monotonic()<deadline,'Operator lock wait exceeded';time.sleep(2)
  yield
with operator_locks():
    assert json.loads((ROOT/'memory-dedupe-batch-01/wakes/complete.json').read_text())['passed']
    assert all((p/'complete.json').exists() for p in (ROOT/'memory-compaction').glob('fleet-*')), 'An unfinished compaction needs operator review'
    generation='fleet-'+str(time.time_ns());out=ROOT/'memory-compaction'/generation;out.mkdir(mode=0o700,parents=True)
    env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env'])
    policy=json.loads(env['SANDBOXD_CUBE_ADMISSION']);scope=rows();results=[]
    state_path=ROOT/'memory-compaction'/'processed.json'
    processed=set(json.loads(state_path.read_text())['snapshot_ids']) if state_path.exists() else set()
    b.atomic(out/'scope.json',b.encoded(scope))
    for batch,start in enumerate(range(0,len(scope),8)):
        if args.scheduled and len(results)>=4:break
        # Refresh current placement and pause IDs before every bounded batch.
        current={r['sandbox_id']:r for r in rows()};selected=[]
        for row in scope[start:start+8]:
            if current.get(row['sandbox_id'])!=row:continue
            item=snapshot(row,policy)
            if item:selected.append(item)
        if not selected or all(r['snapshot_id'] in processed for r in selected):continue
        code='SCOPE='+repr(selected)+'\nGENERATION='+repr(generation)+'\nBATCH='+str(batch)+'\n'+INNER
        b.atomic(out/('batch-%03d-intent.json'%batch),b.encoded(selected))
        b.atomic(out/('batch-%03d-worker.py'%batch),code.encode())
        result=json.loads(subprocess.check_output(SSH+['python3','-'],input=code.encode(),timeout=1000))
        b.atomic(out/('batch-%03d.json'%batch),b.encoded(result));results.append(result)
        processed.update(r['snapshot_id'] for r in result['files'])
        b.atomic(state_path,b.encoded({'snapshot_ids':sorted(processed),'last_generation':generation,'at':time.time()}))
        print(json.dumps({k:v for k,v in result.items() if k!='files'}),flush=True)
    report={'complete':True,'generation':generation,'files':sum(r['count'] for r in results),'filesystem_free_delta_bytes':sum(r['filesystem_free_delta_bytes'] for r in results),'last_data_used_bytes':results[-1]['data_used_bytes'] if results else None,'b200_contacted':False,'model_calls':False,'scheduled':args.scheduled,'at':time.time()}
    b.atomic(out/'complete.json',b.encoded(report));print(json.dumps(report),flush=True)
