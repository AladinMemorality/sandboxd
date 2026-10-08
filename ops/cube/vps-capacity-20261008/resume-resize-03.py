"""Resume boot reconciliation only; the new VPS worker is already running."""
import importlib.util,json,os,signal,subprocess,time
from pathlib import Path
root=Path('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('resize',root/'resize-worker.py')
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r);p=r.p
job=p.m.ROOT/'maintenance/vps-capacity-resize-20261008-04'
plan=p.b.strict(p.b.trusted(root/'vps-resize-plan.PRIVATE.json'))
pending=sorted(job.glob('resume2-*-pending-operator-review.json'))
p.need(len(pending)==1,'exact pending event required')
p.need(p.b.strict(p.b.trusted(pending[0]))['value']['error']=='controller and native admission policy differ','different pending failure')
p.need(p.b.strict(p.b.http('/config/',2019))==p.b.strict(p.b.trusted(job/'offline.json')),'offline traffic fence lost')
cp=p.b.strict(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
p.need(cp['Id']==plan['expected']['controller_id'] and not cp['State']['Running'] and cp['HostConfig']['RestartPolicy']['Name']=='no','controller fence lost')
transition_plan=p.b.strict(p.b.trusted(job/'transition-plan.json'))
bridge=p.b.Host(transition_plan)
generation=bridge.generation(p.b.strict(p.b.trusted(job/'stop-wanted.json')))
p.need(generation['qemu_pid']==4174251,'new worker generation changed')
directory=p.m.ROOT/'boot-transitions'/job.name
p.need(directory.is_dir() and not (directory/'transition.json').exists(),'transition already initialized')
pid=int(subprocess.check_output(['systemctl','show','baarcha-vps-resize-continue-20261008-02','-p','MainPID','--value']))
p.need(b'/opt/baarcha/operations/vps-50-profiles-20261008/resume-resize-02.py\0' in Path('/proc/'+str(pid)+'/cmdline').read_bytes(),'old coordinator changed')
p.x.publish(job/'resume3-handoff.json',{'old_coordinator_pid':pid,'reason':'validate effective admission after compose overlays','worker_generation':generation})
os.kill(pid,signal.SIGUSR1)
until=time.monotonic()+10
while Path('/proc/'+str(pid)).exists():
    p.need(time.monotonic()<until,'old coordinator did not exit');time.sleep(.05)
with p.b.locked() as fds:
    events=[]
    def event(phase,value=None):
        p.x.publish(job/('resume3-%03d-%s.json'%(len(events),phase)),{'at':time.time(),'phase':phase,'value':value})
        events.append(phase);p.b.atomic(job/'current.json',p.b.encoded({'phase':phase,'locks_held':True,'continuation':True}))
    host=r.Host(plan,job,fds,event)
    host.routes={name:p.b.strict(p.b.trusted(job/(name+'.json'))) for name in ('online','offline','drain')}
    host.baseline=p.b.strict(p.b.trusted(job/'baseline.json'))
    host.want_stop=p.b.strict(p.b.trusted(job/'stop-wanted.json'))
    host.motion_baseline=p.b.strict(p.b.trusted(job/'pre-drain-evidence.json'))['direct_writers']['jobs']
    host.transition_plan=transition_plan
    signal.signal(signal.SIGTERM,lambda*a:None);signal.signal(signal.SIGINT,lambda*a:None)
    try:
        with bridge.worker_lock():result=p.b.transition(transition_plan,directory,bridge)
        host.transition_result=result;event('reconciled',result)
        host.capture_after();host.ready_fence();host.reopen();event('complete',result)
        print(json.dumps(result),flush=True)
    except BaseException as error:
        event('pending-operator-review',{'error_class':type(error).__name__,'error':str(error)[:2048]})
        while True:time.sleep(30)
