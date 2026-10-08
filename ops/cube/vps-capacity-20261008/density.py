import concurrent.futures,fcntl,hashlib,json,os,pathlib,shlex,sqlite3,subprocess,threading,time,urllib.request
ROOT=pathlib.Path('/opt/baarcha/operations/vps-density-20261008-01')
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-o','BatchMode=yes','-o','ConnectTimeout=5','root@127.0.0.1']
CG=pathlib.Path('/sys/fs/cgroup/system.slice/baarcha-cube-worker-01.service')
G=1024**3
os.umask(0o077)
def save(n,v):
 p=ROOT/n;t=p.with_suffix(p.suffix+'.tmp');t.write_text(json.dumps(v,indent=2)+'\n');os.replace(t,p)
def remote(code,timeout=15):return subprocess.check_output(SSH+['python3 -'],input=code.encode(),timeout=timeout)
def native():
 with urllib.request.urlopen('http://10.254.240.1:13010/internal/v1/nodes/10.0.2.15',timeout=8) as r:x=json.load(r)
 return {k:x.get(k) for k in ['InstanceID','QuotaCpu','QuotaMem','QuotaCpuUsage','QuotaMemUsage','Healthy','SchedulingDisabled','MetricUpdateAt']}
METRICS='''import json,pathlib,subprocess,os
p=pathlib.Path
def kv(s):return {a.split()[0].rstrip(":"):int(a.split()[1]) for a in s.splitlines() if len(a.split())>1 and a.split()[1].isdigit()}
m=kv(p("/proc/meminfo").read_text());v=kv(p("/proc/vmstat").read_text());s=os.statvfs("/data")
x={"available":m["MemAvailable"]*1024,"swap_used":(m["SwapTotal"]-m["SwapFree"])*1024,"oom":v["oom_kill"],"free_disk":s.f_bavail*s.f_frsize,"memory_psi":p("/proc/pressure/memory").read_text(),"cpu_psi":p("/proc/pressure/cpu").read_text(),"load":p("/proc/loadavg").read_text().split()[:3]}
t=subprocess.check_output(["ctr","--address","/data/cubelet/cubelet.sock","--namespace","default","tasks","list"],stderr=subprocess.DEVNULL,text=True)
x["tasks"]=[]
for line in t.splitlines()[1:]:
 a=line.split()
 if len(a)<3:continue
 try:r=kv(p("/proc/"+a[1]+"/smaps_rollup").read_text());x["tasks"].append({"id":a[0],"pid":int(a[1]),"state":a[2],"rss_bytes":r["Rss"]*1024,"pss_bytes":r["Pss"]*1024})
 except FileNotFoundError:pass
disks=[]
for task in x["tasks"]:
 for disk in p("/data/cubelet/storage/xfs/objects/volumes").glob("*/sb-"+task["id"]+"-rootfs*"):
  if disk.is_file():
   st=disk.stat();disks.append({"id":task["id"],"logical_bytes":st.st_size,"allocated_bytes":st.st_blocks*512})
x["disks"]=disks
print(json.dumps(x))
'''
def collect():
 def kv(path):return {a.split()[0].rstrip(':'):int(a.split()[1]) for a in path.read_text().splitlines() if len(a.split())>1 and a.split()[1].isdigit()}
 m=kv(pathlib.Path('/proc/meminfo'));v=kv(pathlib.Path('/proc/vmstat'))
 at=time.monotonic();worker=json.loads(remote(METRICS));times=[]
 for route in ['http://127.0.0.1:9090/healthz','https://baarcha.tn/']:
  began=time.monotonic()
  try:
   with urllib.request.urlopen(urllib.request.Request(route,headers={"User-Agent":"Mozilla/5.0 Baarcha-capacity-check"}),timeout=5) as r:code=r.status;r.read(4096)
  except Exception:code=0
  times.append({'http':code,'seconds':time.monotonic()-began})
 return {'at':time.time(),'outer':{'available':m['MemAvailable']*1024,'swap_used':(m['SwapTotal']-m['SwapFree'])*1024,'oom':v['oom_kill'],'memory_psi':pathlib.Path('/proc/pressure/memory').read_text()},'worker':worker,'cgroup':{'current':int((CG/'memory.current').read_text()),'events':kv(CG/'memory.events'),'cpu_stat':kv(CG/'cpu.stat'),'cpu_psi':(CG/'cpu.pressure').read_text()},'probes':times,'collection_seconds':time.monotonic()-at}
def bindings():
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:return db.execute('select sandbox_id,runtime_id from runtime_binding order by sandbox_id').fetchall()

