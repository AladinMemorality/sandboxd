#!/usr/bin/env python3
"""Load the owned private copies through the real controller, keeping previews warm."""
import importlib.util,threading,time,json,os,sqlite3,hashlib,http.client,concurrent.futures,subprocess,copy,signal
from pathlib import Path
from urllib.parse import urlsplit
from html.parser import HTMLParser
s=importlib.util.spec_from_file_location('copy_fleet',Path(__file__).resolve().parent.parent/'copy-fleet.py');c=importlib.util.module_from_spec(s);s.loader.exec_module(c)
ROOT=c.ROOT;PLAN=json.loads((ROOT/'plan.PRIVATE.json').read_text());GUESTS=ROOT/'guests';GUESTS.mkdir(mode=0o700,exist_ok=True)
create_lock=threading.Lock();ready_lock=threading.Lock();ready=[];stop=threading.Event()
def event(phase,**kwargs):
 value=dict(at=time.time(),phase=phase,**kwargs);c.save(ROOT/'load-current.json',value);print(json.dumps(value),flush=True)
def scoped(app):
 status,value=c.api('GET','/v1/apps/'+app['id']);assert status==200 and value['external_user_id']=='operator:fleet-100' and value['external_project_id']==app['external_project_id']
def preview(row):
 status,v=c.api('POST','/v1/sandboxes/'+row['sandbox_id']+'/preview-access');assert status==200
 before=time.monotonic();status,data=c.request('127.0.0.1',9090,'/',headers={'Host':urlsplit(v['url']).netloc,'Cookie':'sandbox_preview='+v['token']},timeout=90)
 assets=[]
 if status==200:
  class Assets(HTMLParser):
   def handle_starttag(self,tag,attrs):
    a=dict(attrs);url=a.get('src') if tag=='script' else a.get('href') if tag=='link' and a.get('rel')=='stylesheet' else None
    if url and url.startswith('/') and not url.startswith('//') and len(assets)<4:assets.append(url)
  Assets().feed(data.decode('utf-8',errors='replace'))
 observed=[]
 for asset in assets:
  code,body=c.request('127.0.0.1',9090,asset,headers={'Host':urlsplit(v['url']).netloc,'Cookie':'sandbox_preview='+v['token']},timeout=90)
  observed.append(dict(status=code,bytes=len(body)))
 return dict(sandbox_id=row['sandbox_id'],status=status,seconds=time.monotonic()-before,bytes=len(data),body_sha256=hashlib.sha256(data).hexdigest(),assets=observed)
def traffic():
 while not stop.wait(15):
  with ready_lock:current=list(ready)
  with concurrent.futures.ThreadPoolExecutor(max_workers=50) as pool:
   futures=[pool.submit(preview,r) for r in current]
   failures=0
   for f in futures:
    try:f.result()
    except Exception:failures+=1
  c.save(ROOT/'traffic-current.json',dict(at=time.time(),guests=len(current),request_failures=failures))
