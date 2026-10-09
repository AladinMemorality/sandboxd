"""Add four reviewed missing client-side modules after exact source restoration.

The private repair bundle stays on the VPS, not in this infrastructure repository.
Existing project files are preserved. No agent/model request is submitted.
"""
import hashlib,importlib.util,json,os,pathlib,subprocess,sys,time,zipfile,urllib.request,sqlite3
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M3MPPSK8RPNMK9TE5Y00EN55';job=root/'recovery-moves'/('vps-restore-'+sid.lower());out=root/'association-repair'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
BIN=root/'observe-release-e261aac/cube-relocate';migrations=root/'queue-release-d463b2d/source/control-plane/migrations'
def cli(action):
 request={'Action':action,'ID':job.name,'Directory':str(job),'Migrations':str(migrations)}
 p=subprocess.run([str(BIN)],input=json.dumps(request).encode(),capture_output=True,timeout=200)
 b.atomic(out/('cli-'+action+'.PRIVATE.log'),p.stdout+p.stderr);assert p.returncode==0,'Relocation '+action+' requires review'
def manifest(path):
 with zipfile.ZipFile(path) as z:
  return {e.filename:(e.external_attr,e.file_size,hashlib.sha256(z.read(e.filename)).hexdigest()) for e in z.infolist()}
