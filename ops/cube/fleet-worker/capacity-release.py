"""Raise the empty B200 partition to 96 and isolate the 100 owned test apps."""
import contextlib,copy,importlib.util,sqlite3
from pathlib import Path
HERE=Path(__file__).resolve().parent
SPEC=HERE.parent/'preview-renewal-02/controller-adjustment.py'
spec=importlib.util.spec_from_file_location('adjustment',SPEC);a=importlib.util.module_from_spec(spec);spec.loader.exec_module(a)
f,b,m,c,x,need=a.f,a.b,a.m,a.c,a.x,a.need
CONFIG=HERE/'capacity-release.PRIVATE.json'
COPIES=HERE/'plan.PRIVATE.json'
FILES=tuple(dict.fromkeys((*c.FILES,SPEC,a.BASE,Path(__file__).resolve(),CONFIG,COPIES,HERE/'cube-controller',HERE/'image.id',HERE/'release-smoke.json',f.GUARD)))
KIND='current-generation-controller-capacity-100'
def validate(plan):m.validate_plan(plan,kind=KIND,files=FILES)
def wanted_config(before,guard,image,ids):
 result=copy.deepcopy(before);service=result['services']['sandboxd'];service['image']=image
 fleet=b.strict(service['environment']['SANDBOXD_CUBE_FLEET'])
 need([(w['id'],w['admission']['max_active'],w['draining']) for w in fleet['workers']]==[('vps',4,False),('b200-01',46,True)],'Drained fifty-slot baseline required')
 vps,b200=fleet['workers'];b200['draining']=False;b200['admission'].update(max_active=96,host_cpu_millis=221000,host_memory_mb=212992,storage_guard=guard)
 # Fill B200 first during preparation; the VPS remains available for its customers.
 fleet['workers']=[b200,vps]
 service['environment']['SANDBOXD_CUBE_FLEET']=b.encoded(fleet).decode().strip()
 service['environment']['SANDBOXD_CUBE_OFFLINE_APPS']=b.encoded(ids).decode().strip()
 return result
