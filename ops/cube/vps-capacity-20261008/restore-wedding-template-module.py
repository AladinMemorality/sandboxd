"""Restore one missing template module only after matching the entire saved source.

No tenant-specific source is copied: the donor is the platform's tagged starter.
The retained backup and live files must match it before this one-file repair.
"""
import hashlib,importlib.util,json,os,pathlib,shlex,sqlite3,subprocess,sys,tarfile,time,zipfile
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
out=root/'wedding-template-module-repair-37'
sid='01M4DWDR4TQ4JRBTTTJMG5SB78';runtime='ce648b2c8a804ad2a0e989d61f0d351b'
donor='01M1HH5DT8FVCP5TNRESEDJBH6';backup=P('/var/backups/baarcha-vps-source/20261009T162045Z/sandboxes')
def module(name,path):
 spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
b=module('boot','/usr/local/libexec/baarcha-cube-boot-transition.py')
copy=module('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py')
assets=module('assets',root/'preview-assets.py')
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def rows(q,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(q,args)]
def saved(which):
 with tarfile.open(backup/which/'home.tar.gz') as t:
  return {m.name.removeprefix('./workspace/app/'):t.extractfile(m).read() for m in t if m.isfile() and (m.name.startswith('./workspace/app/src/') or m.name in ['./workspace/app/package.json','./workspace/app/BRAIN.md','./workspace/app/AGENTS.md'])}
def sha(data):return hashlib.sha256(data).hexdigest()
def manifest(path):
 with zipfile.ZipFile(path) as z:return {e.filename:(e.external_attr,e.file_size,sha(z.read(e.filename))) for e in z.infolist() if not e.filename.startswith('node_modules/.vite/')}
def api(action):
 code,body=copy.api('POST','/v1/sandboxes/'+sid+'/'+action)
 if code!=200:
  b.atomic(out/(action+'-error.PRIVATE.json'),b.encoded(body));raise RuntimeError(action+' HTTP '+str(code))
