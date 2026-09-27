"""Check B200 readiness reuse across the old 30-second renewal boundary."""
import http.client,json,os,pathlib,secrets,sqlite3,subprocess,sys,time
from urllib.parse import urlsplit
P=pathlib.Path;os.umask(0o077);parent=P(sys.argv[1]);job=parent/'preview-reuse';job.mkdir(mode=0o700)
def http(port,path,method='GET',body=None,headers=None):
 c=http.client.HTTPConnection('127.0.0.1',port,timeout=90)
 try:
  c.request(method,path,body,headers or {});res=c.getresponse();return res.status,res.read(2*1024*1024)
 finally:c.close()
st,raw=http(2019,'/config/');assert st==200 and json.loads(raw)==json.loads((parent/'offline.json').read_text())
cp=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];env=dict(v.split('=',1) for v in cp['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
def api(method,path,data=None,raw=False):
 st,out=http(9090,path,method,data if raw else None if data is None else json.dumps(data),{'Authorization':'Bearer '+token,'Content-Type':'application/json'})
 return st,json.loads(out) if out else None
def rows(sql,args=()):
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:return db.execute(sql,args).fetchall()
assert rows('select sum(charged) from cube_admission')==[(0,)]
baseline=rows('select id from sandbox order by id');prefix='fleet-reuse-'+secrets.token_hex(8);apps=[];result={'complete':False}
try:
 for i in range(5):
  key=prefix+'-'+str(i);st,a=api('POST','/v1/apps',{'name':'Fleet preview readiness acceptance','external_user_id':'operator:fleet-reuse-acceptance','external_project_id':key,'runtime_preset':'react-vite','tags':['operator-acceptance',prefix]});assert st==201
  apps.append({'id':a['id'],'external_project_id':key});(job/'apps.PRIVATE.json').write_text(json.dumps(apps))
  st,s=api('POST','/v1/apps/'+a['id']+'/sandbox',{'runtime_preset':'react-vite','ports':[3000]});assert st==201
 assert rows('select a.worker_id from cube_admission a join runtime_binding b on b.runtime_id=a.runtime_id where b.sandbox_id=?',(s['id'],))==[('b200-01',)]
 st,_=api('PUT','/v1/sandboxes/'+s['id']+'/files?path=index.html','<html><body>'+prefix+'</body></html>',True);assert st==200
 st,v=api('POST','/v1/sandboxes/'+s['id']+'/preview-access');assert st==200
 def preview():
  began=time.monotonic();st,raw=http(9090,'/',headers={'Host':urlsplit(v['url']).netloc,'Cookie':'sandbox_preview='+v['token']})
  assert st==200 and prefix.encode() in raw;return time.monotonic()-began
 deadline=time.monotonic()+60
 while True:
  try:preview();break
  except AssertionError:
   assert time.monotonic()<deadline;time.sleep(1)
 sql='select a.token from cube_admission a join runtime_binding b on b.runtime_id=a.runtime_id where b.sandbox_id=?'
 before=rows(sql,(s['id'],));assert len(before)==1
 time.sleep(35)
 elapsed=preview();assert rows(sql,(s['id'],))==before,'warm page unnecessarily renewed native connection'
 assert elapsed<5,'warm page exceeded five seconds'
 result.update(complete=True,worker='b200-01',cache_reuse_after_seconds=35,warm_page_seconds=elapsed,admission_generation_unchanged=True)
finally:
 failures=[]
 for a in reversed(apps):
  try:
   st,v=api('GET','/v1/apps/'+a['id']);assert st==200 and v['external_user_id']=='operator:fleet-reuse-acceptance' and v['external_project_id']==a['external_project_id']
   st,_=api('DELETE','/v1/apps/'+a['id']);assert st==204
  except Exception:failures.append(a['id'])
 result['cleanup_failures']=failures;result['cleanup_verified']=not failures and rows('select id from sandbox order by id')==baseline and rows('select sum(charged) from cube_admission')==[(0,)]
 (job/'result.json').write_text(json.dumps(result));print(json.dumps(result));assert result['cleanup_verified']
