#!/usr/bin/env python3
"""Native full-stack integration fixture. Requires setup.py in the isolated VM.
The fake OpenCode binary exercises lifecycle/transport, not real model quality.
"""
import base64, hashlib, hmac, io, json, os, pathlib, statistics, subprocess, time, urllib.request, urllib.error, urllib.parse, zipfile
ROOT=pathlib.Path('/root/cube-pilot'); SRC=ROOT/'src'
assert (pathlib.Path('/root/bench-ready')).exists()
secrets=json.loads((ROOT/'test-secrets.json').read_text())
template=json.loads((ROOT/'template-build.json').read_text())['job']['template_id']
origin='http://127.0.0.1:19091'
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):return None
opener=urllib.request.build_opener(NoRedirect)
proc=None; log=None
report={'template':template,'checks':[],'timings':{},'task_fixture':'deterministic fake OpenCode, zero model tokens and no external model call'}
sandboxes=[]
def call(method,path,data=None,tenant='a',headers=None,want=None):
 h={'Authorization':'Bearer '+secrets['api_'+tenant]} if tenant else {}
 if isinstance(data,dict):data=json.dumps(data).encode();h['Content-Type']='application/json'
 if isinstance(data,str):data=data.encode()
 h.update(headers or {})
 req=urllib.request.Request(origin+path,data=data,method=method,headers=h)
 try:
  with opener.open(req,timeout=160) as res:code,body=res.status,res.read()
 except urllib.error.HTTPError as e:code,body=e.code,e.read()
 if want is not None:assert code==want,(method,path,code,body[:500].decode(errors='replace'))
 try:out=json.loads(body)
 except (ValueError,UnicodeDecodeError):out=body
 return code,out

def start(apps):
 global proc,log
 if proc is not None:proc.terminate();proc.wait(timeout=20)
 if log is not None:log.close()
 env=dict(os.environ,SANDBOXD_ADDR='127.0.0.1:19091',SANDBOXD_DATA_DIR='/data/cube-pilot',SANDBOXD_LOG_DIR='/data/cube-pilot/log',SANDBOXD_MIGRATIONS=str(SRC/'control-plane/migrations'),SANDBOXD_API_AUTH_DISABLED='false',SANDBOXD_API_TOKENS='pilot-a='+secrets['api_a']+',pilot-b='+secrets['api_b'],SANDBOXD_PREVIEW_TOKEN_SECRETS='pilot='+secrets['preview_secret'],PREVIEW_DOMAIN='pilot.localhost',PREVIEW_URL_SCHEME='http',SANDBOXD_PUBLIC_HTTP_PORT='19091',SANDBOXD_AGENT_PROXY_ADDR='127.0.0.1:19101',SANDBOXD_CUBE_ENABLED='true',SANDBOXD_CUBE_API_URL='http://127.0.0.1:3000',SANDBOXD_CUBE_API_KEY=secrets['cube_key'],SANDBOXD_CUBE_PROXY_URL='http://127.0.0.1:80',SANDBOXD_CUBE_DOMAIN='cube.app',SANDBOXD_CUBE_APP_IDS=','.join(apps) or 'unassigned',SANDBOXD_CUBE_TEMPLATES=json.dumps({'react-vite':template}),SANDBOXD_IDLE_REAP_INTERVAL_SECONDS='0',SANDBOXD_PRESSURE_INTERVAL_SECONDS='0',SANDBOXD_TELEMETRY_DISABLED='true')
 log=open(ROOT/'pilot-server.log','ab')
 proc=subprocess.Popen([str(SRC/'control-plane/sandboxd')],env=env,stdout=log,stderr=log)
 for _ in range(100):
  assert proc.poll() is None,'control plane exited; inspect pilot-server.log'
  try:
   if call('GET','/healthz',tenant=None)[0]==200:return
  except OSError:pass
  time.sleep(.1)
 raise AssertionError('control plane did not start')

