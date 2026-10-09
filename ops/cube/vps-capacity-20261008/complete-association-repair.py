"""Finish a guarded repair of an unfinished customer app; private source stays private."""
import hashlib,importlib.util,json,os,pathlib,subprocess,sys,time,zipfile,urllib.request,sqlite3,shlex
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M3MPPSK8RPNMK9TE5Y00EN55';job=root/'recovery-moves'/('vps-restore-'+sid.lower());out=root/'association-repair'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('assets',root/'preview-assets.py');assets=importlib.util.module_from_spec(spec);spec.loader.exec_module(assets)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
request=json.loads((job/'worker-job.PRIVATE.json').read_text());worker=transport.Worker(request);runtime=request['runtime_id']
env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
def api(action):
 req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
 with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
def manifest(path):
 with zipfile.ZipFile(path) as z:return {e.filename:(e.external_attr,e.file_size,hashlib.sha256(z.read(e.filename)).hexdigest()) for e in z.infolist()}
def health():
 headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)}
 base,html=assets.page(worker.origin,headers);queue=assets.entries(html,base);seen=set();total=0
 while queue:
  path=queue.pop(0)
  if path in seen:continue
  seen.add(path);assert len(seen)<=512
  data=worker.http('GET',path,headers=headers,timeout=45);total+=len(data);assert total<=64*1024**2
  queue.extend(p for p in assets.imports(path,data) if p not in seen)
 memory=assets.guest_memory(runtime);assert memory['oom_kill']==0
 result={'modules':len(seen),'bytes':total,'memory':memory,'passed':True}
 b.atomic(job/('module-health-'+str(time.time_ns())+'.json'),b.encoded(result));return result
with b.locked(),account_maintenance([sid],out):
 assert not (out/'contributions-intent.json').exists()
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()==(runtime,)
  assert not db.execute("select task_id from task where sandbox_id=? and status in ('running','queued')",(sid,)).fetchall()
 assert json.loads((out/'installed.json').read_text())['typescript_exit']==2
 payload=b.trusted(out/'contributions.PRIVATE.json');patch=json.loads(payload);assert patch['path']=='src/pages/Contributions.tsx'
 with zipfile.ZipFile(out/'before-install.PRIVATE.zip') as z:original=z.read(patch['path'])
 assert hashlib.sha256(original.decode().rstrip('\n').encode()).hexdigest()==patch['before_sha256']
 patch['before_sha256']=hashlib.sha256(original).hexdigest();payload=json.dumps(patch).encode()
 b.atomic(out/'contributions-intent.json',b.encoded({'path':patch['path'],'before_sha256':patch['before_sha256'],'after_sha256':patch['sha256']}))
 api('start');worker.control('POST','/workspace/quiesce')
 try:
  guest=r'''import pathlib,os,json,sys,hashlib,tty,subprocess
p=pathlib.Path;cfg=json.loads(sys.argv[1]);root=p('/home/sandbox/workspace/app')
assert os.geteuid()==0 and p('/home/sandbox/.runtimed/workspace-quiesced').exists()
tty.setraw(0);print('RUNTIME_READY',flush=True)
raw=sys.stdin.buffer.read(cfg['bytes']);assert hashlib.sha256(raw).hexdigest()==cfg['sha256'];data=json.loads(raw)
target=root/data['path'];assert data['path']=='src/pages/Contributions.tsx' and not target.is_symlink()
assert hashlib.sha256(target.read_bytes()).hexdigest()==data['before_sha256']
assert hashlib.sha256(data['text'].encode()).hexdigest()==data['sha256']
temp=target.with_suffix('.tsx.repair');fd=os.open(temp,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,0o644)
with os.fdopen(fd,'wb') as f:f.write(data['text'].encode());os.fchown(f.fileno(),1000,1000);f.flush();os.fsync(f.fileno())
os.replace(temp,target);fd=os.open(target.parent,os.O_DIRECTORY);os.fsync(fd);os.close(fd)
def owner():os.setgroups([]);os.setgid(1000);os.setuid(1000)
env={'PATH':'/usr/local/bin:/usr/bin:/bin:/home/sandbox/.local/share/pnpm','HOME':'/home/sandbox','NODE_OPTIONS':'--max-old-space-size=384','COREPACK_ENABLE_NETWORK':'0','NO_COLOR':'1'}
check=subprocess.run(['pnpm','exec','tsc','--noEmit','--incremental','false'],cwd=root,env=env,preexec_fn=owner,capture_output=True,text=True,timeout=120)
print('RUNTIME_RECEIPT='+json.dumps({'typescript_exit':check.returncode,'diagnostics':(check.stdout+check.stderr)[-12000:],'model_calls':False}))
'''
  guest='try:\n'+''.join(' '+line+'\n' for line in guest.splitlines())+"except BaseException as error:\n import json,traceback\n print('RUNTIME_RECEIPT='+json.dumps({'error':type(error).__name__,'trace':traceback.format_exc()}),flush=True)\n"
  config=json.dumps({'bytes':len(payload),'sha256':hashlib.sha256(payload).hexdigest()})
  inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-export-recovery-2c7e700');import worker;data=sys.stdin.buffer.read();print(json.dumps(worker.execute("+repr(runtime)+","+repr(guest)+","+repr(config)+",data)))"
  ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
  p=subprocess.run(ssh+['python3 -c '+shlex.quote(inner)],input=payload,capture_output=True,timeout=250)
  b.atomic(out/'contributions-guest.PRIVATE.log',p.stdout+p.stderr);assert p.returncode==0
  result=json.loads(p.stdout);b.atomic(out/'typescript.json',b.encoded(result));assert result['typescript_exit']==0
  worker.http('GET','/export/private-workspace-v2',export=out/'after.PRIVATE.zip')
  before=manifest(out/'before-install.PRIVATE.zip');after=manifest(out/'after.PRIVATE.zip');added=json.loads(b.trusted(out/'repair.PRIVATE.json'))['manifest']['files'];changed=patch['path']
  assert all(after.get(name)==value for name,value in before.items() if name!=changed),'Unexpected original file change'
  assert set(after)-set(before)==set(added)|{'src/views/'},'Unexpected new files'
  assert all(after[name][2]==digest for name,digest in added.items()) and after[changed][2]==patch['sha256']
  b.atomic(out/'files-verified.json',b.encoded({'added_files':added,'changed_files':{changed:patch['sha256']},'other_files_unchanged':True,'typescript_passed':True,'source_backup_preserved':True}))
 finally:worker.control('POST','/workspace/resume')
 first=health();api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;second=health();api('stop')
 result={'restored':True,'sandbox_id':sid,'worker':'vps','profile':'balanced','source_contacted':False,'same_project_identity':True,'wake_seconds':wake,'original_source_verified_before_repair':True,'application_repaired':True,'added_files':4,'changed_files':1,'typescript_passed':True,'module_checks':[first['modules'],second['modules']],'source_retained':True,'at':time.time()}
 b.atomic(job/'complete.json',b.encoded(result));b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
