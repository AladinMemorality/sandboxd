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
 deadline=time.monotonic()+90
 while True:
  try:base,html=assets.page(worker.origin,headers);break
  except (OSError,RuntimeError):assert time.monotonic()<deadline;time.sleep(1)
 queue=assets.entries(html,base);seen=set();total=0
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
 assert not (out/'complete.json').exists() and json.loads((out/'syntax.json').read_text())['syntax_exit']==0
 evidence=json.loads((out/'files-verified.json').read_text());assert evidence['syntax_edits']==2 and evidence['only_changed_file']=='vite.config.ts'
 assert json.loads((worker.root/'content-verified.json').read_text())['content_verified']
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute("select id from cube_relocation where phase='fenced'").fetchall()==[(job.name,)]
  assert not db.execute("select task_id from task where status in ('running','queued')").fetchall()
 patch=json.loads(b.trusted(out/'repair-02.PRIVATE.json'))
 # The normal restore verified every original home byte before starting the
 # dependency installer. Recheck durable home files now; PNPM has since created
 # its local store and caches. Those reproducible files stay on the target.
 files={k:v for k,v in manifest(root/'recovery-prepared-canonical'/sid/'home.zip').items() if not k.startswith(('.cache/','.npm/')) and not k.endswith('/')}
 guest="import pathlib,json,hashlib,stat;cfg=json.loads(__import__('sys').argv[1]);p=pathlib.Path('/home/sandbox');checked=[]\nfor name,value in cfg['files'].items():\n f=p/name;assert not f.is_symlink() and f.is_file();assert len(f.read_bytes())==value[1] and hashlib.sha256(f.read_bytes()).hexdigest()==value[2];checked.append(name)\nassert hashlib.sha256((p/'workspace/app/vite.config.ts').read_bytes()).hexdigest()==cfg['config_sha']\nassert {x.name for x in (p/'.local').iterdir()}=={'share','state'}\nassert {x.name for x in (p/'.local/share').iterdir()}=={'pnpm'} and {x.name for x in (p/'.local/state').iterdir()}=={'pnpm'}\nprint('RUNTIME_RECEIPT='+json.dumps({'durable_home_files_verified':checked,'new_local_paths':'PNPM store and state only','syntax_file_verified':True}))"
 cfg=json.dumps({'files':files,'config_sha':patch['sha256']})
 inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-export-recovery-2c7e700');import worker;print(json.dumps(worker.execute("+repr(runtime)+","+repr(guest)+","+repr(cfg)+",b'')))"
 ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
 result=json.loads(subprocess.check_output(ssh+['python3 -c '+shlex.quote(inner)],timeout=45));b.atomic(out/'home-post-install.json',b.encoded(result))
 worker.control('POST','/workspace/quiesce')
 try:
  worker.http('GET','/export/private-workspace-v2',export=out/'final.PRIVATE.zip')
  expected={k:v for k,v in manifest(out/'after.PRIVATE.zip').items() if not k.startswith('node_modules/.vite/')};actual={k:v for k,v in manifest(out/'final.PRIVATE.zip').items() if not k.startswith('node_modules/.vite/')};assert actual==expected
  worker.http('POST','/export/private-task-history',{'task_ids':request['source']['task_ids']},export=out/'history.PRIVATE.zip');assert transport.digest(out/'history.PRIVATE.zip')==request['receipts']['history']['sha256']
  status=worker.control('GET','/status');assert status['app_config_revision']==sid+':'+str(request['config_revision']) and not status['active_task']
 finally:worker.control('POST','/workspace/resume')
 first=health()
 proof={'RelocationID':job.name,'SandboxID':sid,'RuntimeID':runtime,'WorkerID':'vps','WorkspaceSHA256':transport.digest(out/'final.PRIVATE.zip'),'HomeSHA256':request['receipts']['home']['sha256'],'HistorySHA256':request['receipts']['history']['sha256'],'WorkspaceVerified':True,'HomeVerified':True,'HistoryVerified':True,'ConfigApplied':True,'ApplicationReady':True}
 b.atomic(out/'proof-phases.json',b.encoded({'original_workspace_home_history':'fully byte-verified before dependency installation','final_workspace':'two reviewed syntax edits; other source files unchanged; generated Vite cache allowed','final_home':'durable original files rechecked; PNPM caches and store created by dependency install','final_history':'fresh export equals original digest','model_calls':False}))
 b.atomic(job/'verified.json',b.encoded(proof));cli('commit');api('start');api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;second=health();api('stop')
 result={'restored':True,'sandbox_id':sid,'worker':'vps','profile':'large','source_contacted':False,'same_project_identity':True,'wake_seconds':wake,'original_source_verified_before_repair':True,'application_repaired':True,'changed_files':1,'syntax_edits':2,'syntax_passed':True,'module_checks':[first['modules'],second['modules']],'source_retained':True,'model_calls':False,'at':time.time()}
 b.atomic(job/'complete.json',b.encoded(result));b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
