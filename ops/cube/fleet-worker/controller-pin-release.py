"""Pin existing VPS admission before enabling a second Cube worker.

Uses the installed, reviewed controller-only drain/restore coordinator. The
private plan contains current generations and exact rendered Compose inputs.
No worker or guest power operation, B200 enrollment, or node uncordon occurs.
"""
import contextlib, copy, importlib.util, sqlite3
from pathlib import Path
HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('cohort','/opt/baarcha-bench/cube-final-customers-20260927-01/source/ops/cube/cutover/cohort.py')
c=importlib.util.module_from_spec(spec);spec.loader.exec_module(c)
b,m,x,need=c.b,c.m,c.x,c.need
CONFIG=HERE/'controller-release.PRIVATE.json'
FILES=tuple(dict.fromkeys((*c.FILES,Path(__file__).resolve(),CONFIG,HERE/'cube-controller',HERE/'image.id')))
KIND='current-generation-controller-vps-pin-release'
def validate(plan):m.validate_plan(plan,kind=KIND,files=FILES)
class Host(c.Host):
 def validate_plan(self):validate(self.plan)
 def preflight(self,defer_busy=False):
  self.release=b.strict(b.trusted(CONFIG));self.inputs()
  result=m.Host.preflight(self,defer_busy)
  self.environment=dict(v.split('=',1) for v in self.cp()['Config']['Env'])
  self.bridge.plan={'controller_image':self.e['controller_image']}
  need(self.bridge.recreated(self.environment)==self.e['controller_id'],'Controller baseline changed')
  need(self.environment['SANDBOXD_CUBE_ROLLOUT']=='global','Global Cube required')
  need(self.cp()['Config']['Entrypoint']==['/usr/local/bin/cube-controller'],'Cube controller required')
  need(b.strict(self.bridge.compose('config','--format','json'))==self.release['before'],'Current Compose changed')
  need(self.inspect_image()['Id']==self.release['image'],'Candidate image changed')
  before=self.release['before'];after=self.release['after'];wanted=copy.deepcopy(before)
  wanted['services']['sandboxd']['image']=self.release['image']
  env=wanted['services']['sandboxd']['environment']
  admission=b.strict(env['SANDBOXD_CUBE_ADMISSION'])
  need('node_id' not in admission and not env.get('SANDBOXD_CUBE_FLEET'),'Only first standalone VPS pin is supported')
  admission.update(node_id='10.0.2.15',host_cpu_millis=10000,host_memory_mb=10240)
  env['SANDBOXD_CUBE_ADMISSION']=b.encoded(admission).decode().strip()
  env['SANDBOXD_CUBE_MASTER_URL']='http://127.0.0.1:20889'
  relay=copy.deepcopy(before['services']['cube-management-api'])
  relay['image']=self.release['relay_image'];relay['command']=['master']
  relay['healthcheck']['test']=['CMD','/usr/local/bin/healthcheck.sh','master']
  wanted['services']['cube-management-master']=relay
  need(after==wanted,'Only reviewed VPS pin and private master relay may change')
  need(b.strict(self.command(['/usr/bin/docker','image','inspect',self.release['relay_image']]))[0]['Id']==self.release['relay_image'],'Relay image changed')
  with self.db() as db:
   need(dict(db.execute('SELECT runtime_provider,count(*) FROM sandbox GROUP BY runtime_provider'))=={'cube':len(self.plan['bindings'])},'Fleet changed')
   need(dict(db.execute('SELECT phase,count(*) FROM runtime_migration GROUP BY phase'))=={'complete':72},'Migration changed')
   self.accepted=[c.accepted_binding(db,row) for row in self.c['projects']]
  self.source_fence();return result
 def inspect_image(self):return b.strict(self.command(['/usr/bin/docker','image','inspect',self.release['image']]))[0]
 def before_controller_stop(self):
  # No guest or worker shutdown: only HTTP/task drain before replacing the controller.
  self.quiet_tasks();self.bindings_readonly();self.source_fence();self.provider()
 def restore_controller(self):
  self.fence();self.restoration_scope();self.bindings_readonly()
  with self.db() as source,contextlib.closing(sqlite3.connect(self.job/'before-controller.PRIVATE.sqlite')) as target:source.backup(target)
  (self.job/'before-controller.PRIVATE.sqlite').chmod(0o600)
  old_cp=self.cp(True)
  self.event('vps-pin-install-intent',{'image':self.release['image']})
  for target in (b.COMPOSE,b.ACTIVE):
   raw=b.trusted(target);need(b.sha(raw)==self.plan['files'][str(target)],'Compose layer changed')
   x.publish(self.job/('original-'+target.name),raw)
   value=b.strict(raw);value['services']['sandboxd']['image']=self.release['image']
   if target==b.ACTIVE:
    value['services']['sandboxd'].setdefault('environment',{}).update({k:self.release['after']['services']['sandboxd']['environment'][k] for k in ('SANDBOXD_CUBE_ADMISSION','SANDBOXD_CUBE_MASTER_URL')})
    need('cube-management-master' not in value['services'],'Master relay already configured')
    value['services']['cube-management-master']=self.release['after']['services']['cube-management-master']
   b.atomic(target,b.encoded(value))
   self.plan['files'][str(target)]=b.digest(target)
  rendered=b.strict(self.bridge.compose('config','--format','json'))
  need(rendered==self.release['after'],'Rendered release differs from reviewed plan')
  self.environment.update({k:rendered['services']['sandboxd']['environment'][k] for k in ('SANDBOXD_CUBE_ADMISSION','SANDBOXD_CUBE_MASTER_URL')})
  self.e['controller_image']=self.release['image'];self.bridge.plan={'controller_image':self.release['image']}
  self.bridge.activate()
  self.bridge.compose('up','-d','--no-deps','--force-recreate','cube-management-master')
  ident=self.wait(lambda:self.bridge.recreated(self.environment),120)
  cp=self.inspect(ident)
  need(cp['Config']['Entrypoint']==old_cp['Config']['Entrypoint'] and cp['Config']['Cmd']==old_cp['Config']['Cmd'],'Executable changed')
  key=lambda v:(v['Destination'],v['Source'],v['Type'],v['RW'])
  need(sorted(map(key,cp['Mounts']))==sorted(map(key,old_cp['Mounts'])),'Controller mounts changed')
  stop=b.strict(b.trusted(b.STOP));need(stop['controller_id']==self.e['controller_id'],'STOP identity changed')
  x.publish(self.job/'original-stop.json',stop);stop['controller_id']=ident;b.atomic(b.STOP,b.encoded(stop))
  self.e['controller_id']=ident;self.plan['files'][str(b.STOP)]=b.digest(b.STOP)
  pid=cp['State']['Pid'];need(Path('/proc/'+str(pid)+'/root'+str(c.SOCKET)).is_socket(),'Motion socket missing')
  self.wait(self.master_relay,90)
  self.event('vps-pin-controller-active',{'controller_id':ident,'image':self.release['image']})
  self.ready_fence()
 def master_relay(self):
  r=self.inspect('src-cube-management-master-1');h=r['HostConfig']
  need(r['Image']==self.release['relay_image'] and r['State']['Running'] and r['State']['Health']['Status']=='healthy','Master relay not healthy')
  need(h['NetworkMode']=='container:'+self.e['controller_id'] and h['ReadonlyRootfs'] and h['CapDrop']==['ALL'] and 'no-new-privileges:true' in h['SecurityOpt'],'Master relay isolation changed')
  need(r['Config']['User']=='65532:65532' and h['GroupAdd']==['982'] and not h['PortBindings'],'Master relay exposed')
  need([(v['Source'],v['Destination'],v['RW']) for v in r['Mounts']]==[('/run/cube-management','/run/cube-management',False)],'Master relay mounts changed')
  return True
 def controller_ready(self):
  return self.wait(lambda:super(Host,self).controller_ready(),30)
 def verify_apis(self):
  with self.db() as db:
   total=db.execute('SELECT count(*) FROM sandbox_runtime_accounting').fetchone()[0]
   need(total==len(self.plan['bindings']),'Accounting migration did not cover fleet')
   need(db.execute('SELECT count(*) FROM sandbox_runtime_accounting WHERE total_seconds<0').fetchone()[0]==0,'Invalid accounting')
   need(db.execute('SELECT worker_id,node_id FROM cube_worker_identity').fetchall()==[('vps','10.0.2.15')],'VPS node pin was not durably enrolled')
   need(db.execute("SELECT count(*) FROM cube_admission WHERE worker_id!='vps'").fetchone()[0]==0,'Unexpected non-VPS admission')
  need(self.motion_jobs()==self.motion_baseline,'Motion work changed')
  self.event('vps-pin-schema-verified',{'sandboxes':total,'node_id':'10.0.2.15','max_active':4})
class Sequence:
 def __init__(self,h,event):self.h,self.event=h,event
 def stop(self):
  self.h.preflight();self.event('preflight-passed');self.event('drain-intent');self.h.drain();self.event('drained');return {'drained':True}
 def start(self,receipt):
  need(receipt=={'drained':True},'Drain required');h=self.h
  h.restore_controller();h.verify_apis();h.ready_fence();self.event('reopen-intent');h.reopen();h.close_nested()
  result={'online_restored':True,'controller_id':h.e['controller_id'],'image':h.e['controller_image'],'sandboxes':len(h.plan['bindings']),'guest_power_operations':0}
  self.event('complete',result);return result
if __name__=='__main__':m.run_cli(Host,Sequence,validate,__doc__)