def jwt(sid,user):
 enc=lambda obj:base64.urlsafe_b64encode(json.dumps(obj,separators=(',',':')).encode()).rstrip(b'=')
 raw=enc({'alg':'HS256','typ':'JWT','kid':'pilot'})+b'.'+enc({'aud':'sandbox-preview','sandbox_id':sid,'sub':user,'exp':int(time.time())+3600})
 return (raw+b'.'+base64.urlsafe_b64encode(hmac.new(secrets['preview_secret'].encode(),raw,hashlib.sha256).digest()).rstrip(b'=')).decode()

def preview(sid,path='/',data=None,user='owner-a',authorized=True,want=200):
 headers={'Host':f's-{sid.lower()}-3000.preview.pilot.localhost:19091'}
 if authorized:headers['Cookie']='sandbox_preview='+jwt(sid,user)
 return call('POST' if data is not None else 'GET',path,data,tenant=None,headers=headers,want=want)[1]

def check(name):report['checks'].append(name);print('PASS',name,flush=True)
try:
 start([])
 apps=[]
 for suffix in ['a','b']:
  _,a=call('POST','/v1/apps',{'name':'Cube integration '+suffix,'external_user_id':'owner-'+suffix,'external_project_id':'pilot-'+suffix+'-'+str(time.time_ns()),'runtime_preset':'react-vite'},want=201)
  apps.append(a['id'])
 start(apps)
 for app in apps:
  began=time.monotonic();_,sb=call('POST',f'/v1/apps/{app}/sandbox',{},want=201);sandboxes.append(sb['id']);report['timings']['create_'+app]=round((time.monotonic()-began)*1000,2)
 a,b=sandboxes
 check('two guests create and authenticate their supervisors')
 _,inspected=call('GET',f'/v1/sandboxes/{a}',want=200)
 assert inspected['runtime_provider']=='cube'
 call('POST',f'/v1/sandboxes/{a}/preview-access',{},tenant='b',want=404)
 _,access=call('POST',f'/v1/sandboxes/{a}/preview-access',{},want=200)
 handoff=urllib.parse.urlsplit(access['access_url'])
 stable=urllib.parse.urlsplit(inspected['preview']['url'])
 assert handoff.netloc==stable.netloc and handoff.path=='/__sandboxd/preview-auth'
 request=urllib.request.Request(origin+handoff.path+'?'+handoff.query,headers={'Host':handoff.netloc})
 try:opener.open(request,timeout=10);raise AssertionError('handoff did not redirect')
 except urllib.error.HTTPError as response:
  assert response.code==302 and response.headers['Location']=='/'
  cookie=response.headers['Set-Cookie']
  assert 'HttpOnly' in cookie and 'Domain=' not in cookie and 'sandbox_preview=' in cookie
  browser_cookie=cookie.split(';',1)[0]
 call('GET','/__sandboxd/preview-ready',tenant=None,headers={'Host':handoff.netloc,'Cookie':browser_cookie},want=204)
 call('GET','/__sandboxd/preview-ready',tenant=None,headers={'Host':handoff.netloc},want=401)
 call('GET',f'/v1/sandboxes/{a}/files?path=.&recursive=true',want=200)
 check('actual UI file query and scoped preview link/cookie handoff')
 call('GET',f'/v1/sandboxes/{a}',tenant='b',want=404)
 for bad in ['../etc/passwd','/etc/passwd','.runtimed/sock','node_modules/.bin/vite']:
  code,_=call('GET',f'/v1/sandboxes/{a}/files/content?path='+urllib.parse.quote(bad,safe=''))
  assert code in [400,404],(bad,code)
 check('wrong tenant and unsafe file paths rejected')
 for _ in range(50):
  try:preview(a,'/__bench/ready');break
  except AssertionError:time.sleep(.2)
 else:raise AssertionError('frontend/backend never ready')
 preview(a,authorized=False,want=401);preview(a,user='owner-b',want=401)
 preview(a,'/__bench/set?value=71',data=b'')
 initial=preview(a,'/__bench/ready');assert initial['memoryValue']==71
 check('private frontend and backend preview')
 call('PUT',f'/v1/sandboxes/{a}/files?path=pilot-owner.txt','private-a',want=200)
 call('GET',f'/v1/sandboxes/{b}/files/content?path=pilot-owner.txt',want=404)
 _,archive=call('GET',f'/v1/sandboxes/{a}/export',want=200)
 assert zipfile.ZipFile(io.BytesIO(archive)).read('pilot-owner.txt')==b'private-a'
 check('workspace write/read/export and cross-guest independence')
 durations=[]
 for _ in range(5):
  call('POST',f'/v1/sandboxes/{a}/stop',{},want=200)
  began=time.monotonic();after=preview(a,'/__bench/ready');durations.append((time.monotonic()-began)*1000)
  assert after['bootId']==initial['bootId'] and after['memoryValue']==71 and after['diskValue']==71
 report['timings']['resume_ms']=durations;report['timings']['resume_median_ms']=statistics.median(durations)
 check('five authenticated wake cycles preserve process memory and disk')
 _,before_tasks=call('GET',f'/v1/sandboxes/{a}/tasks',want=200)
 _,disabled=call('POST',f'/v1/sandboxes/{a}/tasks',{'prompt':'must not start without scoped relay','agent':'claude-code','timeout_s':30},want=503)
 assert disabled.get('error',{}).get('code')=='model_relay_disabled',disabled
 _,after_tasks=call('GET',f'/v1/sandboxes/{a}/tasks',want=200)
 assert after_tasks==before_tasks,'disabled Claude request created a task'
 check('Claude tasks fail clearly while scoped model relay is disabled')
 _,task=call('POST',f'/v1/sandboxes/{a}/tasks',{'prompt':'fixture writes a file without any model request','agent':'opencode','timeout_s':30},want=202)
 for _ in range(150):
  _,result=call('GET',f'/v1/sandboxes/{a}/tasks/'+task['id'],want=200)
  if result.get('status')!='running':break
  time.sleep(.2)
 assert result.get('status')=='succeeded',result
 _,task_file=call('GET',f'/v1/sandboxes/{a}/files/content?path=pilot-task-result.txt',want=200)
 assert task_file==b'verified guest task\n',task_file
 start(apps)
 _,recovered=call('GET',f'/v1/sandboxes/{a}/tasks/'+task['id'],want=200);assert recovered['status']=='succeeded'
 check('deterministic OpenCode transport fixture result and workspace survive control-plane restart; no AI request')
 # Config must reach the original guest without being copied into a remix.
 call('POST',f'/v1/apps/{apps[0]}/config',{'key':'PILOT_SECRET','value':'owner-secret','sensitive':True,'access_policy':'runtime_access'},want=201)
 for _ in range(100):
  time.sleep(.1)
  _,cfg=call('GET',f'/v1/sandboxes/{a}/files/content?path=pilot-isolation.json',want=200)
  if isinstance(cfg,dict) and cfg.get('configuredSecret'):break
 else:raise AssertionError('runtime config not applied')
 call('PUT',f'/v1/sandboxes/{a}/files?path=.env','SECRET=owner-only',want=200)
 call('PUT',f'/v1/sandboxes/{a}/files?path=private.db','private-user-database',want=200)
 began=time.monotonic()
 _,snap=call('POST','/v1/snapshots',{'source_sandbox_id':a,'name':'Source fixture'},want=201)
 report['timings']['publish_ms']=(time.monotonic()-began)*1000
 began=time.monotonic()
 _,fork=call('POST',f'/v1/apps/{apps[0]}/fork',{'snapshot_id':snap['id'],'name':'Independent remix','external_user_id':'owner-c','external_project_id':'pilot-remix-'+str(time.time_ns())},want=201)
 report['timings']['remix_ms']=(time.monotonic()-began)*1000
 assert fork.get('sandbox'),fork
 c=fork['sandbox']['id'];sandboxes.append(c)
 for _ in range(100):
  try:fresh=preview(c,'/__bench/ready',user='owner-c');break
  except AssertionError:time.sleep(.2)
 else:raise AssertionError('remix frontend/backend did not start')
 assert fresh['bootId']!=initial['bootId'] and fresh['memoryValue']==0 and fresh['diskValue']==0,fresh
 for filename in ['.env','private.db','bench-state.json']:
  call('GET',f'/v1/sandboxes/{c}/files/content?path='+filename,want=404)
 _,newconfig=call('GET',f"/v1/apps/{fork['app']['id']}/config",want=200)
 assert 'PILOT_SECRET' not in json.dumps(newconfig)
 for _ in range(100):
  code,forkprobe=call('GET',f'/v1/sandboxes/{c}/files/content?path=pilot-isolation.json')
  if code==200 and isinstance(forkprobe,dict):break
  assert code in [200,404],('remix probe',code)
  time.sleep(.1)
 else:raise AssertionError('remix isolation probe absent')
 assert forkprobe['configuredSecret'] is False,'creator runtime configuration inherited by fork'

 check('source publication/remix omits runtime data and owner config')
 call('DELETE',f'/v1/apps/{apps[0]}/config/PILOT_SECRET',want=204)
 for _ in range(100):
  time.sleep(.1)
  _,cfg=call('GET',f'/v1/sandboxes/{a}/files/content?path=pilot-isolation.json',want=200)
  if isinstance(cfg,dict) and not cfg.get('configuredSecret'):break
 else:raise AssertionError('deleted config remained in running process')
 check('runtime config application and deletion')
 _,raw=call('GET',f'/v1/sandboxes/{a}/files/content?path=pilot-isolation.json',want=200)
 isolation=json.loads(raw) if isinstance(raw,bytes) else raw
 report['isolation']=isolation
 assert not isolation['supervisorTokenInherited'] and not isolation['supervisorTokenReadableFromProc'],isolation
 assert not isolation['hostDockerSocket'] and not isolation['hostCanary'],isolation
 assert isolation['uid']==1000 and isolation['noNewPrivileges'] and isolation['effectiveCapabilitiesZero'],isolation
 assert all(group==1000 for group in isolation['supplementaryGroups']),isolation
 expected_probes={('169.254.169.254',80),('172.17.0.1',3000),('10.0.2.2',22),('1.1.1.1',443),('2606:4700:4700::1111',443)}
 assert {(x['host'],x['port']) for x in isolation['networks']}==expected_probes,'network probe coverage changed or missing'
 assert all(x['accessible'] is False for x in isolation['networks']),isolation
 report['isolation_scope']='UID/capabilities/no-new-privileges, process environment/proc, two host paths and five denied network endpoints; not exhaustive isolation certification'
 check('guest identity, proc credential denial, host-file and listed-network probes')
 report['passed']=True
except Exception as exc:
 report['passed']=False;report['failure']=str(exc);raise
finally:
 cleanup_errors=[]
 if report.get('passed'):
  for sid in sandboxes:
   try:call('DELETE',f'/v1/sandboxes/{sid}',want=204)
   except Exception as error:cleanup_errors.append({'sandbox':sid,'error':str(error)})
 report['retained_sandboxes']=([entry['sandbox'] for entry in cleanup_errors] if report.get('passed') else sandboxes)
 if cleanup_errors:report['cleanup_errors']=cleanup_errors;report['passed']=False
 try:
  if proc:
   proc.terminate()
   try:proc.wait(timeout=20)
   except subprocess.TimeoutExpired:proc.kill();proc.wait(timeout=5)
 finally:
  if log:log.close()
  (ROOT/'integration-result.json').write_text(json.dumps(report,indent=2))
 if cleanup_errors:raise AssertionError('integration passed but test sandbox cleanup failed; see integration-result.json')
