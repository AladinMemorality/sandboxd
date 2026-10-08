"""Raise only VPS stored VM count; keep RAM, CPU, disk and running-slot guards."""
import hashlib,importlib.util,json,os,pathlib,subprocess,urllib.request,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'stored-runtime-limit-256'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
def get():
 with urllib.request.urlopen('http://10.254.240.1:13010/internal/v1/nodes/10.0.2.15',timeout=10) as r:return json.load(r)
with b.locked():
 out.mkdir(mode=0o700);node=get();assert node['MaxMvmLimit']==128 and node['QuotaCpu']==72000 and node['QuotaMem']==45056 and node['Healthy']
 b.atomic(out/'node-before.PRIVATE.json',b.encoded(node))
 code='''import pathlib,json,yaml,hashlib,os,socket
assert socket.gethostname()=='baarcha-cube-worker-01'
p=pathlib.Path('/usr/local/services/cubetoolbox/Cubelet/dynamicconf/conf.yaml');old=p.read_bytes();v=yaml.safe_load(old)
assert v['host']['quota']['mcpu_limit']==72000 and v['host']['quota']['mem_limit']=='44Gi' and v['host']['quota']['mvm_limit']==128
r=pathlib.Path('/root/vps-stored-runtime-limit-256-20261008');r.mkdir(mode=0o700);(r/'config-before.PRIVATE.yaml').write_bytes(old)
v['host']['quota']['mvm_limit']=256;new=yaml.safe_dump(v,sort_keys=False).encode()
m=pathlib.Path('/etc/baarcha-cube/lifecycle.json');raw=m.read_bytes();value=json.loads(raw)
if str(p) in value['artifacts']:assert value['artifacts'][str(p)]==hashlib.sha256(old).hexdigest()
tmp=p.with_name('.stored-runtime-limit-new');tmp.write_bytes(new);tmp.chmod(0o600);os.replace(tmp,p)
if str(p) in value['artifacts']:
 (r/'lifecycle-before.PRIVATE.json').write_bytes(raw);value['artifacts'][str(p)]=hashlib.sha256(new).hexdigest();tmp=m.with_name('.stored-runtime-limit-new');tmp.write_text(json.dumps(value));tmp.chmod(0o600);os.replace(tmp,m)
print(json.dumps({'worker':'vps','max_stored_vms':256,'cpu_quota':72000,'memory_quota_mb':45056,'service_restart':False}))
'''
 result=subprocess.run(ssh+['python3 -'],input=code.encode(),capture_output=True,timeout=30);assert result.returncode==0,result.stderr.decode()[:500]
 body={'node_id':node['InstanceID'],'host_ip':node['IP'],'grpc_port':0,'labels':node['NodeLabels'],'capacity':{'milli_cpu':72000,'memory_mb':45056},'allocatable':{'milli_cpu':72000,'memory_mb':45056},'instance_type':node['InstanceType'],'cluster_label':node['ClusterLabel'],'quota_cpu':72000,'quota_mem_mb':45056,'create_concurrent_num':node['CreateConcurrentNum'],'max_mvm_num':256,'versions':node['Versions'],'host_facts':node['HostFacts']}
 req=urllib.request.Request('http://10.254.240.1:13010/internal/v1/node-agent/nodes/register',data=json.dumps(body).encode(),headers={'Content-Type':'application/json'},method='POST')
 with urllib.request.urlopen(req,timeout=15) as r:assert r.status==200;r.read()
 deadline=time.monotonic()+90
 while True:
  after=get()
  if after['MaxMvmLimit']==256 and after['Healthy']:break
  assert time.monotonic()<deadline;time.sleep(2)
 assert after['QuotaCpu']==node['QuotaCpu'] and after['QuotaMem']==node['QuotaMem']
 receipt=json.loads(result.stdout);receipt.update(converged=True,running_runtime_slots_unchanged=True,b200_contacted=False)
 b.atomic(out/'applied.json',b.encoded(receipt));print(json.dumps(receipt),flush=True)
