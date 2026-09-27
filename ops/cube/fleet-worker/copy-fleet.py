#!/usr/bin/env python3
"""Private, temporary customer-workspace copies for the 100-slot fleet test."""
import base64,concurrent.futures,hashlib,http.client,json,os,secrets,sqlite3,subprocess,sys,threading,time,zipfile
from pathlib import Path
from urllib.parse import urlsplit
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
ROOT=Path('/opt/baarcha-bench/cube-fleet-20260927/capacity-100')
DB='file:/var/lib/sandboxd/state/sandboxd.db?mode=ro'
os.umask(0o077)
def rows(sql,args=()):
 with sqlite3.connect(DB,uri=True,timeout=5) as d:
  d.row_factory=sqlite3.Row
  return [dict(r) for r in d.execute(sql,args)]
def save(path,value):
 temp=path.with_suffix(path.suffix+'.pending');temp.write_text(json.dumps(value));temp.chmod(0o600);os.replace(temp,path)
def environment():return dict(v.split('=',1) for v in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env'])
ENV=environment()
TOKEN=ENV['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
def request(host,port,path,method='GET',body=None,headers=None,timeout=180):
 conn=http.client.HTTPConnection(host,port,timeout=timeout)
 try:
  conn.request(method,path,body,headers or {});r=conn.getresponse();data=r.read(8*1024**2+1);assert len(data)<=8*1024**2
  return r.status,data
 finally:conn.close()
def api(method,path,body=None):
 status,raw=request('127.0.0.1',9090,path,method,None if body is None else json.dumps(body),{'Authorization':'Bearer '+TOKEN,'Content-Type':'application/json'})
 return status,json.loads(raw) if raw else None
def client(sid):
 r=rows("select b.*,a.worker_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id where b.sandbox_id=?",(sid,))[0]
 key=ENV.get('SANDBOXD_SECRETS_KEY') or Path('/var/lib/sandboxd/secrets.key').read_text().strip()
 credentials=json.loads(AESGCM(base64.b64decode(key)).decrypt(r['token_nonce'],r['token_ciphertext'],None))
 origin=('127.0.0.1',20080) if r['worker_id']=='vps' else ('10.254.240.2',28080)
 return origin,{'Host':'3031-'+r['runtime_id']+'.'+r['domain'],'Authorization':'Bearer '+credentials['supervisor_token'],'cube-traffic-access-token':credentials['traffic_access_token']}
def guest(sid,method,path,body=None):
 origin,headers=client(sid)
 if body is not None:headers['Content-Type']='application/json';body=json.dumps(body)
 return request(*origin,path,method,body,headers)
def manifest(path):
 h=hashlib.sha256();count=0;size=0
 with zipfile.ZipFile(path) as z:
  names=z.namelist();assert len(names)==len(set(names)) and len(names)<=200000
  for n in sorted(names):
   i=z.getinfo(n);digest=hashlib.sha256()
   with z.open(i) as f:
    while data:=f.read(1024*1024):digest.update(data)
   h.update(json.dumps([n,i.external_attr,i.file_size,digest.hexdigest()],separators=(',',':')).encode());count+=1;size+=i.file_size
 return dict(sha256=h.hexdigest(),files=count,expanded_bytes=size,archive_bytes=path.stat().st_size)
def prepare():
 ROOT.mkdir(mode=0o700,exist_ok=True)
 assert not (ROOT/'plan.PRIVATE.json').exists()
 source=rows("select s.id sandbox_id,s.app_id,s.status,s.web_port,a.name,a.runtime_preset,b.runtime_id,b.template_id from sandbox s join app a on a.id=s.app_id join runtime_binding b on b.sandbox_id=s.id order by s.id")
 assert len(source)==74 and len({v['sandbox_id'] for v in source})==74
 p=dict(prefix='fleet-100-'+secrets.token_hex(8),sources=source,apps=[],target=100)
 save(ROOT/'plan.PRIVATE.json',p)
 for i in range(100):
  original=source[i] if i<len(source) else None;key=p['prefix']+'-'+str(i)
  save(ROOT/'create-intent.PRIVATE.json',dict(index=i,external_project_id=key))
  status,a=api('POST','/v1/apps',dict(name=('Copy of '+original['name']) if original else 'Fleet capacity filler '+str(i),external_user_id='operator:fleet-100',external_project_id=key,runtime_preset=original['runtime_preset'] if original else 'react-vite',tags=['operator-acceptance',p['prefix']]))
  assert status==201
  p['apps'].append(dict(id=a['id'],external_project_id=key,source=original));save(ROOT/'plan.PRIVATE.json',p)
 print(json.dumps(dict(prepared_apps=len(p['apps']),source_projects=len(source))),flush=True)
def export_one(row,include_running=False):
 sid=row['sandbox_id'];out=ROOT/'sources'/sid;out.mkdir(mode=0o700,exist_ok=True)
 if (out/'complete.json').exists():return json.loads((out/'complete.json').read_text())
 original=rows('select status from sandbox where id=?',(sid,))[0]['status']
 # Active customer previews are deferred until the copy window.
 if original!='stopped' and not include_running:return dict(sandbox_id=sid,deferred='currently running')
 save(out/'intent.json',dict(sandbox_id=sid,original_status=original))
 started=False;quiesced=False;stop=threading.Event();heartbeat=None
 try:
  status,_=api('POST','/v1/sandboxes/'+sid+'/start');assert status==200,'source start failed';started=True
  status,raw=guest(sid,'GET','/status');assert status==200 and json.loads(raw)['active_task'] is None
  status,v=api('POST','/v1/sandboxes/'+sid+'/preview-access');assert status==200
  def touch():
   while not stop.wait(15):
    try:request('127.0.0.1',9090,'/',headers={'Host':urlsplit(v['url']).netloc,'Cookie':'sandbox_preview='+v['token']},timeout=20)
    except Exception:pass
  heartbeat=threading.Thread(target=touch,daemon=True);heartbeat.start()
  # Resume is mandatory even when quiescence only partially succeeded.
  quiesced=True;status,_=guest(sid,'POST','/workspace/quiesce');assert status==200,'source cannot quiesce'
  origin,headers=client(sid)
  assert origin==('127.0.0.1',20080),'export on the source worker; never stream remote workspaces through the controller'
  conn=http.client.HTTPConnection(*origin,timeout=1800)
  try:
   conn.request('GET','/export/private-workspace-v2',headers=headers);response=conn.getresponse()
   assert response.status==200,'source export HTTP '+str(response.status)
   count=0
   with (out/'workspace.zip.pending').open('xb') as dest:
    while data:=response.read(1024*1024):
     count+=len(data);assert count<=4*1024**3;dest.write(data)
   os.replace(out/'workspace.zip.pending',out/'workspace.zip')
  finally:conn.close()
  proof=manifest(out/'workspace.zip');save(out/'manifest.json',proof)
  result=dict(sandbox_id=sid,**proof)
 finally:
  stop.set()
  if heartbeat:heartbeat.join(25)
  if quiesced:
   status,_=guest(sid,'POST','/workspace/resume');assert status==200,'SOURCE RESUME REQUIRES REVIEW'
  if started and original=='stopped':
   status,_=api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200,'SOURCE STOP REQUIRES REVIEW'
 if started:save(out/'complete.json',result)
 return result
def exports():
 plan=json.loads((ROOT/'plan.PRIVATE.json').read_text());(ROOT/'sources').mkdir(mode=0o700,exist_ok=True)
 # Leave two of the four VPS slots for live customer activity.
 with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
  pending={pool.submit(export_one,s):s['sandbox_id'] for s in plan['sources']}
  completed=0
  for f in concurrent.futures.as_completed(pending):
   try:result=f.result()
   except Exception as error:
    save(ROOT/('export-failure-'+pending[f]+'.json'),dict(error=str(error)));print(json.dumps(dict(source=pending[f],failed=True)),flush=True)
   else:
    completed+=int('sha256' in result);print(json.dumps(dict(exported=completed,source=pending[f],bytes=result.get('archive_bytes'),deferred=result.get('deferred'))),flush=True)
if __name__=='__main__':
 {'prepare':prepare,'export':exports}[sys.argv[1]]()
