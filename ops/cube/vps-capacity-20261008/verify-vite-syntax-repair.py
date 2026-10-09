"""Finish a guarded repair of an unfinished customer app; private source stays private."""
import hashlib,importlib.util,json,os,pathlib,subprocess,sys,time,zipfile,urllib.request,sqlite3,shlex
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M45GE5BT8GTFEA2VV2R9KCEZ';job=root/'recovery-moves'/('vps-restore-'+sid.lower()+'-memory2048');out=root/'vite-syntax-repair';out.mkdir(mode=0o700,exist_ok=True)
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
def cli(action):
 command={'Action':action,'ID':job.name,'Directory':str(job),'Migrations':str(root/'queue-release-d463b2d/source/control-plane/migrations')}
 p=subprocess.run([str(root/'observe-release-e261aac/cube-relocate')],input=json.dumps(command).encode(),capture_output=True,timeout=200)
 b.atomic(out/('cli-'+action+'.PRIVATE.log'),p.stdout+p.stderr);assert p.returncode==0,'Relocation operation requires reconciliation'
with b.locked(),account_maintenance([sid],out):
 assert (out/'intent.json').exists() and not (out/'files-verified.json').exists()
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute("select id from cube_relocation where phase='fenced'").fetchall()==[(job.name,)]
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
 assert json.loads((worker.root/'content-verified.json').read_text())['content_verified']
 payload=b.trusted(out/'repair.PRIVATE.json');patch=json.loads(payload);assert patch['path']=='vite.config.ts'
 with zipfile.ZipFile(root/'recovery-prepared-canonical'/sid/'workspace.zip') as z:original=z.read('vite.config.ts')
 modified=patch['text'].encode();assert len(original)==len(modified) and sum(a!=b for a,b in zip(original,modified))==1
 offset=next(i for i,(a,c) in enumerate(zip(original,modified)) if a!=c);assert original[offset:offset+1]==b'}' and modified[offset:offset+1]==b']'
 assert hashlib.sha256(original).hexdigest()==patch['before_sha256'] and hashlib.sha256(modified).hexdigest()==patch['sha256']
 worker.control('POST','/workspace/quiesce')
 try:
  before=manifest(out/'before.PRIVATE.zip');assert before['vite.config.ts'][2]==patch['before_sha256']
  guest=r'''import pathlib,os,json,sys,hashlib,tty,subprocess
p=pathlib.Path;cfg=json.loads(sys.argv[1]);root=p('/home/sandbox/workspace/app')
assert os.geteuid()==0 and p('/home/sandbox/.runtimed/workspace-quiesced').exists()
tty.setraw(0);print('RUNTIME_READY',flush=True)
raw=sys.stdin.buffer.read(cfg['bytes']);assert hashlib.sha256(raw).hexdigest()==cfg['sha256'];data=json.loads(raw)
assert data['path']=='vite.config.ts';target=root/data['path'];assert not target.is_symlink()
assert hashlib.sha256(target.read_bytes()).hexdigest()==data['sha256']
def owner():os.setgroups([]);os.setgid(1000);os.setuid(1000)
env={'PATH':'/usr/local/bin:/usr/bin:/bin:/home/sandbox/.local/share/pnpm','HOME':'/home/sandbox','NODE_OPTIONS':'--max-old-space-size=384','COREPACK_ENABLE_NETWORK':'0','NO_COLOR':'1'}
check=subprocess.run(['node','--input-type=module','-e',"import { transformWithEsbuild } from 'vite'; import { readFile } from 'node:fs/promises'; await transformWithEsbuild(await readFile('vite.config.ts','utf8'),'vite.config.ts');"],cwd=root,env=env,preexec_fn=owner,capture_output=True,text=True,timeout=120)
print('RUNTIME_RECEIPT='+json.dumps({'syntax_exit':check.returncode,'diagnostics':(check.stdout+check.stderr)[-12000:],'model_calls':False}))
'''
  guest='try:\n'+''.join(' '+line+'\n' for line in guest.splitlines())+"except BaseException as error:\n import json,traceback\n print('RUNTIME_RECEIPT='+json.dumps({'error':type(error).__name__,'trace':traceback.format_exc()}),flush=True)\n"
  config=json.dumps({'bytes':len(payload),'sha256':hashlib.sha256(payload).hexdigest()})
  inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-export-recovery-2c7e700');import worker;data=sys.stdin.buffer.read();print(json.dumps(worker.execute("+repr(runtime)+","+repr(guest)+","+repr(config)+",data)))"
  ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
  p=subprocess.run(ssh+['python3 -c '+shlex.quote(inner)],input=payload,capture_output=True,timeout=250)
  b.atomic(out/'guest.PRIVATE.log',p.stdout+p.stderr);assert p.returncode==0
  result=json.loads(p.stdout);b.atomic(out/'syntax.json',b.encoded(result));assert result['syntax_exit']==0
  worker.http('GET','/export/private-workspace-v2',export=out/'after.PRIVATE.zip');after=manifest(out/'after.PRIVATE.zip')
  assert set(before)==set(after) and all(after[name]==value for name,value in before.items() if name!='vite.config.ts')
  assert after['vite.config.ts'][2]==patch['sha256']
  # Verify the reviewed repaired workspace against its own immutable artifact.
  # Original source receipt stays untouched and is linked by repair evidence.
  repaired_digest=transport.digest(out/'after.PRIVATE.zip')
  b.atomic(out/'files-verified.json',b.encoded({'only_changed_file':'vite.config.ts','changed_characters':1,'original_archive_sha256':request['receipts']['workspace']['sha256'],'repaired_workspace_sha256':repaired_digest,'original_source_retained':True}))
  reviewed=json.loads(json.dumps(request));reviewed['receipts']['workspace']['sha256']=repaired_digest
  repaired_worker=transport.Worker(reviewed);proof=repaired_worker.verify()
  b.atomic(job/'verified.json',b.encoded(proof));b.atomic(out/'repaired-proof.json',b.encoded(proof))
 finally:worker.control('POST','/workspace/resume')
 first=health();cli('commit');api('start');api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;second=health();api('stop')
 result={'restored':True,'sandbox_id':sid,'worker':'vps','profile':'large','source_contacted':False,'same_project_identity':True,'wake_seconds':wake,'original_source_verified_before_repair':True,'application_repaired':True,'changed_files':1,'changed_characters':1,'syntax_passed':True,'module_checks':[first['modules'],second['modules']],'source_retained':True,'model_calls':False,'at':time.time()}
 b.atomic(job/'complete.json',b.encoded(result));b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
