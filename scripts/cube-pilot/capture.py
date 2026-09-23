#!/usr/bin/env python3
"""Historical v7 guest-browser experiment (retired capture-capable template only).
Current app guests have no capture route; use the platform shared-service checks.
Run only inside the disposable marked pilot VM.
"""
import base64, json, pathlib, secrets, statistics, time, urllib.request, urllib.error
ROOT=pathlib.Path('/root/cube-pilot')
assert pathlib.Path('/root/bench-ready').exists()
key=json.loads((ROOT/'test-secrets.json').read_text())['cube_key']
template=json.loads((ROOT/'capture-template.json').read_text())['job']['template_id']
token=secrets.token_hex(32)
report={'template':template,'checks':[],'timings_ms':{},'fixture':'prepared React app; real Chromium inside guest'}

def request(method,path,data=None,guest=None,credential=True):
 headers={'Content-Type':'application/json'}
 origin='http://127.0.0.1:3000'
 if guest:
  origin='http://127.0.0.1:80'
  headers['Host']='3031-'+guest['sandboxID']+'.cube.app'
  headers['cube-traffic-access-token']=guest['trafficAccessToken']
  if credential:headers['Authorization']='Bearer '+token
 else:headers['X-API-Key']=key
 raw=json.dumps(data).encode() if data is not None else None
 req=urllib.request.Request(origin+path,data=raw,headers=headers,method=method)
 try:
  with urllib.request.urlopen(req,timeout=100) as response:
   raw=response.read();return response.status,json.loads(raw) if raw else None
 except urllib.error.HTTPError as error:return error.code,error.read().decode(errors='replace')[:300]

guest=None
try:
 code,guest=request('POST','/sandboxes',{'templateID':template,'timeout':600,
  'envVars':{'RUNTIMED_HTTP_ADDR':':3031','RUNTIMED_HTTP_TOKEN':token},
  'lifecycle':{'onTimeout':'pause','autoResume':False},'allow_internet_access':False,
  'network':{'allowPublicTraffic':False,'allowOut':[],'denyOut':['0.0.0.0/0']},
  'metadata':{'purpose':'baarcha-disposable-guest-capture'}})
 assert code==201,(code,guest)
 for _ in range(100):
  code,status=request('GET','/status',guest=guest)
  if code==200:break
  time.sleep(.1)
 else:raise AssertionError('supervisor not ready')
 for body,auth,expected in [({'mode':'hero'},False,401),({'mode':'hero','url':'http://invalid'},True,400)]:
  code,_=request('POST','/capture',body,guest,auth);assert code==expected,(code,expected)
 report['checks'].append('capture auth and fixed-target input')
 def capture(label,mode='hero'):
  start=time.monotonic();code,result=request('POST','/capture',{'mode':mode},guest)
  elapsed=round((time.monotonic()-start)*1000,2)
  assert code==200,(code,result)
  for field in ['screenshot','cover']:
   image=base64.b64decode(result[field],validate=True)
   assert image.startswith(b'\xff\xd8\xff') and len(image)>1000
  assert len(result['raw']['text'])>0,result['raw']
  report['timings_ms'][label]=elapsed
  report.setdefault('capture_stages',{})[label]={'timings_ms':result.get('timings_ms'),'ready':result.get('ready')}
  return elapsed
 capture('cold_hero')
 warm=[capture('warm_hero_'+str(i)) for i in range(5)]
 report['timings_ms']['warm_hero_median']=statistics.median(warm)
 capture('full_page','full')
 report['checks'].append('real guest Chromium cover, full image and metadata')
 code,_=request('POST','/sandboxes/'+guest['sandboxID']+'/pause');assert code in (200,204),code
 start=time.monotonic();code,_=request('POST','/sandboxes/'+guest['sandboxID']+'/connect',{'timeout':600});assert code==200,code
 report['timings_ms']['resume']=round((time.monotonic()-start)*1000,2)
 capture('resumed_hero')
 report['checks'].append('warm browser survives VM pause/resume')
 (ROOT/'capture-result.json').write_text(json.dumps(report,indent=2))
 print(json.dumps(report,indent=2))
finally:
 if isinstance(guest,dict) and guest.get('sandboxID'):
  request('DELETE','/sandboxes/'+guest['sandboxID'])
