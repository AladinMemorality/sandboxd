"""Fenced one-time enrollment of the tested VPS runtime resource budget."""
import contextlib,copy,fcntl,hashlib,importlib.util,json,os,pathlib,signal,sqlite3,subprocess,time,urllib.request
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');release=root/'weighted-release-02';os.umask(0o077)
spec=importlib.util.spec_from_file_location('maintenance','/usr/local/libexec/baarcha-cube-maintenance.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);b=m.b
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def run(a,timeout=180):return subprocess.check_output(a,stderr=subprocess.STDOUT,timeout=timeout)
def inspect():return json.loads(run(['docker','inspect','src-sandboxd-1']))[0]
def remote(code):return run(SSH+['python3 -c '+__import__('shlex').quote(code)])
def put(p,value):b.atomic(p,b.encoded(value))
def read(p):return json.loads(p.read_bytes())
def compose(*args):return run(['docker','compose','--project-directory','/opt/sandboxd/src','-f','/opt/sandboxd/src/docker-compose.yml','-f',str(b.COMPOSE),'-f',str(b.ACTIVE),*args])
def route(value):
 req=urllib.request.Request('http://127.0.0.1:2019/load',data=json.dumps(value).encode(),headers={'Content-Type':'application/json'},method='POST')
 with urllib.request.urlopen(req,timeout=10) as r:assert r.status==200
 assert b.strict(b.http('/config/',2019))==value
@contextlib.contextmanager
def database(write=False):
 c=sqlite3.connect(('file:/var/lib/sandboxd/state/sandboxd.db?mode=rw' if write else 'file:/var/lib/sandboxd/state/sandboxd.db?mode=ro'),uri=True,timeout=10)
 try:yield c
 finally:c.close()
def quiet(c):
 assert c.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0,'coding task in progress'
 assert c.execute("select count(*) from cube_admission where state='pending'").fetchone()[0]==0,'provider operation in progress'
 assert c.execute("select count(*) from cube_recovery where phase<>'complete'").fetchone()[0]==0,'recovery in progress'
def bindings(c):return c.execute('select sandbox_id,runtime_id,template_id,config_revision from runtime_binding order by sandbox_id').fetchall()
with b.locked():
 assert read(root/'profiles-ready.json')['complete']
 d=P('/opt/baarcha/operations/vps-density-20261008-02')
 assert read(d/'result.json')['cleanup_verified'] and read(d/'operator-exit.json')['exit_code']==0
 v=read(d/'final-verification.json');assert v['bindings_unchanged'] and v['quota_unchanged'] and not v['monitor_failures']
 job=root/'weighted-enrollment';job.mkdir(mode=0o700)
 before=inspect();assert before['Image']=='sha256:faf4ff62f0ccce9f828fad77024bd65355add0df2c118608505fa976238a7453' and before['State']['Running']
 oldenv=dict(x.split('=',1) for x in before['Config']['Env']);policy=json.loads(oldenv['SANDBOXD_CUBE_ADMISSION']);assert policy['max_active']==4 and not policy.get('resource_budget')
 fleet=json.loads(oldenv['SANDBOXD_CUBE_FLEET']);workers=[w for w in fleet['workers'] if w['id']=='vps'];assert len(workers)==1 and workers[0]['admission']==policy and all(w['draining'] for w in fleet['workers'] if w['id']!='vps')
 templates={}
 for label in ['react-vite-512','standard-1024','builder-3072']:
  result=json.loads(remote("import pathlib;print(pathlib.Path('/root/vps-50-profiles-20261008/"+label+"-result.json').read_text())"))
  assert result['job']['status']=='READY' and result['job']['ready_node_count']==1
  templates[label]=result['job']['template_id']
 policy.update(max_active=52,cpu_count=0,memory_mb=0,writable_disk_mb=0,host_cpu_millis=72000,host_memory_mb=45056)
 profiles={k:{'cpu_millis':500,'writable_disk_mb':10240,'kind':'runtime'} for k in policy['templates']}
 assert all(v=={'cpu_count':2,'memory_mb':2048} for v in policy['templates'].values())
 for label,cpu,memory,reservation,disk,kind in [('react-vite-512',1,512,100,4096,'runtime'),('standard-1024',1,1024,200,8192,'runtime'),('builder-3072',2,3072,2000,8192,'build')]:
  ident=templates[label];policy['templates'][ident]={'cpu_count':cpu,'memory_mb':memory};profiles[ident]={'cpu_millis':reservation,'writable_disk_mb':disk,'kind':kind}
 policy['resource_budget']={'cpu_millis':9000,'memory_mb':40960,'runtime_slots':50,'build_slots':2,'profiles':profiles}
 workers[0]['admission']=policy
 presets=json.loads(oldenv['SANDBOXD_CUBE_TEMPLATES']);presets['react-vite']=templates['react-vite-512']
 desired={'SANDBOXD_CUBE_ADMISSION':json.dumps(policy,separators=(',',':')),'SANDBOXD_CUBE_FLEET':json.dumps(fleet,separators=(',',':')),'SANDBOXD_CUBE_TEMPLATES':json.dumps(presets,separators=(',',':'))}
 candidate=(release/'image.id').read_text().strip();assert candidate.startswith('sha256:')
 originals={str(p):p.read_bytes() for p in (b.COMPOSE,b.ACTIVE,b.STOP)}
 for p in (b.COMPOSE,b.ACTIVE,b.STOP):b.atomic(job/(p.name+'.before'),originals[str(p)])
 stop=json.loads(originals[str(b.STOP)]);assert stop['controller_id']==before['Id'] and stop['admission']==json.loads(oldenv['SANDBOXD_CUBE_ADMISSION'])
 online=b.strict(b.http('/config/',2019));scope=read(root/'vps-resize-plan.PRIVATE.json')['routing'];scope['online_sha256']=hashlib.sha256(json.dumps(online,sort_keys=True,separators=(',',':')).encode()).hexdigest();routes=m.routing_variants(online,scope)
 put(job/'online.json',online);put(job/'offline.json',routes['offline']);put(job/'policy.json',policy);put(job/'templates.json',templates)
 with database() as c:
  quiet(c);baseline=bindings(c)
  assert c.execute("select profile,max_active from cube_admission_policy where worker_id='vps'").fetchone()==('cpu=2;memory_mb=2048',4)
  assert c.execute("select count(*) from cube_resource_budget where worker_id='vps'").fetchone()[0]==0
  assert all(r[0] in policy['templates'] for r in c.execute("select distinct template_id from cube_admission where worker_id='vps' and state<>'deleted'"))
  with sqlite3.connect(job/'before.PRIVATE.sqlite') as backup:c.backup(backup)
 timers={name:run(['systemctl','show',name,'-p','ActiveState','--value']).decode().strip() for name in b.TIMERS};put(job/'timers.json',timers)
 signal.signal(signal.SIGTERM,lambda*a:None);signal.signal(signal.SIGINT,lambda*a:None)
 try:
  route(routes['offline']);run(['systemctl','stop',*b.TIMERS]);time.sleep(3)
  with database() as c:quiet(c)
  run(['docker','update','--restart=no',before['Id']]);run(['docker','stop','--time=-1',before['Id']])
  cp=inspect();assert not cp['State']['Running'] and cp['State']['ExitCode']==0 and not cp['State']['OOMKilled']
  with database() as c:quiet(c);assert bindings(c)==baseline
  # The native worker quota is an upper bound on guest allocations. Physical
  # reservations, VM overhead, runtime slots and build slots remain durable.
  code='''import pathlib,json,yaml,hashlib,os
p=pathlib.Path('/usr/local/services/cubetoolbox/Cubelet/dynamicconf/conf.yaml');old=p.read_bytes();v=yaml.safe_load(old)
assert v['host']['quota']['mcpu_limit']==10000 and v['host']['quota']['mem_limit']=='10Gi'
r=pathlib.Path('/root/vps-weighted-enrollment-20261008');r.mkdir(mode=0o700);(r/'quota-before.PRIVATE.yaml').write_bytes(old)
v['host']['quota']['mcpu_limit']=72000;v['host']['quota']['mem_limit']='44Gi';new=yaml.safe_dump(v,sort_keys=False).encode();tmp=p.with_name('.weighted-new');tmp.write_bytes(new);tmp.chmod(0o600);os.replace(tmp,p)
m=pathlib.Path('/etc/baarcha-cube/lifecycle.json');raw=m.read_bytes();value=json.loads(raw)
if str(p) in value['artifacts']:
 assert value['artifacts'][str(p)]==hashlib.sha256(old).hexdigest();(r/'lifecycle-before.PRIVATE.json').write_bytes(raw)
 value['artifacts'][str(p)]=hashlib.sha256(new).hexdigest();tmp=m.with_name('.weighted-new');tmp.write_text(json.dumps(value));tmp.chmod(0o600);os.replace(tmp,m)
print(json.dumps({'quota_cpu':72000,'quota_mem_mb':45056}))
'''
  put(job/'native-quota.json',json.loads(remote(code)))
  with urllib.request.urlopen('http://10.254.240.1:13010/internal/v1/nodes/10.0.2.15',timeout=8) as response:node=json.load(response)
  assert node['InstanceID']=='10.0.2.15' and node['IP']=='10.0.2.15' and not node['SchedulingDisabled']
  put(job/'node-before.PRIVATE.json',node)
  body={'node_id':node['InstanceID'],'host_ip':node['IP'],'grpc_port':0,'labels':node['NodeLabels'],
   'capacity':{'milli_cpu':72000,'memory_mb':45056},'allocatable':{'milli_cpu':72000,'memory_mb':45056},
   'instance_type':node['InstanceType'],'cluster_label':node['ClusterLabel'],'quota_cpu':72000,'quota_mem_mb':45056,
   'create_concurrent_num':node['CreateConcurrentNum'],'max_mvm_num':node['MaxMvmLimit'],'versions':node['Versions'],'host_facts':node['HostFacts']}
  req=urllib.request.Request('http://10.254.240.1:13010/internal/v1/node-agent/nodes/register',data=json.dumps(body).encode(),headers={'Content-Type':'application/json'},method='POST')
  with urllib.request.urlopen(req,timeout=15) as response:assert response.status==200;response.read()
  # Dynamic quota may take a heartbeat to reach the shared scheduler.
  deadline=time.monotonic()+90
  while True:
   with urllib.request.urlopen('http://10.254.240.1:13010/internal/v1/nodes/10.0.2.15',timeout=8) as r:node=json.load(r)
   if node['QuotaCpu']==72000 and node['QuotaMem']==45056 and node['Healthy']:break
   assert time.monotonic()<deadline,'native quota has not converged';time.sleep(2)
  with database(True) as c:
   quiet(c);assert bindings(c)==baseline
   changed=c.execute("update cube_admission_policy set profile='resource-budget-v1',max_active=52 where worker_id='vps' and profile='cpu=2;memory_mb=2048' and max_active=4");assert changed.rowcount==1;c.commit()
  for p in (b.COMPOSE,b.ACTIVE):
   value=json.loads(originals[str(p)]);service=value['services']['sandboxd'];service['image']=candidate;service.setdefault('environment',{}).update(desired);put(p,value)
  for command in ['cube-worker-start','cube-worker-stop']:
   run(['install','-m','755',str(release/command),'/usr/local/libexec/baarcha-'+command])
  run(['install','-m','755',str(root/'lifecycle-tests/boot_transition.py'),'/usr/local/libexec/baarcha-cube-boot-transition.py'])
  compose('up','-d','--no-deps','--no-build','--pull','never','sandboxd')
  compose('up','-d','--no-deps','--no-build','--pull','never','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy')
  deadline=time.monotonic()+90
  while True:
   try:
    assert b.http('/readyz',9090).strip()==b'ready';break
   except Exception:
    assert time.monotonic()<deadline,'controller not ready';time.sleep(1)
  after=inspect();assert after['Image']==candidate and after['State']['Running']
  stop['admission']=policy;stop['controller_id']=after['Id'];put(b.STOP,stop)
  observation=json.loads(run(['/usr/local/libexec/baarcha-cube-worker-start','--observe']))
  assert observation['consistent'] and observation['max_active']==52 and observation['bindings']==7
  with database() as c:
   assert bindings(c)==baseline
   contract=json.loads(c.execute("select contract from cube_resource_budget where worker_id='vps'").fetchone()[0]);assert contract['budget']==policy['resource_budget'] and contract['templates']==policy['templates']
  for name,state in timers.items():
   if state=='active':run(['systemctl','start',name])
  route(online)
  result={'complete':True,'image':candidate,'controller_id':after['Id'],'runtime_slots':50,'build_slots':2,'memory_budget_mb':40960,'cpu_reservation_millis':9000,'bindings_preserved':len(baseline),'new_react_memory_mb':512,'workers_contacted':['vps'],'at':time.time()}
  put(job/'complete.json',result);print(json.dumps(result),flush=True)
 except BaseException as error:
  put(job/'pending-review.json',{'error':str(error)[:2000],'class':type(error).__name__,'at':time.time()})
  while True:time.sleep(30)