def register_quota(mode):
 url='http://10.254.240.1:13010/internal/v1/nodes/10.0.2.15'
 with urllib.request.urlopen(url,timeout=8) as r: node=json.load(r)
 assert node['InstanceID']=='10.0.2.15' and node['IP']=='10.0.2.15' and not node['SchedulingDisabled']
 if mode=='raise':
  assert node['QuotaCpu']==10000 and node['QuotaMem']==10240
  save('node-before.PRIVATE.json',node)
  old={'node_id':node['InstanceID'],'host_ip':node['IP'],'grpc_port':0,
   'labels':node['NodeLabels'],'capacity':{'milli_cpu':node['CpuTotal']*1000,'memory_mb':node['MemMBTotal']},
   'allocatable':{'milli_cpu':node['CpuTotal']*1000,'memory_mb':node['MemMBTotal']},
   'instance_type':node['InstanceType'],'cluster_label':node['ClusterLabel'],
   'quota_cpu':node['QuotaCpu'],'quota_mem_mb':node['QuotaMem'],
   'create_concurrent_num':node['CreateConcurrentNum'],'max_mvm_num':node['MaxMvmLimit'],
   'versions':node['Versions'],'host_facts':node['HostFacts']}
  save('registration-before.PRIVATE.json',old)
  body=dict(old,quota_cpu=45000,quota_mem_mb=32768,capacity={'milli_cpu':45000,'memory_mb':32768},allocatable={'milli_cpu':45000,'memory_mb':32768})
 else:
  assert node['QuotaCpu'] in (10000,45000) and node['QuotaMem'] in (10240,32768)
  body=json.loads((ROOT/'registration-before.PRIVATE.json').read_text())
 req=urllib.request.Request('http://10.254.240.1:13010/internal/v1/node-agent/nodes/register',data=json.dumps(body).encode(),headers={'Content-Type':'application/json'},method='POST')
 with urllib.request.urlopen(req,timeout=15) as response:
  assert response.status==200;response.read()
 save('registration-'+mode+'.json',{'node':'10.0.2.15','quota_cpu':body['quota_cpu'],'quota_mem_mb':body['quota_mem_mb']})

def quota(mode):
 code='''import pathlib,yaml,json,os,tempfile,hashlib
p=pathlib.Path("/usr/local/services/cubetoolbox/Cubelet/dynamicconf/conf.yaml")
r=pathlib.Path("/root/vps-density-20261008-01");mode=MODE
if mode=="raise":
 r.mkdir(mode=0o700)
 old=p.read_bytes();v=yaml.safe_load(old);assert v["host"]["quota"]["mcpu_limit"]==10000 and v["host"]["quota"]["mem_limit"]=="10Gi"
 (r/"quota-before.PRIVATE.yaml").write_bytes(old);os.chmod(r/"quota-before.PRIVATE.yaml",0o600)
 v["host"]["quota"]["mcpu_limit"]=45000;v["host"]["quota"]["mem_limit"]="32Gi"
 new=yaml.safe_dump(v,sort_keys=False).encode();(r/"quota-during.PRIVATE.yaml").write_bytes(new)
else:
 assert p.read_bytes()==(r/"quota-during.PRIVATE.yaml").read_bytes(),"Concurrent quota edit; retain evidence"
 new=(r/"quota-before.PRIVATE.yaml").read_bytes()
fd,name=tempfile.mkstemp(dir=p.parent,prefix=".capacity-")
with os.fdopen(fd,"wb") as f:os.fchmod(f.fileno(),0o600);f.write(new);f.flush();os.fsync(f.fileno())
os.replace(name,p)
print(json.dumps({"mode":mode,"sha256":hashlib.sha256(new).hexdigest()}))
'''.replace('MODE',repr(mode))
 return json.loads(remote(code))

locks=[]
for path in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
 fd=os.open(path,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB);locks.append(fd)
