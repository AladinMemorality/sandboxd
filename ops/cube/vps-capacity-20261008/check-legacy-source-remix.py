"""Exercise source publishing and a disposable remix on old VPS supervisors.

Only the explicitly created private fixture and snapshot are deleted. Original
bindings, source contents, tasks, and running state must remain unchanged.
"""
import hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,sys,time,zipfile
from maintenance_account import account_maintenance
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'legacy-source-remix-67'
sid='01M1HH5DT8FVCP5TNRESEDJBH6';source_app='01M1HH5DJRG3C8HDJ2553XR2S1';key='operator:legacy-source-remix-20261009-67'
def module(name,path):
 spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
b=module('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');copy=module('copy_fleet','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');assets=module('assets',root/'preview-assets.py')
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport

def rows(q,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  db.row_factory=sqlite3.Row;return [dict(r) for r in db.execute(q,args)]
def bindings():return rows('select sandbox_id,runtime_id,template_id from runtime_binding order by sandbox_id')
def api(method,path,body=None,status=200):
 code,result=copy.api(method,path,body)
 if code!=status:
  b.atomic(out/('api-error-'+str(time.time_ns())+'.PRIVATE.json'),b.encoded({'code':code,'response':result}));raise RuntimeError(path+' HTTP '+str(code))
 return result

deadline=time.monotonic()+18000
while True:
 state=subprocess.check_output(['systemctl','show','baarcha-vps-source-data-deploy-72','-p','ActiveState','--value'],text=True).strip()
 assert state!='failed' and time.monotonic()<deadline
 if state=='inactive':break
 time.sleep(5)
assert json.loads((root/'source-data-release-867d7de/deployed.json').read_text())['deployed']
with b.locked():
 out.mkdir(mode=0o700)
 baseline=bindings();assert len(baseline)==136
 assert not rows("select task_id from task where status in ('running','queued')")
 assert not rows('select id from app where external_project_id=?',(key,))
 source=rows('select a.name,a.tags,a.external_user_id,s.status from app a join sandbox s on s.app_id=a.id where a.id=? and s.id=?',(source_app,sid))[0]
 assert source['external_user_id']=='baarcha:1' and 'template' in json.loads(source['tags']) and source['status']=='stopped'
 history=rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,));snap=None;created=None;success=False
 with account_maintenance([sid],out):
  try:
   api('POST','/v1/sandboxes/'+sid+'/start')
   origin,headers=copy.client(sid);assert origin==('127.0.0.1',20080)
   runtime=next(r['runtime_id'] for r in baseline if r['sandbox_id']==sid)
   worker=transport.Worker({'worker':'vps','id':out.name,'sandbox_id':sid,'runtime_id':runtime,'headers':headers,'web_port':3000})
   worker.http('GET','/export/source',export=out/'legacy-export.PRIVATE.zip')
   with zipfile.ZipFile(out/'legacy-export.PRIVATE.zip') as z:assert 'src/data/wedding.ts' not in z.namelist(),'Source was already upgraded; review this test scope'
   b.atomic(out/'snapshot-intent.json',b.encoded({'source_sandbox':sid,'name':key,'at':time.time()}))
   snap=api('POST','/v1/snapshots',{'source_sandbox_id':sid,'name':key},201)['id'];b.atomic(out/'snapshot.json',b.encoded({'id':snap}))
   saved=rows('select image_path,source_sandbox_id,source_app_id,name from snapshot where id=?',(snap,))[0]
   assert saved['source_sandbox_id']==sid and saved['source_app_id']==source_app and saved['name']==key
   with zipfile.ZipFile(saved['image_path']) as z:
    exported=z.read('src/data/wedding.ts');assert hashlib.sha256(exported).hexdigest()=='d450e45fd957b2d4c4ee91b70340c82ebf4e58af6e3c459d1021d232bddc713b'
   b.atomic(out/'fork-intent.json',b.encoded({'external_project_id':key,'snapshot_id':snap,'at':time.time()}))
   fork=api('POST','/v1/apps/'+source_app+'/fork',{'snapshot_id':snap,'name':key,'external_user_id':'operator:source-data-compatibility','external_project_id':key},201)
   created=fork['app']['id'];b.atomic(out/'created.PRIVATE.json',b.encoded(fork));assert 'sandbox_error' not in fork,'Inspect retained fixture creation error'
   fixture=rows('select s.id,b.runtime_id,a.worker_id,s.web_port from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.app_id=?',(created,));assert len(fixture)==1 and fixture[0]['worker_id']=='vps';fixture=fixture[0]
   child=fixture['id'];origin,h=copy.client(child);assert origin==('127.0.0.1',20080)
   cw=transport.Worker({'worker':'vps','id':out.name+'-fixture','sandbox_id':child,'runtime_id':fixture['runtime_id'],'headers':h,'web_port':fixture['web_port'] or 3000})
   data=cw.http('GET','/files/content?path=src%2Fdata%2Fwedding.ts');assert data==exported
   web={**h,'Host':h['Host'].replace('3031-',str(fixture['web_port'] or 3000)+'-',1)}
   ready=time.monotonic()+120
   while True:
    code,_=copy.request(*origin,'/',headers=web,timeout=10)
    if code==200:break
    assert code in (502,503) and time.monotonic()<ready;time.sleep(1)
   base,html=assets.page(cw.origin,web);static=base.rsplit('/',1)[0]+'/';queue=assets.entries(html,base,static);seen=set();total=0
   while queue:
    path=queue.pop(0)
    if path in seen:continue
    seen.add(path);assert len(seen)<=512
    data=cw.http('GET',path,headers=web,timeout=45);total+=len(data);assert total<=64*1024**2
    queue.extend(p for p in assets.imports(path,data,static) if p not in seen)
   cw.http('GET','/export/source',export=out/'legacy-fixture-export.PRIVATE.zip')
   with zipfile.ZipFile(out/'legacy-fixture-export.PRIVATE.zip') as z:assert 'src/data/wedding.ts' not in z.namelist(),'Destination was upgraded; legacy import behavior not proved'
   assert not cw.control('GET','/status')['active_task']
   result={'passed':True,'source_supervisor_legacy_filter':True,'destination_supervisor_legacy_filter':True,'controller_export_preserved_module':True,'controller_import_restored_module':True,'module_checks':len(seen),'module_bytes':total,'private_fixture_app':created,'b200_contacted':False,'model_calls':False,'at':time.time()};success=True
  finally:
   # Discover only this operation's uniquely named fixture if a response was lost.
   own=rows('select id,name,external_user_id from app where external_project_id=?',(key,))
   assert len(own)<=1
   for app in own:
    assert app['name']==key and app['external_user_id']=='operator:source-data-compatibility'
    assert not rows('select task_id from task where sandbox_id in (select id from sandbox where app_id=?)',(app['id'],))
    api('DELETE','/v1/apps/'+app['id'],status=204)
   snapshots=rows('select id,source_app_id,source_sandbox_id from snapshot where name=?',(key,))
   for snapshot in snapshots:
    assert snapshot['source_app_id']==source_app and snapshot['source_sandbox_id']==sid
    api('DELETE','/v1/snapshots/'+snapshot['id'],status=204)
   assert rows('select task_id,status from task where sandbox_id=? order by task_id',(sid,))==history,'User task changed; preserve source runtime'
   api('POST','/v1/sandboxes/'+sid+'/stop')
  assert success and bindings()==baseline
  b.atomic(out/'cleanup.json',b.encoded({'complete':True,'original_bindings_preserved':True,'temporary_app_deleted':True,'temporary_snapshot_deleted':True,'source_stopped':True}))
 b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
