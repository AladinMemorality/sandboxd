"""Continue the exact cleanly stopped VPS generation after a reader-mode fix."""
import hashlib,importlib.util,json,os,signal,subprocess,time
from pathlib import Path
root=Path('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('resize',root/'resize-worker.py')
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r);p=r.p
job=p.m.ROOT/'maintenance/vps-capacity-resize-20261008-04'
plan=p.b.strict(p.b.trusted(root/'vps-resize-plan.PRIVATE.json'))
pending=p.b.strict(p.b.trusted(job/'008-pending-operator-review.json'))
p.need(pending['value']['error']=='private file must be root0600','different pending failure')
p.need(not (job/'supervisor-before.py').exists(),'resize already started')
receipt=p.b.strict(p.b.trusted(job/'supervisor-clean.json'))
inventory=p.b.strict(p.b.trusted(job/'pre-drain-evidence.json'))['inventory']
status=p.b.strict(p.b.trusted(p.m.ROOT/'lifecycle-status.json'))
digest=hashlib.sha256(json.dumps(receipt['proof'],sort_keys=True,separators=(',',':')).encode()).hexdigest()
clean=p.m.ROOT/('clean-stop-'+digest+'.json')
p.validate_clean(receipt,clean,plan['expected'],inventory,status)
p.need(status==p.b.strict(p.b.trusted(job/'supervisor-stopped-status.json')),'stopped generation changed')
p.need(all(not Path('/proc/'+str(plan['expected'][k])).exists() for k in ('qemu_pid','supervisor_pid')),'old worker still exists')
p.need(p.b.strict(p.b.http('/config/',2019))==p.b.strict(p.b.trusted(job/'offline.json')),'offline traffic fence lost')
cp=p.b.strict(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
p.need(cp['Id']==plan['expected']['controller_id'] and not cp['State']['Running'] and cp['HostConfig']['RestartPolicy']['Name']=='no','controller fence lost')
pid=int(subprocess.check_output(['systemctl','show','baarcha-vps-resize-20261008-04','-p','MainPID','--value']))
cmd=Path('/proc/'+str(pid)+'/cmdline').read_bytes()
p.need(b'/opt/baarcha/operations/vps-50-profiles-20261008/resize-worker.py\0' in cmd,'old coordinator changed')
p.x.publish(job/'resume-handoff.json',{'old_coordinator_pid':pid,'reason':'public executable reader used private-only mode','worker_stopped_clean':True})
os.kill(pid,signal.SIGUSR1)
until=time.monotonic()+10
while Path('/proc/'+str(pid)).exists():
    p.need(time.monotonic()<until,'old coordinator did not exit');time.sleep(.05)
with p.b.locked() as fds:
    events=[]
    def event(phase,value=None):
        p.x.publish(job/('resume-%03d-%s.json'%(len(events),phase)),{'at':time.time(),'phase':phase,'value':value})
        events.append(phase);p.b.atomic(job/'current.json',p.b.encoded({'phase':phase,'locks_held':True,'continuation':True}))
    host=r.Host(plan,job,fds,event)
    host.routes={name:p.b.strict(p.b.trusted(job/(name+'.json'))) for name in ('online','offline','drain')}
    host.baseline=p.b.strict(p.b.trusted(job/'baseline.json'))
    host.want_stop=p.b.strict(p.b.trusted(job/'stop-wanted.json'))
    host.clean_path=clean;host.clean_inventory=inventory
    host.motion_baseline=p.b.strict(p.b.trusted(job/'pre-drain-evidence.json'))['direct_writers']['jobs']
    signal.signal(signal.SIGTERM,lambda*a:None);signal.signal(signal.SIGINT,lambda*a:None)
    try:
        result=p.Sequence(host,event).start(receipt)
        print(json.dumps(result),flush=True)
    except BaseException as error:
        event('pending-operator-review',{'error_class':type(error).__name__,'error':str(error)[:2048]})
        while True:time.sleep(30)
