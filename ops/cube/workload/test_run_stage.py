import copy,importlib.util,json,pathlib,unittest
from unittest.mock import patch
p=pathlib.Path(__file__).with_name('run-stage.py');spec=importlib.util.spec_from_file_location('stage',p);stage=importlib.util.module_from_spec(spec);spec.loader.exec_module(stage)
class StageTest(unittest.TestCase):
 def config(self):return {'controller_id':'c'*64,'controller_image':'sha256:'+'d'*64,'worker_boot_id':'worker','fixture':{'max_active':6,'paused_baseline':{'runtime_id':'kept'}}}
 def receipt(self):return {'version':1,'outer_boot_id':'outer','worker_boot_id':'worker','controller_id':'c'*64,'started_boottime_ns':10,'controller_stopped':True,'canonical_charged':0,'canonical_active_tasks':0,'provider_active_jobs':0,'worker_tasks':0,'paused_baseline':{'runtime_id':'kept'},'native_quota':[14000,'14Gi']}
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
 def test_only_reviewed_quota_choices(self):self.assertEqual(set(stage.QUOTAS),{4,6,8,12})
if __name__=='__main__':unittest.main()