class Host(a.Host):
 def validate_plan(self):validate(self.plan)
 def preflight(self,defer_busy=False):
  self.release=b.strict(b.trusted(CONFIG));self.inputs();result=m.Host.preflight(self,defer_busy)
  self.environment=dict(v.split('=',1) for v in self.cp()['Config']['Env']);self.bridge.plan={'controller_image':self.e['controller_image']}
  need(self.bridge.recreated(self.environment)==self.e['controller_id'],'Controller changed')
  self.guard=b.strict(b.trusted(f.GUARD));self.bridge.fresh(self.guard,0)
  copies=b.strict(b.trusted(COPIES));ids=[v['id'] for v in copies['apps']]
  need(len(ids)==len(set(ids))==100 and len(copies['sources'])==74,'All 100 private app IDs required')
  with self.db() as db:
   need(db.execute("SELECT count(*) FROM cube_admission WHERE worker_id='b200-01' AND state!='deleted'").fetchone()[0]==0,'B200 must be empty')
   for row in copies['apps']:
    need(db.execute('SELECT external_user_id,external_project_id FROM app WHERE id=?',(row['id'],)).fetchone()==('operator:fleet-100',row['external_project_id']),'Operator copy ownership differs')
  need(b.strict(self.bridge.compose('config','--format','json'))==self.release['before'],'Compose changed')
  need(self.release['after']==wanted_config(self.release['before'],self.guard,self.release['image'],ids),'Only reviewed fleet and copy isolation changes allowed')
  need(self.inspect_image()['Id']==self.release['image'],'Image changed')
  smoke=b.strict(b.trusted(HERE/'release-smoke.json'));need(smoke['image']==self.release['image'] and smoke['cgo_sqlite_verified'],'Actual executable SQLite smoke required')
  node=f.native(13010,'/internal/v1/nodes/10.254.240.2');need(node['Healthy'] and node['SchedulingDisabled'],'Healthy held B200 required')
  inv=f.native(18089,'/cube/sandbox/inventory?host_id=10.254.240.2');need(inv['ret']['ret_code']==200 and inv['size']==1 and inv['data']==[],'Empty B200 native inventory required')
  self.source_fence();return result
 def restore_controller(self):
  self.fence();self.bindings_readonly()
  with self.db() as source,contextlib.closing(sqlite3.connect(self.job/'before-controller.PRIVATE.sqlite')) as target:source.backup(target)
  (self.job/'before-controller.PRIVATE.sqlite').chmod(0o600);old=self.cp(True)
  self.event('capacity-install-intent',{'image':self.release['image'],'vps':4,'b200':96,'offline_apps':100})
  # Exact offline CAS; the process-lifetime DB lock is available only after clean shutdown.
  import fcntl,os
  fd=os.open('/var/lib/sandboxd/state/sandboxd.db.maintenance.lock',os.O_RDWR|os.O_NOFOLLOW)
  try:
   fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
   with sqlite3.connect('/var/lib/sandboxd/state/sandboxd.db') as db:
    db.execute('BEGIN IMMEDIATE')
    need(db.execute("SELECT count(*) FROM cube_admission WHERE worker_id='b200-01' AND state!='deleted'").fetchone()[0]==0,'B200 changed while stopped')
    need(db.execute("UPDATE cube_admission_policy SET max_active=96 WHERE singleton=1 AND worker_id='b200-01' AND max_active=46 AND profile='cpu=2;memory_mb=2048'").rowcount==1,'Exact 46-slot policy CAS required')
  finally:os.close(fd)
  for target in (b.COMPOSE,b.ACTIVE):
   raw=b.trusted(target);need(b.sha(raw)==self.plan['files'][str(target)],'Compose changed');x.publish(self.job/('original-'+target.name),raw)
   value=b.strict(raw);value['services']['sandboxd']['image']=self.release['image']
   if target==b.ACTIVE:
    for key in ('SANDBOXD_CUBE_FLEET','SANDBOXD_CUBE_OFFLINE_APPS'):value['services']['sandboxd']['environment'][key]=self.release['after']['services']['sandboxd']['environment'][key]
   b.atomic(target,b.encoded(value));self.plan['files'][str(target)]=b.digest(target)
  need(b.strict(self.bridge.compose('config','--format','json'))==self.release['after'],'Rendered candidate differs')
  for key in ('SANDBOXD_CUBE_FLEET','SANDBOXD_CUBE_OFFLINE_APPS'):self.environment[key]=self.release['after']['services']['sandboxd']['environment'][key]
  self.e['controller_image']=self.release['image'];self.bridge.plan={'controller_image':self.release['image']};self.bridge.activate()
  self.bridge.compose('up','-d','--no-deps','--force-recreate','cube-management-master','cube-management-b200-proxy')
  ident=self.wait(lambda:self.bridge.recreated(self.environment),120);cp=self.inspect(ident)
  need(cp['Config']['Entrypoint']==old['Config']['Entrypoint'] and cp['Config']['Cmd']==old['Config']['Cmd'],'Entrypoint changed')
  key=lambda v:(v['Source'],v['Destination'],v['Type'],v['RW'])
  need(sorted(map(key,cp['Mounts']))==sorted(map(key,old['Mounts'])),'Mounts changed')
  stop=b.strict(b.trusted(b.STOP));need(stop['controller_id']==self.e['controller_id'],'STOP pin changed')
  x.publish(self.job/'original-stop.json',stop);stop['controller_id']=ident;b.atomic(b.STOP,b.encoded(stop));self.e['controller_id']=ident
  self.wait(self.master_relay,90);self.ready_fence()
 def verify_apis(self):
  with self.db() as db:
   need(db.execute('SELECT worker_id,max_active FROM cube_admission_policy ORDER BY worker_id').fetchall()==[('b200-01',96),('vps',4)],'Durable 100-slot policy differs')
   need(db.execute("SELECT count(*) FROM cube_admission WHERE worker_id='b200-01' AND state!='deleted'").fetchone()[0]==0,'Unexpected B200 binding before test')
   need(db.execute('SELECT count(*) FROM sandbox_runtime_accounting').fetchone()[0]==74,'Customer accounting differs')
  need(self.motion_jobs()==self.motion_baseline,'Motion jobs changed')
class Sequence:
 def __init__(self,h,event):self.h,self.event=h,event
 def stop(self):
  self.h.preflight();self.event('preflight-passed');self.event('drain-intent');self.h.drain();self.event('drained');return {'drained':True}
 def start(self,receipt):
  need(receipt=={'drained':True},'Drain required');h=self.h
  h.restore_controller();h.verify_apis();h.ready_fence()
  f.native(13010,'/internal/v1/nodes/10.254.240.2/isolation','DELETE')
  self.event('reopen-intent');h.reopen();h.close_nested()
  result=dict(online_restored=True,controller_id=h.e['controller_id'],image=h.e['controller_image'],configured_active_capacity=100,capacity_test_complete=False,customer_bindings=len(h.plan['bindings']))
  self.event('complete',result);return result
if __name__=='__main__':m.run_cli(Host,Sequence,validate,__doc__)