with b.locked(),account_maintenance([sid],out):
 assert not (out/'installed.json').exists()
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute('select phase from cube_relocation where id=?',(job.name,)).fetchone()==('complete',)
  assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()==('608b1291da4b466b9e8f5e0c8222d659',)
 request=json.loads((job/'worker-job.PRIVATE.json').read_text());worker=transport.Worker(request);runtime=request['runtime_id']
 proof=json.loads((worker.root/'verified.json').read_text())
 assert proof['RelocationID']==job.name and proof['RuntimeID']==runtime and all(proof[k] for k in ['WorkspaceVerified','HomeVerified','HistoryVerified','ConfigApplied','ApplicationReady'])
 payload=b.trusted(out/'repair.PRIVATE.json');patch=json.loads(payload)
 expected={'src/pages/Checkin.tsx','src/pages/Finance.tsx','src/views/Settings.tsx','src/components/events/SponsorsTab.tsx'}
 assert set(patch['files'])==expected and patch['manifest']['sandbox_id']==sid
 assert all(hashlib.sha256(text.encode()).hexdigest()==patch['manifest']['files'][name] for name,text in patch['files'].items())
 assert json.loads((out/'intent.json').read_text())['payload_sha256']==hashlib.sha256(payload).hexdigest()
 env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
 req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/start',method='POST',headers={'Authorization':'Bearer '+token})
 with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
 worker.control('POST','/workspace/quiesce')
 try:
  worker.http('GET','/export/private-workspace-v2',export=out/'before-install.PRIVATE.zip')
  before=manifest(out/'before-install.PRIVATE.zip');assert not (expected & set(before))
  guest=r'''import pathlib,os,json,sys,hashlib,tty,subprocess,shutil
p=pathlib.Path;cfg=json.loads(sys.argv[1]);root=p('/home/sandbox/workspace/app')
assert os.geteuid()==0 and p('/home/sandbox/.runtimed/workspace-quiesced').exists()
tty.setraw(0);print('RUNTIME_READY',flush=True)
raw=sys.stdin.buffer.read(cfg['bytes']);assert hashlib.sha256(raw).hexdigest()==cfg['sha256'];data=json.loads(raw)
expected={'src/pages/Checkin.tsx','src/pages/Finance.tsx','src/views/Settings.tsx','src/components/events/SponsorsTab.tsx'}
assert set(data['files'])==expected
for name in expected:
 target=root/name;assert not target.exists() and not target.is_symlink()
for name,text in data['files'].items():
 target=root/name;target.parent.mkdir(mode=0o755,parents=True,exist_ok=True);os.chown(target.parent,1000,1000)
 fd=os.open(target,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,0o644)
 with os.fdopen(fd,'wb') as f:f.write(text.encode());os.fchown(f.fileno(),1000,1000);f.flush();os.fsync(f.fileno())
 assert hashlib.sha256(target.read_bytes()).hexdigest()==data['manifest']['files'][name]
for folder in {str((root/n).parent) for n in expected}:
 fd=os.open(folder,os.O_DIRECTORY);os.fsync(fd);os.close(fd)
def owner():os.setgroups([]);os.setgid(1000);os.setuid(1000)
env={'PATH':'/usr/local/bin:/usr/bin:/bin:/home/sandbox/.local/share/pnpm','HOME':'/home/sandbox','NODE_OPTIONS':'--max-old-space-size=384','COREPACK_ENABLE_NETWORK':'0','NO_COLOR':'1'}
check=subprocess.run(['pnpm','exec','tsc','--noEmit','--incremental','false'],cwd=root,env=env,preexec_fn=owner,capture_output=True,text=True,timeout=120)
print('RUNTIME_RECEIPT='+json.dumps({'added_files':sorted(expected),'typescript_exit':check.returncode,'diagnostics':(check.stdout+check.stderr)[-12000:],'model_calls':False}))
'''
  config=json.dumps({'bytes':len(payload),'sha256':hashlib.sha256(payload).hexdigest()})
  guest='try:\n'+''.join(' '+line+'\n' for line in guest.splitlines())+"except BaseException as error:\n import json,traceback\n print('RUNTIME_RECEIPT='+json.dumps({'error':type(error).__name__,'trace':traceback.format_exc()}),flush=True)\n"
  inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-export-recovery-2c7e700');import worker;data=sys.stdin.buffer.read();print(json.dumps(worker.execute("+repr(runtime)+","+repr(guest)+","+repr(config)+",data)))"
  import shlex
  ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
  p=subprocess.run(ssh+['python3 -c '+shlex.quote(inner)],input=payload,capture_output=True,timeout=250)
  b.atomic(out/'guest.PRIVATE.log',p.stdout+p.stderr);assert p.returncode==0,'Repair guest receipt requires review'
  result=json.loads(p.stdout);b.atomic(out/'installed.json',b.encoded(result));assert result['typescript_exit']==0,'TypeScript check requires review'
  worker.http('GET','/export/private-workspace-v2',export=out/'after.PRIVATE.zip');after=manifest(out/'after.PRIVATE.zip')
  assert all(after.get(name)==value for name,value in before.items()),'Existing project files changed'
  assert set(after)-set(before)==expected|{'src/views/'},'Unexpected added project files'
  assert all(after[name][2]==patch['manifest']['files'][name] for name in expected)
  b.atomic(out/'files-verified.json',b.encoded({'existing_files_unchanged':True,'added_files':patch['manifest']['files'],'typescript_passed':True,'source_restored_before_repair':True}))
 finally:worker.control('POST','/workspace/resume')
 headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)}
 deadline=time.monotonic()+90
 while True:
  try:base,html=assets.page(worker.origin,headers);break
  except (OSError,RuntimeError):assert time.monotonic()<deadline;time.sleep(1)
 queue=assets.entries(html);seen=set();total=0
 while queue:
  path=queue.pop(0)
  if path in seen:continue
  seen.add(path);assert len(seen)<=512
  data=worker.http('GET',path,headers=headers,timeout=45);total+=len(data);assert total<=64*1024**2
  queue.extend(p for p in assets.imports(path,data) if p not in seen)
 memory=assets.guest_memory(runtime);assert memory['oom_kill']==0
 b.atomic(out/'module-health.json',b.encoded({'modules':len(seen),'bytes':total,'memory':memory,'passed':True}))
 print(json.dumps({'repair_installed':True,'typescript_passed':True,'modules':len(seen),'original_files_unchanged':True}),flush=True)