with b.locked():
 assert not out.exists();out.mkdir(mode=0o700)
 assert rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[{'runtime_id':runtime}]
 assert rows('select status from sandbox where id=?',(sid,))==[{'status':'stopped'}]
 assert not rows("select task_id from task where status in ('running','queued')")
 history=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,));assert not history
 source_app=rows('select a.external_user_id,a.tags from app a join sandbox s on s.app_id=a.id where s.id=?',(donor,))[0]
 assert source_app['external_user_id']=='baarcha:1' and 'template' in json.loads(source_app['tags'])
 source=saved(donor);target=saved(sid);path='src/data/wedding.ts'
 checks={n:sha(v) for n,v in target.items() if n.startswith('src/') or n=='package.json'}
 assert len(checks)==76 and all(source[n]==target[n] for n in checks) and path not in target
 content=source[path];assert len(content)==9647
 note='\n- 2026-10-09: restored missing `src/data/wedding.ts` from the platform wedding starter after all 76 source/package files matched. The app now centralizes Lina & Sami content there. Verify: `pnpm exec tsc --noEmit` and `test -f src/data/wedding.ts`.\n'
 brain=target['BRAIN.md'].decode().rstrip()+'\n'+note
 assert len(brain.splitlines())<=30
 payload=json.dumps({'checks':checks,'data':content.decode(),'data_sha256':sha(content),'brain_before':sha(target['BRAIN.md']),'brain':brain,'agents_sha256':sha(target['AGENTS.md'])}).encode()
 b.atomic(out/'repair.PRIVATE.json',payload)
 b.atomic(out/'provenance.json',b.encoded({'sandbox_id':sid,'runtime_id':runtime,'source_template_sandbox':donor,'matched_source_files':len(checks),'restored_path':path,'restored_sha256':sha(content),'invented_content':False,'model_calls':False,'at':time.time()}))
 started=False;quiesced=False
 with account_maintenance([sid],out):
  try:
   started=True;api('start');origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
   worker=transport.Worker({'worker':'vps','id':out.name,'sandbox_id':sid,'runtime_id':runtime,'headers':headers,'web_port':3000})
   status=worker.control('GET','/status');assert not status['active_task']
   web={**headers,'Host':headers['Host'].replace('3031-','3000-',1)}
   code,body=copy.request(*origin,'/src/pages/Home.tsx',headers=web);assert code==500 and b'@/data/wedding' in body
   b.atomic(out/'before-error.PRIVATE.txt',body)
   quiesced=True;worker.control('POST','/workspace/quiesce')
   worker.http('GET','/export/private-workspace-v2',export=out/'before.PRIVATE.zip');before=manifest(out/'before.PRIVATE.zip')
   guest=r'''import pathlib,os,json,sys,hashlib,tty,subprocess
p=pathlib.Path;cfg=json.loads(sys.argv[1]);root=p('/home/sandbox/workspace/app')
assert os.geteuid()==0 and p('/home/sandbox/.runtimed/workspace-quiesced').exists()
tty.setraw(0);print('RUNTIME_READY',flush=True)
raw=sys.stdin.buffer.read(cfg['bytes']);assert hashlib.sha256(raw).hexdigest()==cfg['sha256'];data=json.loads(raw)
sha=lambda v:hashlib.sha256(v).hexdigest()
for name,digest in data['checks'].items():
 target=root/name;assert not target.is_symlink() and sha(target.read_bytes())==digest,name
assert sha((root/'AGENTS.md').read_bytes())==data['agents_sha256']
assert sha((root/'BRAIN.md').read_bytes())==data['brain_before']
errors=(root/'.runtime-errors.log').read_text()[-16000:] if (root/'.runtime-errors.log').exists() else None
print('RUNTIME_DIAGNOSTIC='+json.dumps({'runtime_errors_before':errors}),flush=True)
target=root/'src/data/wedding.ts';assert not target.exists() and not target.is_symlink()
assert sha(data['data'].encode())==data['data_sha256']
directory=target.parent
assert not directory.is_symlink()
if not directory.exists():directory.mkdir(mode=0o755);os.chown(directory,1000,1000)
for target,text in [(target,data['data']),(root/'BRAIN.md',data['brain'])]:
 temp=target.with_name(target.name+'.restore');fd=os.open(temp,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,0o644)
 with os.fdopen(fd,'wb') as f:f.write(text.encode());os.fchown(f.fileno(),1000,1000);f.flush();os.fsync(f.fileno())
 os.replace(temp,target);fd=os.open(target.parent,os.O_DIRECTORY);os.fsync(fd);os.close(fd)
def owner():os.setgroups([]);os.setgid(1000);os.setuid(1000)
env={'PATH':'/usr/local/bin:/usr/bin:/bin:/home/sandbox/.local/share/pnpm','HOME':'/home/sandbox','NODE_OPTIONS':'--max-old-space-size=512','COREPACK_ENABLE_NETWORK':'0','NO_COLOR':'1'}
results=[]
for args in [['pnpm','exec','tsc','--noEmit'],['pnpm','exec','tsc','--noEmit','-p','tsconfig.app.json']]:
 c=subprocess.run(args,cwd=root,env=env,preexec_fn=owner,capture_output=True,text=True,timeout=120)
 results.append({'command':args,'exit':c.returncode,'diagnostics':(c.stdout+c.stderr)[-16000:]})
print('RUNTIME_RECEIPT='+json.dumps({'checks':results,'sha256':sha((root/'src/data/wedding.ts').read_bytes()),'model_calls':False}),flush=True)
'''
   guest='try:\n'+''.join(' '+line+'\n' for line in guest.splitlines())+"except BaseException as error:\n import json,traceback\n print('RUNTIME_RECEIPT='+json.dumps({'error':type(error).__name__,'trace':traceback.format_exc()}),flush=True)\n"
   config=json.dumps({'bytes':len(payload),'sha256':sha(payload)})
   inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-export-recovery-2c7e700');import worker;data=sys.stdin.buffer.read();print(json.dumps(worker.execute("+repr(runtime)+","+repr(guest)+","+repr(config)+",data)))"
   proc=subprocess.run(ssh+['python3 -c '+shlex.quote(inner)],input=payload,capture_output=True,timeout=400)
   b.atomic(out/'guest.PRIVATE.log',proc.stdout+proc.stderr);assert proc.returncode==0
   result=json.loads(proc.stdout);assert result.get('sha256')==sha(content) and all(c['exit']==0 for c in result['checks']), 'Inspect private TypeScript diagnostics'
   b.atomic(out/'types.json',b.encoded(result))
   worker.http('GET','/export/private-workspace-v2',export=out/'after.PRIVATE.zip');after=manifest(out/'after.PRIVATE.zip')
   assert set(after)-set(before)=={'src/data/',path} and not set(before)-set(after)
   assert all(after[n]==v for n,v in before.items() if n!='BRAIN.md')
   assert after[path][2]==sha(content) and after['BRAIN.md'][2]==sha(brain.encode())
   worker.control('POST','/workspace/resume');quiesced=False
   def health():
    base,html=assets.page(worker.origin,web);static=base.rsplit('/',1)[0]+'/';queue=assets.entries(html,base,static);seen=set();total=0
    while queue:
     name=queue.pop(0)
     if name in seen:continue
     seen.add(name);assert len(seen)<=512
     data=worker.http('GET',name,headers=web,timeout=45);total+=len(data);assert total<=64*1024**2
     queue.extend(p for p in assets.imports(name,data,static) if p not in seen)
    memory=assets.guest_memory(runtime);assert memory['oom_kill']==0
    return {'modules':len(seen),'bytes':total,'guest_memory':memory,'new_oom_kills':0}
   first=health();api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;second=health()
   assert rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==history
   expected=json.loads((root/'supervisor-canary-2c7e700/passed.json').read_text())['receipt']['sha256']
   update=subprocess.run(ssh+['python3','/opt/baarcha-vps-export-recovery-2c7e700/worker.py','--container',runtime],capture_output=True,timeout=460)
   b.atomic(out/'supervisor.PRIVATE.log',update.stdout+update.stderr);assert update.returncode==0
   receipt=json.loads(update.stdout);assert receipt['sha256']==expected and receipt['status']=='current'
  finally:
   if quiesced:worker.control('POST','/workspace/resume')
   if started and rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==history:api('stop')
  assert rows('select s.status,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(sid,))==[{'status':'stopped','state':'released','charged':0}]
 result={'sandbox_id':sid,'runtime_id':runtime,'verification':'template-module-repair-full-wake-and-modules','wake_seconds':wake,'original_running_preserved':True,'supervisor_sha256':expected,**first}
 case=root/'fleet-wake-validation-01'/(sid+'-'+runtime);assert (case/'failed.json').exists() and not (case/'passed.json').exists()
 b.atomic(out/'complete.json',b.encoded({'complete':True,'result':result,'module_checks':[first['modules'],second['modules']],'changed_files':['src/data/wedding.ts','BRAIN.md'],'b200_contacted':False,'model_calls':False,'at':time.time()}))
 b.atomic(case/'source-repair.json',b.encoded({'proof':str(out/'complete.json'),'original_failure_retained':True}))
 b.atomic(case/'passed.json',b.encoded({'tasks':history,'result':result}))
 print(json.dumps({'complete':True,'modules':first['modules'],'wake_seconds':wake}),flush=True)
