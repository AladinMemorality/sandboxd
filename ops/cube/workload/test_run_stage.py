import copy,datetime,importlib.util,json,pathlib,types,unittest
from unittest.mock import patch
p=pathlib.Path(__file__).with_name('run-stage.py');spec=importlib.util.spec_from_file_location('stage',p);stage=importlib.util.module_from_spec(spec);spec.loader.exec_module(stage)
class StageTest(unittest.TestCase):
 def config(self):return {'controller_id':'c'*64,'controller_image':'sha256:'+'d'*64,'worker_boot_id':'worker','worker_node_id':'reviewed-node','fixture':{'max_active':6,'profile':'cpu2-mem2048','template_id':'tpl-'+'a'*24,'paused_baseline':{'runtime_id':'kept'}}}
 def receipt(self):return {'version':1,'outer_boot_id':'outer','worker_boot_id':'worker','controller_id':'c'*64,'started_boottime_ns':10,'controller_stopped':True,'canonical_charged':0,'canonical_active_tasks':0,'provider_active_jobs':0,'worker_tasks':0,'paused_baseline':{'runtime_id':'kept'},'native_quota':[14000,'14Gi'],'worker_node_id':'reviewed-node','profile':'cpu2-mem2048','template_id':'tpl-'+'a'*24}
 def test_receipt_fresh_and_exact(self):
  with patch.object(stage,'boot',return_value='outer'):
   stage.fresh(self.receipt(),self.config(),10)
   for field,value in [('controller_stopped',False),('canonical_charged',1),('canonical_active_tasks',1),('provider_active_jobs',1),('worker_tasks',1),('paused_baseline',None),('worker_boot_id','other'),('native_quota',[28000,'30Gi'])]:
    with self.subTest(field=field):
     receipt=self.receipt();receipt[field]=value
     with self.assertRaises(ValueError):stage.fresh(receipt,self.config(),10)
   for now in [9,300_000_000_011]:
    with self.assertRaises(ValueError):stage.fresh(self.receipt(),self.config(),now)
 def test_running_controller_false_flag_is_not_a_hold(self):
  row={'Id':'c'*64,'Image':'sha256:'+'d'*64,'State':{'Running':True,'Restarting':False},'Config':{'Env':['SANDBOXD_CUBE_ENABLED=false']}}
  with patch.object(stage,'command',return_value=json.dumps([row]).encode()):
   with self.assertRaises(ValueError):stage.controller_held(self.config())
  row['State']['Running']=False
  with patch.object(stage,'command',return_value=json.dumps([row]).encode()):stage.controller_held(self.config())
 def test_latency_and_cleanup_gates(self):
  result={'all_apps_memory_and_cpu_verified':True,'over_budget_refused':True,'cleanup_verified':True,'charged_remaining':0,'preparation_seconds':19,'measured_load_seconds':45,'rounds':[[],[{'HTTPMS':10,'StatusMS':5}]*20]}
  self.assertEqual(stage.latency_gate(result)['HTTPMS']['p95_ms'],10)
  for field,value in [('cleanup_verified',False),('preparation_seconds',20.01),('charged_remaining',1),('measured_load_seconds',44)]:
   changed=copy.deepcopy(result);changed[field]=value
   with self.assertRaises(ValueError):stage.latency_gate(changed)
  result['rounds'][1]=[{'HTTPMS':251,'StatusMS':5}]*20
  with self.assertRaises(ValueError):stage.latency_gate(result)
 def master(self):return {'ret':{'ret_code':200},'data':[{'InstanceID':'reviewed-node','Healthy':True,'ReportedReady':True,'QuotaCpu':14000,'QuotaMem':14336,'CreateConcurrentNum':1,'MaxMvmLimit':128,'MetricLocalUpdateAt':'2026-09-26T00:00:00Z'}]}
 def test_master_actual_quota_usage_identity_freshness(self):
  now=datetime.datetime(2026,9,26,tzinfo=datetime.timezone.utc)
  self.assertEqual(stage.validate_master_observation(self.master(),self.config(),now)['mcpu'],14000)
  for field,value in [('InstanceID','wrong'),('Healthy',False),('ReportedReady',False),('QuotaCpu',10000),('QuotaMem',10240),('QuotaCpuUsage',1),('QuotaMemUsage',1),('RealTimeCreateNum',1),('LocalCreateNum',1),('CreateConcurrentNum',2),('MetricLocalUpdateAt','2026-09-25T23:59:29Z'),('MetricLocalUpdateAt','2026-09-26T00:00:03Z')]:
   with self.subTest(field=field,value=value):
    data=self.master();data['data'][0][field]=value
    with self.assertRaises(ValueError):stage.validate_master_observation(data,self.config(),now)
  for rows in ([],self.master()['data']*2):
   data=self.master();data['data']=rows
   with self.assertRaises(ValueError):stage.validate_master_observation(data,self.config(),now)
 def test_explicit_profile_and_template_match(self):
  for profile,(cpu,memory) in stage.PROFILES.items():
   config=self.config();config['fixture']['profile']=profile;stage.validate_profile(config)
   item={'status':'READY','container_count':1,'resources':{'cpu':str(cpu*1000)+'m','mem':str(memory)+'Mi'},'network':{'denyOut':['0.0.0.0/0']},'writable_sizes':['10Gi']}
   stage.validate_template_observation(item,config)
   for field,value in [('status','PENDING'),('container_count',2),('resources',{'cpu':'4000m','mem':'4096Mi'}),('network',{}),('writable_sizes',['20Gi'])]:
    changed=copy.deepcopy(item);changed[field]=value
    with self.assertRaises(ValueError):stage.validate_template_observation(changed,config)
  for profile in ('','cpu2-mem1024','cpu8-mem8192'):
   config=self.config();config['fixture']['profile']=profile
   with self.assertRaises(ValueError):stage.validate_profile(config)
 def test_worker_preflight_observes_supported_read_only_interfaces(self):
  config=self.config();config['lcm_id']='e'*64
  master=self.master();master['data'][0]['MetricLocalUpdateAt']=datetime.datetime.now(datetime.timezone.utc).isoformat()
  result={'quota':{},'master':master,'template':{'status':'READY','container_count':1,'resources':{'cpu':'2000m','mem':'2048Mi'},'network':{'denyOut':['0.0.0.0/0']},'writable_sizes':['10Gi']},'worker_tasks':0,'provider_active_jobs':0,'lcm_frozen':True}
  def observe(args,**kwargs):
   payload=json.loads(kwargs['input']);compile(payload['code'],'preflight','exec')
   self.assertIn('http://127.0.0.1:8089/internal/node?',payload['code'])
   self.assertIn("['cubemastercli','tpl','info'",payload['code'])
   self.assertEqual(json.loads(payload['input'])['worker_node_id'],'reviewed-node')
   self.assertEqual(args[:len(stage.SSH)],stage.SSH)
   return types.SimpleNamespace(returncode=0,stdout=json.dumps(result).encode())
  with patch.object(stage.subprocess,'run',side_effect=observe):
   checked=stage.worker_preflight(config)
   self.assertEqual(checked['master']['used_mcpu'],0)
   self.assertEqual(checked['template']['profile'],'cpu2-mem2048')
 def test_only_reviewed_quota_choices(self):self.assertEqual(set(stage.QUOTAS),{4,6,8,12})
if __name__=='__main__':unittest.main()