def clone(app):
 scoped(app);out=GUESTS/app['id'];out.mkdir(mode=0o700,exist_ok=True)
 if (out/'complete.json').exists():
  row=json.loads((out/'complete.json').read_text())
  status,_=c.api('POST','/v1/sandboxes/'+row['sandbox_id']+'/start');assert status==200
  with ready_lock:ready.append(row)
  return row
 source=app['source'];preset=source['runtime_preset'] if source else 'react-vite'
 if source:
  archive=ROOT/'sources'/source['sandbox_id']/'workspace.zip';proof=json.loads(archive.with_name('complete.json').read_text())
  assert c.manifest(archive)['sha256']==proof['sha256']
  # Copy runtime-visible application configuration only; control-plane and agent grants are excluded.
  configs=c.rows("select * from app_config where app_id=? and access_policy in ('both','runtime_access')",(source['app_id'],))
  for config in configs:
   if config['key'].upper().startswith(('RUNTIMED_','SANDBOXD_','STUDIO_WORKER_')) or config['key'].upper()=='BRIDGE_TOKEN':continue
   status,value=c.api('POST','/v1/apps/'+source['app_id']+'/config/'+config['key']+'/reveal')
   assert status==200 and isinstance(value.get('value'),str),'source runtime config unavailable'
   status,_=c.api('POST','/v1/apps/'+app['id']+'/config',dict(key=config['key'],value=value['value'],sensitive=bool(config['sensitive']),access_policy='runtime_access'))
   assert status in (201,409),'clone runtime config rejected'
 with create_lock:
  c.save(out/'intent.json',dict(app_id=app['id'],preset=preset));before=time.monotonic()
  status,sandbox=c.api('POST','/v1/apps/'+app['id']+'/sandbox',dict(runtime_preset=preset,ports=[source['web_port'] or 3000] if source else [3000]))
  if status!=201:
   c.save(out/'create-failure.PRIVATE.json',dict(status=status,response=sandbox));raise RuntimeError('copy creation rejected')
  row=dict(app_id=app['id'],sandbox_id=sandbox['id'],source_sandbox_id=source['sandbox_id'] if source else None,create_seconds=time.monotonic()-before)
  c.save(out/'created.json',row)
  with ready_lock:ready.append(row)
 if source:
  before=time.monotonic();status,raw=c.guest(row['sandbox_id'],'GET','/status');assert status==200
  boot=json.loads(raw)['runtimed']['booted_at']
  status,_=c.guest(row['sandbox_id'],'POST','/workspace/quiesce');assert status==200
  origin,headers=c.client(row['sandbox_id']);headers.update({'Content-Type':'application/zip','Content-Length':str(archive.stat().st_size)})
  connection=http.client.HTTPConnection(*origin,timeout=1800)
  try:
   with archive.open('rb') as body:connection.request('PUT','/import/private-workspace-v2',body,headers)
   response=connection.getresponse();raw=response.read(8192);assert response.status==200,'copy import rejected'
  finally:connection.close()
  until=time.monotonic()+60
  while True:
   try:
    status,raw=c.guest(row['sandbox_id'],'GET','/status')
    if status==200 and json.loads(raw)['runtimed']['booted_at']!=boot:break
   except Exception:pass
   assert time.monotonic()<until,'copy supervisor failed to restart';time.sleep(.5)
  status,_=c.guest(row['sandbox_id'],'POST','/workspace/quiesce');assert status==200
  origin,headers=c.client(row['sandbox_id']);connection=http.client.HTTPConnection(*origin,timeout=1800)
  try:
   connection.request('GET','/export/private-workspace-v2',headers=headers);response=connection.getresponse();assert response.status==200
   size=0
   with (out/'verification.zip').open('wb') as body:
    while data:=response.read(1024*1024):
     size+=len(data);assert size<=4*1024**3;body.write(data)
  finally:connection.close()
  actual=c.manifest(out/'verification.zip');assert actual['sha256']==proof['sha256'],'copy file content/mode verification failed'
  (out/'verification.zip').unlink()
  row.update(copy_seconds=time.monotonic()-before,workspace=proof,workspace_verified=True)
  status,_=c.guest(row['sandbox_id'],'POST','/workspace/resume');assert status==200
 else:
  status,_=c.request('127.0.0.1',9090,'/v1/sandboxes/'+row['sandbox_id']+'/files?path=index.html','PUT','<html><body>'+PLAN['prefix']+' '+app['id']+'</body></html>',{'Authorization':'Bearer '+c.TOKEN,'Content-Type':'text/plain'});assert status==200
 until=time.monotonic()+90
 while True:
  observed=preview(row)
  if observed['status']==200:break
  if time.monotonic()>=until:break
  time.sleep(2)
 row['preview']=observed
 status,raw=c.guest(row['sandbox_id'],'GET','/status');assert status==200;value=json.loads(raw)
 row['processes']=value.get('processes',[]);row['supervisor_preview']=value.get('preview',{})
 # Preview authentication must remain enforced even for copies of public apps.
 status,access=c.api('POST','/v1/sandboxes/'+row['sandbox_id']+'/preview-access');assert status==200
 status,_=c.request('127.0.0.1',9090,'/',headers={'Host':urlsplit(access['url']).netloc});assert status in (401,403),'copy preview was not private'
 row['unsigned_preview_status']=status
 placement=c.rows('select a.worker_id,b.runtime_id from cube_admission a join runtime_binding b on b.runtime_id=a.runtime_id where b.sandbox_id=?',(row['sandbox_id'],))[0];row.update(placement)
 c.save(out/'complete.json',row);event('copy-ready',app_id=app['id'],worker=placement['worker_id'],http_status=observed['status'])
 return row
def run_b200():
 env=c.environment();fleet=json.loads(env['SANDBOXD_CUBE_FLEET'])
 assert [(w['id'],w['admission']['max_active'],w['draining']) for w in fleet['workers']]==[('b200-01',96,False),('vps',4,False)]
 assert set(json.loads(env['SANDBOXD_CUBE_OFFLINE_APPS']))=={a['id'] for a in PLAN['apps']}
 worker=threading.Thread(target=traffic,daemon=True);worker.start()
 failed=[]
 try:
  # First 70 customer projects plus 26 fillers; four real projects go on VPS at peak.
  with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
   futures={pool.submit(clone,app):app['id'] for app in PLAN['apps'][4:]}
   for future in concurrent.futures.as_completed(futures):
    try:
     row=future.result();assert row['worker_id']=='b200-01','copy did not land on B200'
    except Exception as error:
     failed.append(futures[future]);c.save(GUESTS/futures[future]/'failure.json',dict(error=str(error)));event('copy-failed',app_id=futures[future])
  assert not failed,'some copies require review'
  event('b200-ready',copies=96)
  peak()
 finally:
  cleanup()
  stop.set();worker.join(100)

