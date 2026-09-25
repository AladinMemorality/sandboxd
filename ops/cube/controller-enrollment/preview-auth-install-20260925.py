import contextlib,fcntl,hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,time,urllib.request,urllib.error,secrets

CP='2d8604031c475ba00d85120ebf39f7b14351252fbc87eb624a68445491c47850'
SB='01M3D1Q0E1KM1FEM244XVHEC65'
APP='01M3CZB4HXT2Y8HP8CEY75PCWY'
BASE=pathlib.Path('/opt/baarcha-bench/cube-controller-cutover-tools-20260925')
JOB=pathlib.Path('/opt/baarcha-bench/cube-preview-auth-enrollment-20260925-01')
OVERRIDE=pathlib.Path('/opt/sandboxd/deploy-state/runtime-compose.json')
AUTH=pathlib.Path('/var/lib/sandboxd/cube-preview-auth.env')
HELPER=BASE/'execute.py'
assert hashlib.sha256(HELPER.read_bytes()).hexdigest()=='8a70fc9f7294f14f8a5a8ad891b9268681923b1c21ade8381413270f1ed902c9'
spec=importlib.util.spec_from_file_location('h',HELPER);h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
os.umask(0o077)
fds=[];nested=None
def run(args,**kw): return subprocess.run(args,check=True,capture_output=True,timeout=15,**kw).stdout
def atomic(p,raw):
 t=p.with_name('.'+p.name+'.preview-enrollment')
 fd=os.open(t,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
 os.replace(t,p)
 fd=os.open(p.parent,os.O_RDONLY|os.O_DIRECTORY);os.fsync(fd);os.close(fd)
def request(route,token=None,method='GET'):
 req=urllib.request.Request('http://127.0.0.1:9090'+route,method=method,headers={'Authorization':'Bearer '+token} if token else {})
 try:
  with urllib.request.urlopen(req,timeout=10) as r:return r.status,r.read()
 except urllib.error.HTTPError as e:return e.code,e.read()
try:
 for name in h.LOCKS:
  p=pathlib.Path(name);assert p.resolve(strict=True)==p
  fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);fds.append(fd)
  st=os.fstat(fd);assert st.st_uid==0 and st.st_nlink==1
  fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
 nested=h.NestedLock('c1b590df-28d3-4222-b9a3-c0408c8ccc07')
 c=json.loads(run(['docker','inspect','src-sandboxd-1']))[0]
 assert c['Id']==CP and c['State']['Running']
 env=dict(x.split('=',1) for x in c['Config']['Env'] if '=' in x)
 assert env.get('SANDBOXD_CUBE_APP_IDS')==APP and env.get('SANDBOXD_CUBE_ROLLOUT')=='allowlist'
 assert env.get('SANDBOXD_API_AUTH_DISABLED')=='false' and env.get('SANDBOXD_API_TOKENS') and not env.get('SANDBOXD_PREVIEW_TOKEN_SECRETS')
 assert env.get('SANDBOXD_ENV_FILE','/etc/sandboxd/sandboxd.env')=='/etc/sandboxd/sandboxd.env'
 run(['docker','exec',CP,'sh','-c','test ! -e /etc/sandboxd/sandboxd.env && test ! -L /etc/sandboxd/sandboxd.env'])
 assert not AUTH.exists() and not AUTH.is_symlink()
 assert OVERRIDE.resolve(strict=True)==OVERRIDE and OVERRIDE.stat().st_uid==0 and OVERRIDE.stat().st_mode&0o777==0o600
 old=OVERRIDE.read_bytes();assert hashlib.sha256(old).hexdigest()=='971088a38719f571f7f0ea68eed59d8350f954d514a448502f11f42dd7749861'
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
  assert db.execute('select count(*) from runtime_binding').fetchone()[0]==1
  assert db.execute('select sandbox_id from runtime_binding').fetchone()[0]==SB
  assert db.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0
 token=env['SANDBOXD_API_TOKENS'].split(',',1)[0].split('=',1)[1].strip()
 code,body=request('/v1/sandboxes/'+SB+'/preview-access',token,'POST')
 assert code==503 and json.loads(body)['error']['code']=='preview_auth_unavailable'
 JOB.mkdir(mode=0o700)
 atomic(JOB/'runtime-compose.before.json',old)
 key='cube-20260925='+secrets.token_hex(32)
 auth={k:env.get(k,'') for k in ['SANDBOXD_API_TOKENS','SANDBOXD_API_AUTH_DISABLED','SANDBOXD_AUTH_REDIRECT_URL']}
 auth['SANDBOXD_PREVIEW_TOKEN_SECRETS']=key
 assert all(not any(ch in v for ch in '\r\n\x00\"\'') and v==v.strip() for v in auth.values())
 raw=('\n'.join(k+'='+v for k,v in auth.items())+'\n').encode()
 atomic(AUTH,raw)
 candidate=json.loads(old);candidate['services']['sandboxd']['environment'].update({'SANDBOXD_PREVIEW_TOKEN_SECRETS':key,'SANDBOXD_ENV_FILE':str(AUTH)})
 new=(json.dumps(candidate,indent=2)+'\n').encode()
 atomic(JOB/'runtime-compose.candidate.json',new)
 rendered=json.loads(run(['docker','compose','-p','src','--env-file','/opt/sandboxd/src/.env','-f','/opt/sandboxd/src/docker-compose.yml','-f',str(JOB/'runtime-compose.candidate.json'),'-f','/opt/sandboxd/deploy-state/active-images.json','config','--format','json']))
 assert rendered['services']['sandboxd']['environment']['SANDBOXD_PREVIEW_TOKEN_SECRETS']==key
 assert OVERRIDE.read_bytes()==old
 atomic(OVERRIDE,new)
 run(['docker','exec',CP,'sh','-c','mkdir -p /etc/sandboxd && ln -s /var/lib/sandboxd/cube-preview-auth.env /etc/sandboxd/sandboxd.env'])
 run(['docker','kill','--signal=HUP',CP])
 for i in range(20):
  code,body=request('/v1/sandboxes/'+SB+'/preview-access',token,'POST')
  if code==200:break
  time.sleep(.25)
 assert code==200 and json.loads(body).get('token')
 assert request('/v1/sandboxes/'+SB,token)[0]==200
 # Loopback requests can be exempt, so do not claim anonymous API isolation here.
 assert json.loads(run(['docker','inspect','src-sandboxd-1']))[0]['Id']==CP
 receipt={'controller_id':CP,'sandbox_id':SB,'app_id':APP,'preview_access_status':200,'authenticated_api_status':200,'controller_restarted':False,'api_credentials_preserved':True,'persistent_override_sha256':hashlib.sha256(new).hexdigest(),'auth_file_sha256':hashlib.sha256(raw).hexdigest(),'runtime_reload_used':True}
 atomic(JOB/'complete.json',(json.dumps(receipt,indent=2)+'\n').encode())
 print(json.dumps(receipt))
finally:
 if nested is not None:nested.close()
 for fd in fds:os.close(fd)
