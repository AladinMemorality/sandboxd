"""Enroll B200, accept fifty private apps while traffic is fenced, then reopen."""
import contextlib, copy, importlib.util, sqlite3, http.client, json, time
from pathlib import Path
HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('cohort','/opt/baarcha-bench/cube-final-customers-20260927-01/source/ops/cube/cutover/cohort.py')
c=importlib.util.module_from_spec(spec);spec.loader.exec_module(c)
b,m,x,need=c.b,c.m,c.x,c.need
CONFIG=HERE/'controller-fleet-release.PRIVATE.json'
GUARD=Path('/etc/baarcha-cube/storage-guard-b200.json')
OBSERVATION='/run/sandboxd-cube-storage-b200'
ACCEPT=HERE/'accept-fleet.py'
FILES=tuple(dict.fromkeys((*c.FILES,Path(__file__).resolve(),CONFIG,HERE/'cube-controller',HERE/'image.id',GUARD,ACCEPT)))
KIND='current-generation-controller-fleet-release'
def validate(plan):m.validate_plan(plan,kind=KIND,files=FILES)
def native(port,path,method='GET'):
 conn=http.client.HTTPConnection('10.254.240.1',port,timeout=15)
 try:
  conn.request(method,path);response=conn.getresponse();need(response.status==200,'Native control request failed');return json.loads(response.read(1024*1024))
 finally:conn.close()