def native_proof():
 current=list(ready)
 assert len(current)==100 and len({r['sandbox_id'] for r in current})==100
 def inspect(row):
  status,raw=c.request('127.0.0.1',20300,'/sandboxes/'+row['runtime_id'],headers={'X-API-Key':c.ENV['SANDBOXD_CUBE_API_KEY']})
  value=json.loads(raw);assert status==200 and value['state']=='running' and value['cpuCount']==2 and value['memoryMB']==2048
  detail=release.f.native(18089,'/cube/sandbox/info?sandbox_id='+row['runtime_id']+'&instance_type=cubebox')
  assert detail['ret']['ret_code']==200 and len(detail['data'])==1 and detail['data'][0]['host_id']=={'vps':'10.0.2.15','b200-01':'10.254.240.2'}[row['worker_id']]
  return dict(sandbox_id=row['sandbox_id'],worker=row['worker_id'],running=True)
 with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:proof=list(pool.map(inspect,current))
 charges=c.rows('select worker_id,sum(charged) count from cube_admission group by worker_id order by worker_id')
 assert charges==[dict(worker_id='b200-01',count=96),dict(worker_id='vps',count=4)]
 return proof
def remove(app):
 scoped(app)
 with ready_lock:ready[:]=[r for r in ready if r['app_id']!=app['id']]
 created=GUESTS/app['id']/'created.json'
 runtime=None
 if created.exists():
  sid=json.loads(created.read_text())['sandbox_id']
  binding=c.rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))
  if binding:runtime=binding[0]['runtime_id']
 status,_=c.api('DELETE','/v1/apps/'+app['id']);assert status==204,'owned app deletion failed'
 status,_=c.api('GET','/v1/apps/'+app['id']);assert status==404
 if runtime:
  status,_=c.request('127.0.0.1',20300,'/sandboxes/'+runtime,headers={'X-API-Key':c.ENV['SANDBOXD_CUBE_API_KEY']})
  assert status==404,'native deletion not proved'
 c.save(GUESTS/app['id']/'deleted.json',dict(app_id=app['id'],native_absence_verified=bool(runtime)))
def original_bindings():
 actual=c.rows("select s.id sandbox_id,s.app_id,b.runtime_id,b.template_id from sandbox s join runtime_binding b on b.sandbox_id=s.id where s.id in ("+','.join('?' for _ in PLAN['sources'])+") order by s.id",tuple(r['sandbox_id'] for r in PLAN['sources']))
 expected=sorted([{k:r[k] for k in ('sandbox_id','app_id','runtime_id','template_id')} for r in PLAN['sources']],key=lambda r:r['sandbox_id'])
 assert actual==expected,'customer bindings changed'
def cleanup():
 event('cleanup-started')
 failures=[]
 for app in reversed(PLAN['apps']):
  out=GUESTS/app['id'];out.mkdir(mode=0o700,exist_ok=True)
  if (out/'deleted.json').exists():continue
  try:remove(app)
  except Exception as error:
   failures.append(app['id']);c.save(out/'cleanup-failure.json',dict(error=str(error)))
 original_bindings()
 remaining=c.rows('select id from sandbox order by id')
 result=dict(failures=failures,cleanup_verified=not failures and [r['id'] for r in remaining]==sorted(r['sandbox_id'] for r in PLAN['sources']))
 c.save(ROOT/'cleanup.json',result);event('cleanup-finished',**result)
 assert result['cleanup_verified'],'owned copy cleanup incomplete'
