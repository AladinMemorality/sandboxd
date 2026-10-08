"""Restore one stopped B200 project from VPS-local verified artifacts only."""
import contextlib,fcntl,importlib.util,json,os,pathlib,select,shutil,sqlite3,subprocess,sys,time,traceback
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');os.umask(0o077)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sid=sys.argv[1];assert len(sid)==26 and sid.isalnum()
prepared=root/'recovery-prepared-canonical'/sid;source=json.loads((prepared/'export-result.PRIVATE.json').read_text())
subprocess.run([str(root/'artifact-validator'),str(prepared)],check=True,capture_output=True,timeout=180)
job=root/'recovery-moves'/('vps-restore-'+sid.lower());job.mkdir(mode=0o700,parents=True,exist_ok=False)
BIN=root/'cube-relocate-package-recovery';migrations=root/'queue-release-d463b2d/source/control-plane/migrations'
def save(name,value):b.atomic(job/name,b.encoded(value))
def rows(query,args=()):
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=10)) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(query,args)]
def cli(action,**extra):
 request={'Action':action,'ID':job.name,'Directory':str(job),'Migrations':str(migrations),**extra}
 p=subprocess.run([str(BIN)],input=json.dumps(request).encode(),capture_output=True,timeout=200)
 if p.returncode:save('cli-'+action+'-failed.json',{'exit_code':p.returncode});raise RuntimeError('relocation '+action+' refused')
 return json.loads(p.stdout)
class LocalWorker(transport.Worker):
 def fetch(self,role,receipt):
  path=P(receipt['local_path']);assert path==prepared/(role+'.zip') and not path.is_symlink()
  assert path.stat().st_size==receipt['archive_bytes'] and transport.digest(path)==receipt['sha256'];return path
with b.locked():
 try:
  row=rows("select s.status,s.web_port,b.runtime_id,b.config_revision,a.worker_id,p.charged,p.state from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission p on p.runtime_id=b.runtime_id join cube_admission a on a.admission_key=p.admission_key where s.id=? and p.state<>'deleted'",(sid,))[0]
  assert row['status']=='stopped' and row['runtime_id']==source['runtime_id'] and row['worker_id']=='b200-01' and row['charged']==0 and row['state']=='released'
  tasks=[r['task_id'] for r in rows('select task_id from task where sandbox_id=? order by task_id',(sid,))];assert tasks==source['task_ids']
  assert not rows("select id from cube_relocation where phase='fenced'")
  save('export-result.PRIVATE.json',source)
  save('scope.json',{'sandbox_id':sid,'source_worker':'b200-01','target_worker':'vps','source_archive_sha256':source['source_archive_sha256'],'source_contacted':False,'at':time.time()})
  cli('fence',SandboxID=sid,ExpectedRuntime=source['runtime_id'],TargetWorker='vps',TargetTemplate='tpl-78e4edb3d629465e9d8372c1')
  cli('create')
  target=json.loads((job/'target.PRIVATE.json').read_text());runtime=target['Runtime']['sandboxID'] if 'sandboxID' in target['Runtime'] else target['Runtime'].get('sandbox_id')
  assert runtime,'target provider identity missing'
  request={'worker':'vps','id':job.name,'sandbox_id':sid,'runtime_id':runtime,'headers':{'Host':'3031-'+runtime+'.'+target['Relocation']['Domain'],'Authorization':'Bearer '+target['supervisor_token'],'cube-traffic-access-token':target['traffic_access_token']},'source':source,'receipts':source['artifacts'],'env':target['Env'],'config_revision':row['config_revision'],'web_port':row['web_port'] or 3000}
  save('worker-job.PRIVATE.json',request)
  save('channel-request.PRIVATE.json',{'Action':'channel','ID':job.name,'Directory':str(job),'Migrations':str(migrations),'PackageDownloads':True})
  with (job/'channel-error.log').open('ab') as error:
   channel=subprocess.Popen([str(BIN),str(job/'channel-request.PRIVATE.json')],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=error)
   try:
    assert select.select([channel.stdout],[],[],60)[0]
    channel_ready=json.loads(channel.stdout.readline());assert channel_ready.get('channel_ready') and channel_ready['outbound']=='npm-registry-only'
    worker=LocalWorker(request);proof=worker.restore();save('verified.json',proof)
   finally:
    channel.stdin.close();channel.wait(timeout=30)
  cli('commit')
  env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
  import urllib.request
  def api(action):
   req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
   with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
  api('start');api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began
  worker.http('GET','/',headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)},timeout=10)
  api('stop')
  assert rows("select worker_id,state,charged from cube_admission where runtime_id=?",(runtime,))==[{'worker_id':'vps','state':'released','charged':0}]
  result={'restored':True,'sandbox_id':sid,'worker':'vps','source_contacted':False,'same_project_identity':True,'wake_seconds':wake,'all_content_verified':True,'source_retained':True,'at':time.time()};save('complete.json',result);print(json.dumps(result),flush=True)
 except BaseException as error:
  save('failed.json',{'error':type(error).__name__,'reason':str(error)[:512],'line':traceback.extract_tb(error.__traceback__)[-1].lineno,'at':time.time(),'source_retained':True});raise
