"""Deploy the tested controller and hold empty B200 admission for expansion."""
import contextlib,copy,importlib.util,sqlite3
from pathlib import Path
HERE=Path(__file__).resolve().parent
BASE=HERE.parent/'controller-fleet-release.py'
spec=importlib.util.spec_from_file_location('fleet_release',BASE);f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)
b,m,c,x,need=f.b,f.m,f.c,f.x,f.need
CONFIG=HERE/'controller-adjustment.PRIVATE.json'
FILES=tuple(dict.fromkeys((*c.FILES,BASE,Path(__file__).resolve(),CONFIG,HERE/'cube-controller',HERE/'image.id',f.GUARD)))
KIND='current-generation-controller-fleet-expansion-hold'
def validate(plan):m.validate_plan(plan,kind=KIND,files=FILES)
class Host(f.Host):
 def validate_plan(self):validate(self.plan)
 def provider(self):
  actual=self.provider_counts();expected=self.plan['provider_terminal_counts']
  if actual!=expected:
   candidate=c.preview_snapshot_counts(actual,expected)
   self.bindings_readonly()
   for node,wanted in [('10.0.2.15',{v['runtime_id'] for v in self.plan['bindings']}),('10.254.240.2',set())]:
    inventory=f.native(18089,'/cube/sandbox/inventory?host_id='+node)
    need(inventory['ret']['ret_code']==200 and inventory['size']==1 and {v['sandbox_id'] for v in inventory['data']}==wanted,'Customer inventory changed during idle snapshot completion')
   self.plan['provider_terminal_counts']=candidate
  return m.verify_counts(actual,self.plan['provider_terminal_counts'])
 def preflight(self,defer_busy=False):
  self.release=b.strict(b.trusted(CONFIG));self.inputs();result=m.Host.preflight(self,defer_busy)
  self.environment=dict(v.split('=',1) for v in self.cp()['Config']['Env']);self.bridge.plan={'controller_image':self.e['controller_image']}
  need(self.bridge.recreated(self.environment)==self.e['controller_id'],'Controller contract changed')
  before=self.release['before'];need(b.strict(self.bridge.compose('config','--format','json'))==before,'Compose changed')
  wanted=copy.deepcopy(before);wanted['services']['sandboxd']['image']=self.release['image']
  env=wanted['services']['sandboxd']['environment'];fleet=b.strict(env['SANDBOXD_CUBE_FLEET'])
  need([(w['id'],w['admission']['max_active'],w['draining']) for w in fleet['workers']]==[('vps',4,False),('b200-01',46,False)],'Accepted fifty-slot baseline required')
  fleet['workers'][1]['draining']=True;env['SANDBOXD_CUBE_FLEET']=b.encoded(fleet).decode().strip()
  need(wanted==self.release['after'],'Only image and empty B200 drain may change')
  need(self.inspect_image()['Id']==self.release['image'],'Candidate image changed')
  self.guard=b.strict(b.trusted(f.GUARD));self.bridge.fresh(self.guard,0)
  with self.db() as db:
   self.accepted=[c.accepted_binding(db,row) for row in self.c['projects']]
   need(db.execute("SELECT count(*) FROM cube_admission WHERE worker_id='b200-01' AND state!='deleted'").fetchone()[0]==0,'B200 must have no customer binding')
  self.source_fence();return result
 def before_controller_stop(self):
  self.quiet_tasks();self.bindings_readonly();self.source_fence();self.provider()
  with self.db() as db:need(db.execute("SELECT count(*) FROM cube_admission WHERE worker_id='b200-01' AND state!='deleted'").fetchone()[0]==0,'B200 acquired a binding')
 def restore_controller(self):
  self.fence();self.restoration_scope();self.bindings_readonly()
  with self.db() as source,contextlib.closing(sqlite3.connect(self.job/'before-controller.PRIVATE.sqlite')) as target:source.backup(target)
  (self.job/'before-controller.PRIVATE.sqlite').chmod(0o600);old=self.cp(True)
  self.event('candidate-install-intent',{'image':self.release['image'],'b200_draining':True})
  for target in (b.COMPOSE,b.ACTIVE):
   raw=b.trusted(target);need(b.sha(raw)==self.plan['files'][str(target)],'Compose input changed');x.publish(self.job/('original-'+target.name),raw)
   value=b.strict(raw);value['services']['sandboxd']['image']=self.release['image']
   if target==b.ACTIVE:value['services']['sandboxd']['environment']['SANDBOXD_CUBE_FLEET']=self.release['after']['services']['sandboxd']['environment']['SANDBOXD_CUBE_FLEET']
   b.atomic(target,b.encoded(value));self.plan['files'][str(target)]=b.digest(target)
  need(b.strict(self.bridge.compose('config','--format','json'))==self.release['after'],'Rendered candidate changed')
  self.environment['SANDBOXD_CUBE_FLEET']=self.release['after']['services']['sandboxd']['environment']['SANDBOXD_CUBE_FLEET']
  self.e['controller_image']=self.release['image'];self.bridge.plan={'controller_image':self.release['image']};self.bridge.activate()
  self.bridge.compose('up','-d','--no-deps','--force-recreate','cube-management-master','cube-management-b200-proxy')
  ident=self.wait(lambda:self.bridge.recreated(self.environment),120);cp=self.inspect(ident)
  need(cp['Config']['Entrypoint']==old['Config']['Entrypoint'] and cp['Config']['Cmd']==old['Config']['Cmd'],'Entrypoint changed')
  key=lambda v:(v['Source'],v['Destination'],v['Type'],v['RW'])
  need(sorted(map(key,cp['Mounts']))==sorted(map(key,old['Mounts'])),'Mounts changed')
  stop=b.strict(b.trusted(b.STOP));need(stop['controller_id']==self.e['controller_id'],'STOP generation changed')
  x.publish(self.job/'original-stop.json',stop);stop['controller_id']=ident;b.atomic(b.STOP,b.encoded(stop));self.plan['files'][str(b.STOP)]=b.digest(b.STOP);self.e['controller_id']=ident
  need(Path('/proc/'+str(cp['State']['Pid'])+'/root'+str(c.SOCKET)).is_socket(),'Motion socket missing')
  self.wait(self.master_relay,90);self.ready_fence()
class Sequence:
 def __init__(self,h,event):self.h,self.event=h,event
 def stop(self):
  self.h.preflight();self.event('preflight-passed');self.event('drain-intent');self.h.drain();self.event('drained');return {'drained':True}
 def start(self,receipt):
  need(receipt=={'drained':True},'Drain required');h=self.h;h.restore_controller();h.verify_apis();h.ready_fence();self.event('reopen-intent');h.reopen();h.close_nested()
  result={'online_restored':True,'controller_id':h.e['controller_id'],'image':h.e['controller_image'],'customer_bindings':len(h.plan['bindings']),'b200_draining':True,'guest_power_operations':0}
  self.event('complete',result);return result
if __name__=='__main__':m.run_cli(Host,Sequence,validate,__doc__)