def peak():
 b,m=release.b,release.m
 path=ROOT/'peak';path.mkdir(mode=0o700)
 plan=b.strict(b.trusted(ROOT/'capacity-plan.PRIVATE.json'));plan['expected']['controller_id']=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Id']
 h=release.Host(plan,path,LOCKS,lambda *_:None)
 h.e['controller_image']=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Image']
 online=b.strict(b.http('/config/',2019));scope=copy.deepcopy(plan['routing']);scope['online_sha256']=b.sha(json.dumps(online,sort_keys=True,separators=(',',':')).encode())
 routes=m.routing_variants(online,scope)
 for name,value in routes.items():b.atomic(path/(name+'.json'),b.encoded(value))
 def reload(name):
  subprocess.run(['caddy','validate','--config',str(path/(name+'.json'))],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  subprocess.run(['caddy','reload','--config',str(path/(name+'.json'))],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  assert b.strict(b.http('/config/',2019))==routes[name]
 h.quiet_tasks();original_bindings()
 timers={t:subprocess.check_output(['systemctl','show',t,'-p','ActiveState','--value'],text=True).strip() for t in (*b.TIMERS,'baarcha-motion-access.timer')}
 prior=[];fenced=False
 report=dict(complete=False,target=100,source_projects=74,fillers=26)
 try:
  event('peak-fence-intent');c.save(path/'fence-state.json',dict(reopened=False));fenced=True;reload('drain')
  subprocess.run(['systemctl','stop',*timers],check=True)
  end=time.monotonic()+180
  while True:
   try:
    h.quiet_tasks()
    assert all(subprocess.check_output(['systemctl','show',t.replace('.timer','.service'),'-p','ActiveState','--value'],text=True).strip()=='inactive' for t in timers)
    break
   except Exception:
    assert time.monotonic()<end,'active customer work prevented peak test';time.sleep(2)
  reload('offline')
  for row in PLAN['sources']:
   status=c.rows('select status from sandbox where id=?',(row['sandbox_id'],))[0]['status']
   if status=='running':prior.append(row['sandbox_id'])
  c.save(path/'original-running.json',prior)
  for sid in prior:
   status,_=c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
  assert c.rows("select sum(charged) n from cube_admission where worker_id='vps'")==[dict(n=0)]
  with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
   vps=list(pool.map(clone,PLAN['apps'][:4]))
  assert all(row['worker_id']=='vps' for row in vps)
  report['live_runtimes']=native_proof();report['started_at']=time.time()
  event('all-100-running',count=100)
  overflow_key=PLAN['prefix']+'-overflow'
  status,extra=c.api('POST','/v1/apps',dict(name='Fleet overflow check',external_user_id='operator:fleet-100',external_project_id=overflow_key,runtime_preset='react-vite'))
  assert status==201
  c.save(ROOT/'overflow.PRIVATE.json',dict(id=extra['id'],external_project_id=overflow_key))
  try:
   status,refusal=c.api('POST','/v1/apps/'+extra['id']+'/sandbox',dict(runtime_preset='react-vite',ports=[3000]))
   assert status==503 and refusal['error']['code']=='runtime_capacity','101st sandbox was not refused at capacity'
   assert not c.rows('select id from sandbox where app_id=?',(extra['id'],))
   report['overflow_refused']=True
  finally:
   status,_=c.api('DELETE','/v1/apps/'+extra['id']);assert status==204
  report['rounds']=[]
  for i in range(3):
   with concurrent.futures.ThreadPoolExecutor(max_workers=25) as pool:responses=list(pool.map(preview,list(ready)))
   report['rounds'].append(responses);c.save(ROOT/'result.json',report);event('peak-round',round=i+1,http_successes=sum(200<=r['status']<400 for r in responses))
   time.sleep(10)
  report['live_after_load']=native_proof()
  report['workspace_copies_verified']=sum(bool(r.get('workspace_verified')) for r in ready)
  assert report['workspace_copies_verified']==74
  report['guests']=[json.loads((GUESTS/a['id']/'complete.json').read_text()) for a in PLAN['apps']]
  report['complete']=True;report['finished_at']=time.time();c.save(ROOT/'result.json',report)
 finally:
  # Free the VPS before restoring customer runtime routes. Other copies remain private.
  for app in PLAN['apps'][:4]:
   if (GUESTS/app['id']/'created.json').exists() and not (GUESTS/app['id']/'deleted.json').exists():remove(app)
  for sid in prior:
   status,_=c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200,'original running project restore failed'
  original_bindings()
  for timer,state in timers.items():
   assert state in ('active','inactive')
   if state=='active':subprocess.run(['systemctl','start',timer],check=True)
  if fenced:
   reload('online');c.save(path/'fence-state.json',dict(reopened=True));event('customer-routes-restored')
  c.save(ROOT/'result.json',report)
if __name__=='__main__':
 spec=importlib.util.spec_from_file_location('release',ROOT/'capacity-release.py');release=importlib.util.module_from_spec(spec);spec.loader.exec_module(release)
 with release.b.locked() as LOCKS:
  def signal_handler(*_):
   state=ROOT/'peak/fence-state.json'
   if state.exists() and not json.loads(state.read_text())['reopened']:return
   raise KeyboardInterrupt
  for sig in (signal.SIGTERM,signal.SIGINT):signal.signal(sig,signal_handler)
  try:run_b200()
  except BaseException as error:
   import traceback
   c.save(ROOT/'load-failure.json',dict(error=str(error),traceback=traceback.format_exc()))
   state=ROOT/'peak/fence-state.json'
   if state.exists() and not json.loads(state.read_text())['reopened']:
    event('fenced-recovery-required')
    while True:time.sleep(30)
   raise
