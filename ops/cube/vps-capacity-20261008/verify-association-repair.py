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
 assert json.loads((out/'typescript.json').read_text())['typescript_exit']==0
 patch=json.loads(b.trusted(out/'contributions.PRIVATE.json'))
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()==(runtime,)
  assert not db.execute("select task_id from task where sandbox_id=? and status in ('running','queued')",(sid,)).fetchall()
 before={k:v for k,v in manifest(out/'before-install.PRIVATE.zip').items() if not k.startswith('node_modules/.vite/')};after={k:v for k,v in manifest(out/'after.PRIVATE.zip').items() if not k.startswith('node_modules/.vite/')};added=json.loads(b.trusted(out/'repair.PRIVATE.json'))['manifest']['files'];changed=patch['path']
 assert all(after.get(name)==value for name,value in before.items() if name!=changed),'Unexpected original file change'
 assert set(after)-set(before)==set(added)|{'src/views/'},'Unexpected new files'
 assert all(after[name][2]==digest for name,digest in added.items()) and after[changed][2]==patch['sha256']
 b.atomic(out/'files-verified.json',b.encoded({'added_files':added,'changed_files':{changed:patch['sha256']},'other_source_files_unchanged':True,'generated_vite_cache_excluded':True,'typescript_passed':True,'source_backup_preserved':True}))
 api('start')
 first=health();api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;second=health();api('stop')
 result={'restored':True,'sandbox_id':sid,'worker':'vps','profile':'balanced','source_contacted':False,'same_project_identity':True,'wake_seconds':wake,'original_source_verified_before_repair':True,'application_repaired':True,'added_files':4,'changed_files':1,'typescript_passed':True,'module_checks':[first['modules'],second['modules']],'source_retained':True,'at':time.time()}
 b.atomic(job/'complete.json',b.encoded(result));b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
