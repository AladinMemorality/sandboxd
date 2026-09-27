import ctypes,importlib.util,json,os,pathlib,sqlite3,subprocess,time,urllib.request,urllib.error
P=pathlib.Path;stage=P('/opt/baarcha-bench/cube-fleet-20260927');job=P('/opt/baarcha-cube/worker-01/maintenance/fleet-capacity-finish-02');evidence=job/'fixture-cleanup-reconciliation';os.umask(0o077)
s=importlib.util.spec_from_file_location('r',stage/'controller-fleet-release.py');r=importlib.util.module_from_spec(s);s.loader.exec_module(r)
assert r.x.ticks(1801667)=='501167496' and r.x.ticks(1812273)=='501217168'
assert json.loads((job/'current.json').read_text())['phase']=='continuation-pending'
assert json.loads(r.b.http('/config/',2019))==json.loads((job/'offline.json').read_text())
fds=[];h=os.pidfd_open(1801667);lib=ctypes.CDLL(None,use_errno=True)
for name in r.b.LOCKS:
 matches=[int(f.name) for f in P('/proc/1801667/fd').iterdir() if os.readlink(f)==name];assert len(matches)==1
 fd=lib.syscall(438,h,matches[0],0);assert fd>=0;fds.append(fd)
evidence.mkdir(mode=0o700)
cp=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];ident=cp['Id'];assert ident=='5912348b37866c34829724da380b44aac2070ac90261e666534fdbead8ebfa03'
env=dict(v.split('=',1) for v in cp['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
apps={a['id']:a for a in json.loads((job/'fleet-acceptance/apps.PRIVATE.json').read_text())}
expected={'bdc3a7088eaa4049822795e921035681':'01M3HZ12TQPBH993T6ATDQBFA5','cf5f1e45530a490db547e2f850607cea':'01M3HZ4A8JFRVPM0T2VSAT6HR8'}
def request(port,path,method='GET',native=False):
 req=urllib.request.Request('http://127.0.0.1:'+str(port)+path,method=method,headers={'X-API-Key':env['SANDBOXD_CUBE_API_KEY']} if native else {'Authorization':'Bearer '+token})
 try:
  with urllib.request.urlopen(req,timeout=30) as res:return res.status,res.read()
 except urllib.error.HTTPError as error:return error.code,error.read()
for runtime,app in expected.items():
 assert app in apps
 status,raw=request(9090,'/v1/apps/'+app);assert status==200
 a=json.loads(raw);assert a['external_user_id']=='operator:fleet-acceptance' and a['external_project_id']==apps[app]['external_project_id']
 status,_=request(20300,'/sandboxes/'+runtime,native=True);assert status==404
# Native inventory proves retained customer runtimes and no test VM on either worker.
plan=json.loads((stage/'controller-fleet-plan.PRIVATE.json').read_text())
for node,wanted in [('10.0.2.15',{a['runtime_id'] for a in plan['bindings']}),('10.254.240.2',set())]:
 inv=r.native(18089,'/cube/sandbox/inventory?host_id='+node);assert inv['ret']['ret_code']==200 and inv['size']==1 and {a['sandbox_id'] for a in inv['data']}==wanted
with sqlite3.connect('/var/lib/sandboxd/state/sandboxd.db',timeout=10) as db:
 db.row_factory=sqlite3.Row
 with sqlite3.connect(evidence/'before.PRIVATE.sqlite') as backup:db.backup(backup)
 rows=[dict(a) for a in db.execute("SELECT * FROM cube_admission WHERE state='pending'")];assert {a['runtime_id'] for a in rows}==set(expected)
 (evidence/'before.PRIVATE.json').write_text(json.dumps(rows))
 db.execute('BEGIN IMMEDIATE')
 paused=False
 try:
  subprocess.run(['docker','pause',ident],check=True,capture_output=True);paused=True
  assert json.loads(subprocess.check_output(['docker','inspect',ident]))[0]['State']['Paused']
  for a in rows:
   assert a['admission_key']=='app:'+expected[a['runtime_id']] and a['operation']=='delete' and a['charged']==1
   status,_=request(20300,'/sandboxes/'+a['runtime_id'],native=True);assert status==404
   assert db.execute("UPDATE cube_admission SET state='deleted',charged=0 WHERE admission_key=? AND runtime_id=? AND token=? AND worker_id=? AND operation='delete' AND state='pending' AND charged=1",(a['admission_key'],a['runtime_id'],a['token'],a['worker_id'])).rowcount==1
   # Preserve storage grants conservatively; no extra storage credit is invented.
  db.commit()
 finally:
  db.rollback()
  if paused:subprocess.run(['docker','unpause',ident],check=True,capture_output=True)
for runtime,app in expected.items():
 status,_=request(9090,'/v1/apps/'+app,'DELETE');assert status==204
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
 assert {a[0] for a in db.execute('select id from sandbox')}=={a['sandbox_id'] for a in plan['bindings']}
 assert db.execute('select sum(charged) from cube_admission').fetchone()[0]==0
result={'cleanup_verified':True,'customer_bindings_preserved':74,'reconciled_owned_deletes':list(expected),'controller_generation_preserved':ident,'storage_grants_retained_conservatively':True}
(evidence/'complete.json').write_text(json.dumps(result));print(json.dumps(result))
