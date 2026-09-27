"""Continue this exact fleet release with uninterrupted operator lock ownership."""
import copy,ctypes,importlib.util,json,os,signal,subprocess,time
from pathlib import Path
STAGE=Path('/opt/baarcha-bench/cube-fleet-20260927')
JOB=Path('/opt/baarcha-cube/worker-01/maintenance/fleet-capacity-release-01')
OUT=Path('/opt/baarcha-cube/worker-01/maintenance/fleet-capacity-finish-02')
MODULE=STAGE/'controller-fleet-release.py';PLAN=STAGE/'controller-fleet-plan.PRIVATE.json'
FIX='3ed4ac01d9bc90e5c7b45d93f6e428ca57d20a67f9759bc2969c3a49c8d3155e'
s=importlib.util.spec_from_file_location('release',MODULE);r=importlib.util.module_from_spec(s);s.loader.exec_module(r)
b,m,c,x=r.b,r.m,r.c,r.x
os.umask(0o077)
state=dict(v.split('=',1) for v in subprocess.check_output(['systemctl','show','baarcha-cube-fleet-capacity-release-02.service','-p','MainPID','-p','ActiveState','-p','InvocationID'],text=True).splitlines())
assert state==dict(MainPID='1801667',ActiveState='active',InvocationID='4f297a02fe63434293a72c6302e51bdc')
pid=int(state['MainPID']);ticks=x.ticks(pid);assert ticks=='501167496'
assert Path(f'/proc/{pid}/cmdline').read_bytes().split(b'\0')[:-1]==[b'/usr/bin/python3',str(MODULE).encode(),b'--plan',str(PLAN).encode(),b'--directory',str(JOB).encode(),b'--execute']
children=Path(f'/proc/{pid}/task/{pid}/children').read_text().split();assert children==['1801700']
child=int(children[0]);child_ticks=x.ticks(child);assert Path(f'/proc/{child}/exe').resolve()==Path('/usr/bin/ssh')
assert b.strict(b.trusted(JOB/'current.json'))['phase']=='pending-operator-review'
assert list((JOB/'fleet-acceptance').iterdir())==[], 'first acceptance must have failed before any create intent'
assert b.digest(r.ACCEPT)==FIX
prior=Path('/opt/baarcha-cube/worker-01/maintenance/fleet-capacity-finish-01')
assert b.strict(b.trusted(prior/'current.json'))['phase']=='continuation-pending'
assert b.strict(b.trusted(prior/'fleet-acceptance/result.json'))['cleanup_verified'] is True
prior_pid=1806781;prior_ticks='501187089'
assert x.ticks(prior_pid)==prior_ticks
assert Path(f'/proc/{prior_pid}/cmdline').read_bytes()==b'/usr/bin/python3\0/opt/baarcha-bench/cube-fleet-20260927/finish-fleet-release.py\0'
prior_handle=os.pidfd_open(prior_pid)
plan=b.strict(b.trusted(PLAN));r.validate(plan)
for path,digest in plan['files'].items():
 if path not in {str(r.ACCEPT),str(b.COMPOSE),str(b.ACTIVE),str(b.STOP),str(b.OFFLINE)}:assert m.file_digest(path)==digest
handle=os.pidfd_open(pid);child_handle=os.pidfd_open(child);libc=ctypes.CDLL(None,use_errno=True);fds=[]
for name in b.LOCKS:
 choices=[int(v.name) for v in Path(f'/proc/{pid}/fd').iterdir() if os.readlink(v)==name];assert len(choices)==1
 fd=libc.syscall(438,handle,choices[0],0)
 if fd<0:raise OSError(ctypes.get_errno(),'same-OFD transfer failed')
 fds.append(fd)
OUT.mkdir(mode=0o700)
def event(name,value=None):
 row=dict(version=1,at=time.time(),phase=name,value=value);x.publish(OUT/(str(time.time_ns())+'-'+name+'.json'),row);b.atomic(OUT/'current.json',b.encoded(row))
class Holder:
 def poll(self):
  assert x.ticks(pid)==ticks and x.ticks(child)==child_ticks
  return None
for sig in (signal.SIGTERM,signal.SIGINT):signal.signal(sig,lambda *_:None)
try:
 with b.locked(fds):
  event('continuous-lock-transfer',dict(parent=pid,child=child,acceptance_fix_sha256=FIX))
  h=r.Host(copy.deepcopy(plan),OUT,tuple(fds),event)
  h.e.update(controller_id='5912348b37866c34829724da380b44aac2070ac90261e666534fdbead8ebfa03')
  h.release=b.strict(b.trusted(r.CONFIG));h.guard=b.strict(b.trusted(r.GUARD))
  h.routes={name:b.strict(b.trusted(JOB/(name+'.json'))) for name in ('online','offline','drain')}
  for name,value in h.routes.items():x.publish(OUT/(name+'.json'),value)
  h.baseline=b.strict(b.trusted(JOB/'baseline.json'))
  h.bridge.nested=Holder();h.bridge.plan={'controller_image':h.e['controller_image']}
  h.environment=dict(v.split('=',1) for v in h.cp()['Config']['Env'])
  assert b.strict(h.bridge.compose('config','--format','json'))==h.release['after']
  h.inputs();h.same();h.source_fence();h.bindings_readonly();h.quiet_tasks();h.master_relay()
  assert b.strict(b.http('/config/',2019))==h.routes['offline']
  assert b.strict(b.trusted(b.STOP))['controller_id']==h.e['controller_id']
  h.motion_baseline=h.motion_jobs();assert h.motion_baseline['active_jobs']==0
  h.ready_fence();h.verify_apis();h.accept_fleet();h.verify_apis();h.ready_fence()
  event('reopen-intent');h.reopen()
  result=dict(online_restored=True,controller_id=h.e['controller_id'],image=h.e['controller_image'],customer_bindings=len(h.plan['bindings']),accepted_concurrent_sandboxes=50,existing_customer_guest_power_operations=0)
  x.publish(OUT/'services-restored.json',result)
  assert x.ticks(pid)==ticks and x.ticks(child)==child_ticks
  event('idle-parent-release-intent',dict(pid=pid,child=child,continuous_locks_retained=True))
  assert x.ticks(prior_pid)==prior_ticks
  signal.pidfd_send_signal(prior_handle,signal.SIGKILL)
  signal.pidfd_send_signal(handle,signal.SIGKILL);signal.pidfd_send_signal(child_handle,signal.SIGTERM)
  until=time.monotonic()+20
  while any(Path(f'/proc/{v}').exists() for v in (pid,child)):
   assert time.monotonic()<until;time.sleep(.2)
  h.bridge.nested=None
  with h.bridge.worker_lock():pass
  event('complete',result);x.publish(OUT/'complete.json',result);print(json.dumps(result))
except BaseException as error:
 import traceback
 event('continuation-pending',dict(error=str(error)[:1024],traceback=traceback.format_exc()))
 while True:time.sleep(30)
finally:
 for fd in fds:os.close(fd)
 os.close(handle);os.close(child_handle)