assert not (ROOT/'admission.db').exists(),'Prior pilot needs reconciliation; do not retry blindly'
lease=subprocess.Popen(SSH+["flock -n /run/lock/cube-operator-acceptance.lock python3 -u -c "+shlex.quote('import sys; print("LOCKED",flush=True); sys.stdin.read()')],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
import select
assert select.select([lease.stdout],[],[],10)[0] and lease.stdout.readline()==b'LOCKED\n','worker lock unavailable'
cp=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
env=dict(v.split('=',1) for v in cp['Config']['Env']);assert cp['State']['Running']
policy=json.loads(env['SANDBOXD_CUBE_ADMISSION']);assert policy['node_id']=='10.0.2.15' and policy['max_active']==4
before=collect();save('baseline.json',before);baseline=bindings();save('bindings-before.PRIVATE.json',baseline)
assert all(x['http']==200 for x in before['probes'])
assert before['outer']['available']>12*G and before['worker']['available']>20*G and before['worker']['free_disk']>160*G
original=native();assert original['QuotaCpu']==10000 and original['QuotaMem']==10240
save('native-before.json',original)
template='tpl-78e4edb3d629465e9d8372c1'
policy.update(max_active=35,host_cpu_millis=45000,host_memory_mb=32768,cpu_count=0,memory_mb=0,writable_disk_mb=0,templates={template:{'cpu_count':1,'memory_mb':512}},resource_budget={'cpu_millis':7000,'memory_mb':22400,'runtime_slots':35,'build_slots':0,'profiles':{template:{'cpu_millis':100,'writable_disk_mb':4096,'kind':'runtime'}}})
save('config.PRIVATE.json',{'api_key':env['SANDBOXD_CUBE_API_KEY'],'admission':policy,'template':template})
stop=threading.Event();failures=[];process=None;raised=False
def monitor():
 bad=0
 while not stop.is_set():
  try:
   now=collect()
   with (ROOT/'metrics.jsonl').open('a') as f:f.write(json.dumps(now)+'\n')
   reasons=[]
   if now['outer']['available']<8*G or now['worker']['available']<6*G:reasons.append('memory headroom')
   for host in ['outer','worker']:
    if now[host]['oom']>before[host]['oom']:reasons.append(host+' OOM')
    if float(now[host]['memory_psi'].split('full avg10=')[1].split()[0])>1:reasons.append(host+' memory pressure')
   for k in ['max','oom','oom_kill']:
    if now['cgroup']['events'][k]>before['cgroup']['events'][k]:reasons.append('worker cgroup '+k)
   if now['worker']['swap_used']>0:reasons.append('worker swap')
   if now['outer']['swap_used']>before['outer']['swap_used']+512*1024**2:reasons.append('outer swap growth')
   if now['worker']['free_disk']<(48+4*12+35*4.5)*G:reasons.append('storage headroom')
   bad=bad+1 if any(x['http']!=200 or x['seconds']>1 for x in now['probes']) else 0
   if bad>=3:reasons.append('platform probes')
   if reasons:failures.extend(reasons);(ROOT/'STOP').write_text('\n'.join(reasons));return
  except Exception as e:failures.append(type(e).__name__);(ROOT/'STOP').write_text(type(e).__name__);return
  stop.wait(2)
thread=threading.Thread(target=monitor)
try:
 save('quota-raised.json',quota('raise'));raised=True
 register_quota('raise')
 deadline=time.monotonic()+60
 while True:
  actual=native()
  if actual['QuotaCpu']==45000 and actual['QuotaMem']==32768 and actual['Healthy']:break
  assert time.monotonic()<deadline,'Native dynamic quota did not update'
  time.sleep(2)
 save('native-during.json',actual);thread.start()
 with (ROOT/'operator.log').open('wb') as log:
  process=subprocess.Popen(['nsenter','-t',str(cp['State']['Pid']),'-n',str(ROOT/'operator')],stdout=log,stderr=subprocess.STDOUT)
  code=process.wait(timeout=900)
 save('operator-exit.json',{'exit_code':code})
finally:
 stop.set()
 if thread.is_alive():thread.join(20)
 if process is not None and process.poll() is None:
  (ROOT/'STOP').write_text('deadline');process.wait(timeout=180)
 if raised:
  # Never restore a smaller native quota over an unconfirmed live fixture.
  if process is not None:
   report=json.loads((ROOT/'result.json').read_text());assert report['cleanup_verified'],'retain expanded quota until fixture cleanup is reconciled'
  save('quota-restored.json',quota('restore'));register_quota('restore')
  deadline=time.monotonic()+60
  while True:
   actual=native()
   if actual['QuotaCpu']==10000 and actual['QuotaMem']==10240 and actual['Healthy']:break
   assert time.monotonic()<deadline,'Native quota restore not observed'
   time.sleep(2)
 after=collect();actual=native();save('after.json',after)
 save('final-verification.json',{'bindings_unchanged':bindings()==baseline,'quota_unchanged':actual['QuotaCpu']==10000 and actual['QuotaMem']==10240,'monitor_failures':failures,'customer_runtime_ids_before':[v['id'] for v in before['worker']['tasks']],'runtime_ids_after':[v['id'] for v in after['worker']['tasks']]})
 lease.stdin.close();lease.wait(timeout=10)
assert code==0 and not failures
assert json.loads((ROOT/'result.json').read_text())['cleanup_verified']