def wanted_config(before,guard):
 wanted=copy.deepcopy(before);service=wanted['services']['sandboxd'];env=service['environment']
 admission=b.strict(env['SANDBOXD_CUBE_ADMISSION'])
 need(admission['node_id']=='10.0.2.15' and admission['max_active']==4 and not env.get('SANDBOXD_CUBE_FLEET'),'Pinned VPS-only baseline required')
 b200=copy.deepcopy(admission);b200.update(node_id='10.254.240.2',max_active=46,host_cpu_millis=106000,host_memory_mb=114688,storage_guard=guard)
 fleet={'version':1,'master_url':env['SANDBOXD_CUBE_MASTER_URL'],'workers':[
  {'id':'vps','proxy_url':env['SANDBOXD_CUBE_PROXY_URL'],'draining':False,'admission':admission},
  {'id':'b200-01','proxy_url':'http://127.0.0.1:28080','draining':False,'admission':b200}]}
 env['SANDBOXD_CUBE_FLEET']=b.encoded(fleet).decode().strip()
 service['volumes'].append({'type':'bind','source':OBSERVATION,'target':OBSERVATION,'read_only':True,'bind':{}})
 relay=copy.deepcopy(before['services']['cube-management-master']);relay['command']=['b200-proxy'];relay['healthcheck']['test']=['CMD','/usr/local/bin/healthcheck.sh','b200-proxy']
 wanted['services']['cube-management-b200-proxy']=relay
 return wanted
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
  before=self.release['before'];after=self.release['after'];self.guard=b.strict(b.trusted(GUARD))
  need(after==wanted_config(before,self.guard),'Only reviewed fleet, storage mount and proxy relay may change')
  need(self.release['image']==self.e['controller_image'],'Fleet release must preserve the accepted executable')
  need(b.strict(self.command(['/usr/bin/docker','image','inspect',self.release['relay_image']]))[0]['Id']==self.release['relay_image'],'Relay image changed')
  with self.db() as db:
   need(dict(db.execute('SELECT runtime_provider,count(*) FROM sandbox GROUP BY runtime_provider'))=={'cube':len(self.plan['bindings'])},'Fleet changed')
   need(dict(db.execute('SELECT phase,count(*) FROM runtime_migration GROUP BY phase'))=={'complete':72},'Migration changed')
   self.accepted=[c.accepted_binding(db,row) for row in self.c['projects']]
   need(db.execute('SELECT coalesce(sum(charged),0) FROM cube_admission').fetchone()[0]==0,'All existing customer sandboxes must already be idle for capacity acceptance')
  self.bridge.fresh(self.guard,0)
  node=native(13010,'/internal/v1/nodes/10.254.240.2')
  need(node['Healthy'] and node['SchedulingDisabled'],'Healthy held B200 required')
  need(set(node['LocalTemplates'])==set(b.strict(self.environment['SANDBOXD_CUBE_ADMISSION'])['templates']),'All reviewed templates must be ready on B200')
  inventory=native(18089,'/cube/sandbox/inventory?host_id=10.254.240.2')
  need(inventory['ret']['ret_code']==200 and inventory['size']==1 and inventory['data']==[],'B200 must be empty before enrollment')
  self.source_fence();return result
 def inspect_image(self):return b.strict(self.command(['/usr/bin/docker','image','inspect',self.release['image']]))[0]
 def before_controller_stop(self):
  # No guest or worker shutdown: only HTTP/task drain before replacing the controller.
  self.quiet_tasks();self.bindings_readonly();self.source_fence();self.provider()
  with self.db() as db:need(db.execute('SELECT coalesce(sum(charged),0) FROM cube_admission').fetchone()[0]==0,'Active customer work changed during drain')
 def restore_controller(self):
  self.fence();self.restoration_scope();self.bindings_readonly()
  with self.db() as source,contextlib.closing(sqlite3.connect(self.job/'before-controller.PRIVATE.sqlite')) as target:source.backup(target)
  (self.job/'before-controller.PRIVATE.sqlite').chmod(0o600)
  old_cp=self.cp(True)
  self.event('fleet-install-intent',{'image':self.release['image']})
  for target in (b.COMPOSE,b.ACTIVE):
   raw=b.trusted(target);need(b.sha(raw)==self.plan['files'][str(target)],'Compose layer changed')
   x.publish(self.job/('original-'+target.name),raw)
   value=b.strict(raw);value['services']['sandboxd']['image']=self.release['image']
   if target==b.ACTIVE:
    value['services']['sandboxd'].setdefault('environment',{})['SANDBOXD_CUBE_FLEET']=self.release['after']['services']['sandboxd']['environment']['SANDBOXD_CUBE_FLEET']
    value['services']['sandboxd']['volumes']=self.release['after']['services']['sandboxd']['volumes']
    need('cube-management-b200-proxy' not in value['services'],'B200 proxy already configured')
    value['services']['cube-management-b200-proxy']=self.release['after']['services']['cube-management-b200-proxy']
   b.atomic(target,b.encoded(value))
   self.plan['files'][str(target)]=b.digest(target)
  rendered=b.strict(self.bridge.compose('config','--format','json'))
  need(rendered==self.release['after'],'Rendered release differs from reviewed plan')
  self.environment['SANDBOXD_CUBE_FLEET']=rendered['services']['sandboxd']['environment']['SANDBOXD_CUBE_FLEET']
  self.e['controller_image']=self.release['image'];self.bridge.plan={'controller_image':self.release['image']}
  self.bridge.activate()
  self.bridge.compose('up','-d','--no-deps','--force-recreate','cube-management-master','cube-management-b200-proxy')
  ident=self.wait(lambda:self.bridge.recreated(self.environment),120)
  cp=self.inspect(ident)
  need(cp['Config']['Entrypoint']==old_cp['Config']['Entrypoint'] and cp['Config']['Cmd']==old_cp['Config']['Cmd'],'Executable changed')
  key=lambda v:(v['Destination'],v['Source'],v['Type'],v['RW'])
  need(sorted(map(key,cp['Mounts']))==sorted([*map(key,old_cp['Mounts']),(OBSERVATION,OBSERVATION,'bind',False)]),'Controller mounts changed beyond B200 observation')
  stop=b.strict(b.trusted(b.STOP));need(stop['controller_id']==self.e['controller_id'],'STOP identity changed')
  x.publish(self.job/'original-stop.json',stop);stop['controller_id']=ident;b.atomic(b.STOP,b.encoded(stop))
  self.e['controller_id']=ident;self.plan['files'][str(b.STOP)]=b.digest(b.STOP)
  pid=cp['State']['Pid'];need(Path('/proc/'+str(pid)+'/root'+str(c.SOCKET)).is_socket(),'Motion socket missing')
  self.wait(self.master_relay,90)
  self.event('fleet-controller-active',{'controller_id':ident,'image':self.release['image']})
  self.ready_fence()
 def master_relay(self):
  for name in ('master','b200-proxy'):self.check_relay(name)
  return True
 def check_relay(self,name):
  r=self.inspect('src-cube-management-'+name+'-1');h=r['HostConfig']
  need(r['Image']==self.release['relay_image'] and r['State']['Running'] and r['State']['Health']['Status']=='healthy','Master relay not healthy')
  need(h['NetworkMode']=='container:'+self.e['controller_id'] and h['ReadonlyRootfs'] and h['CapDrop']==['ALL'] and 'no-new-privileges:true' in h['SecurityOpt'],'Master relay isolation changed')
  need(r['Config']['User']=='65532:65532' and h['GroupAdd']==['982'] and not h['PortBindings'],'Master relay exposed')
  need([(v['Source'],v['Destination'],v['RW']) for v in r['Mounts']]==[('/run/cube-management','/run/cube-management',False)],'Master relay mounts changed')
  return True
 def controller_ready(self):
  # The retained single-worker CLI cannot observe two policy rows. Prove the
  # unchanged customer bindings directly against both native node inventories.
  self.same()
  need(self.bridge.recreated(self.environment)==self.e['controller_id'],'Controller or relay contract changed')
  need(not Path('/var/lib/sandboxd/state/sandboxd.db.worker-stop.json').exists(),'Unexpected worker stop marker')
  self.bindings_readonly();self.source_fence()
  self.bridge.fresh(b.strict(b.trusted(b.GUARD)),0);self.bridge.fresh(self.guard,0)
  for node,expected in [('10.0.2.15',{v['runtime_id'] for v in self.plan['bindings']}),('10.254.240.2',set())]:
   inventory=native(18089,'/cube/sandbox/inventory?host_id='+node)
   need(inventory['ret']['ret_code']==200 and inventory['size']==1 and {v['sandbox_id'] for v in inventory['data']}==expected,'Native node inventory differs from retained customer bindings')
  return True
 def verify_apis(self):
  with self.db() as db:
   total=db.execute('SELECT count(*) FROM sandbox_runtime_accounting').fetchone()[0]
   need(total==len(self.plan['bindings']),'Accounting migration did not cover fleet')
   need(db.execute('SELECT count(*) FROM sandbox_runtime_accounting WHERE total_seconds<0').fetchone()[0]==0,'Invalid accounting')
   need(db.execute('SELECT worker_id,node_id FROM cube_worker_identity ORDER BY worker_id').fetchall()==[('b200-01','10.254.240.2'),('vps','10.0.2.15')],'Worker identities not durably enrolled')
   need(db.execute('SELECT worker_id,max_active FROM cube_admission_policy ORDER BY worker_id').fetchall()==[('b200-01',46),('vps',4)],'Fleet capacity not enrolled')
   need(db.execute("SELECT count(*) FROM cube_admission WHERE worker_id!='vps' AND state!='deleted'").fetchone()[0]==0,'B200 fixture remains')
  need(self.motion_jobs()==self.motion_baseline,'Motion work changed')
  self.event('fleet-schema-verified',{'sandboxes':total,'max_active':50})
 def accept_fleet(self):
  self.ready_fence();self.bridge.fresh(self.guard,0)
  native(13010,'/internal/v1/nodes/10.254.240.2/isolation','DELETE');time.sleep(5)
  try:
   self.command(['/usr/bin/python3',str(ACCEPT),str(self.job)],1800)
   result=b.strict(b.trusted(self.job/'fleet-acceptance/result.json'))
   need(result['complete'] and result['cleanup_verified'] and result['overflow_refused'],'Live fifty-slot acceptance incomplete')
  except BaseException:
   native(13010,'/internal/v1/nodes/10.254.240.2/isolation','PUT');raise
  self.event('fifty-live-apps-accepted',{'count':50,'cleanup_verified':True})
class Sequence:
 def __init__(self,h,event):self.h,self.event=h,event
 def stop(self):
  self.h.preflight();self.event('preflight-passed');self.event('drain-intent');self.h.drain();self.event('drained');return {'drained':True}
 def start(self,receipt):
  need(receipt=={'drained':True},'Drain required');h=self.h
  h.restore_controller();h.verify_apis();h.accept_fleet();h.verify_apis();h.ready_fence();self.event('reopen-intent');h.reopen();h.close_nested()
  result={'online_restored':True,'controller_id':h.e['controller_id'],'image':h.e['controller_image'],'sandboxes':len(h.plan['bindings']),'existing_customer_guest_power_operations':0}
  self.event('complete',result);return result
if __name__=='__main__':m.run_cli(Host,Sequence,validate,__doc__)
