#!/usr/bin/env python3
"""Read-only coding metrics for one exact owned sandbox. No lifecycle or exec."""
import argparse,contextlib,hashlib,json,os,pathlib,select,sqlite3,subprocess,time
import metrics as m
P=pathlib.Path
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oBatchMode=yes','-oConnectTimeout=5','-oServerAliveInterval=5','-oServerAliveCountMax=2','-oStrictHostKeyChecking=yes','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1','python3 -u -']
GROUP='/system.slice/baarcha-cube-worker-01.service'
DB='/var/lib/sandboxd/state/sandboxd.db'
def task_state():
    with contextlib.closing(sqlite3.connect('file:'+DB+'?mode=ro',uri=True,timeout=1)) as db:
        db.execute('PRAGMA query_only=ON');db.execute('BEGIN')
        binding=db.execute('SELECT runtime_id FROM runtime_binding WHERE sandbox_id=?',(m.SANDBOX,)).fetchone()
        m.need(binding==(m.PROVIDER,),'canonical runtime binding changed')
        rows=db.execute('SELECT task_id,status,created_at,finished_at FROM task WHERE sandbox_id=? ORDER BY created_at DESC LIMIT 100',(m.SANDBOX,)).fetchall()
    return [dict(zip(('task_id','status','created_at','finished_at'),r)) for r in rows]
def outer_identity():
    state=json.loads(m.text('/opt/baarcha-cube/worker-01/lifecycle-status.json',8192))
    pid=state['qemu_pid'];p=m.process(pid)
    m.need(P(os.readlink('/proc/'+str(pid)+'/exe')).name=='qemu-system-x86_64' and p['cgroup']=='0::'+GROUP,'outer QEMU identity mismatch')
    return {'boot_id':m.boot(),'pid':pid,'start_ticks':p['start_ticks'],'cgroup':GROUP,'clock_ticks_per_second':os.sysconf('SC_CLK_TCK')}
def outer_sample(identity):
    m.need(m.boot()==identity['boot_id'],'outer rebooted')
    p=m.process(identity['pid'],identity['start_ticks']);m.need(p['cgroup']=='0::'+GROUP,'outer QEMU moved')
    return {'process':p,'cgroup':m.cgroup(GROUP),'host_memory':m.host_memory()}
def read_line(process,timeout=10):
    deadline=time.monotonic()+timeout
    raw=getattr(process,'_profile_buffer',b'')
    while b'\n' not in raw:
        remaining=deadline-time.monotonic();m.need(remaining>0,'worker sampler timed out')
        ready,_,_=select.select([process.stdout],[],[],remaining);m.need(bool(ready),'worker sampler timed out')
        block=os.read(process.stdout.fileno(),65536);m.need(bool(block),'worker sampler ended')
        raw+=block;m.need(len(raw)<=262144,'worker stream exceeded buffer bound')
    line,tail=raw.split(b'\n',1);m.need(len(line)<=131072,'worker metric exceeds bound');process._profile_buffer=tail
    return json.loads(line)
def cpu_delta(before,after,seconds,ticks=100):
    values={}
    for scope in ('outer','worker'):
        old=before[scope]['process'];now=after[scope]['process']
        m.need(old['pid']==now['pid'] and old['start_ticks']==now['start_ticks'],'counter generation changed')
        elapsed=(now['user_ticks']+now['system_ticks'])-(old['user_ticks']+old['system_ticks'])
        m.need(elapsed>=0 and seconds>0,'counter regressed or invalid interval')
        values[scope+'_process_cpu_seconds']=elapsed/ticks
        values[scope+'_process_cpu_percent_one_core']=elapsed/ticks/seconds*100
        for key in ('read_bytes','write_bytes','rchar','wchar'):
            a=old['io'].get(key);b=now['io'].get(key)
            values[scope+'_'+key+'_delta']=b-a if a is not None and b is not None and b>=a else None
    return values
def summary(rows,identity,worker_identity,label):
    m.need(len(rows)>=2,'insufficient samples')
    elapsed=(rows[-1]['outer_boottime_ns']-rows[0]['outer_boottime_ns'])/1e9
    result={'label':label,'sandbox_id':m.SANDBOX,'provider_id':m.PROVIDER,'seconds':elapsed,'samples':len(rows),'cpu_and_io':cpu_delta(rows[0],rows[-1],elapsed,identity['clock_ticks_per_second']),'scopes':{},'assigned':worker_identity['assigned'],'tasks_first':rows[0]['tasks'],'tasks_last':rows[-1]['tasks'],'guest_cache_available':False}
    for scope in ('outer','worker'):
        values=[r[scope]['process']['memory_bytes'] for r in rows]
        result['scopes'][scope]={'rss_sampled_peak_bytes':max(x['Rss'] for x in values),'pss_sampled_peak_bytes':max(x['Pss'] for x in values),'pss_first_bytes':values[0]['Pss'],'pss_last_bytes':values[-1]['Pss'],'pss_delta_bytes':values[-1]['Pss']-values[0]['Pss'],'pss_anon_sampled_peak_bytes':max(x.get('Pss_Anon',0) for x in values),'pss_file_sampled_peak_bytes':max(x.get('Pss_File',0) for x in values),'cgroup_current_sampled_peak_bytes':max(int(r[scope]['cgroup']['memory.current']) for r in rows)}
    guest=[r for r in rows if 'guest_stats' in r['worker']]
    if len(guest)>=2:
        first,last=guest[0],guest[-1];a=first['worker']['guest_stats'];b=last['worker']['guest_stats'];span=(last['worker']['worker_boottime_ns']-first['worker']['worker_boottime_ns'])/1e9
        used=b['cpuacct.usage']-a['cpuacct.usage'];m.need(used>=0 and span>0,'guest stats counter regressed')
        result['guest_container']={'memory_sampled_peak_bytes':max(r['worker']['guest_stats']['memory.usage_in_bytes'] for r in guest),'assigned_limit_bytes':b['memory.limit_in_bytes'],'cpu_seconds':used/1e9,'cpu_percent_one_core':used/1e9/span*100,'sample_span_seconds':span,'samples':len(guest)}
    disks=[r['worker']['disk'] for r in rows]
    result['disk']={'logical_bytes':disks[0]['logical_bytes'],'allocated_first_bytes':disks[0]['allocated_bytes'],'allocated_last_bytes':disks[-1]['allocated_bytes'],'allocated_sampled_peak_bytes':max(x['allocated_bytes'] for x in disks),'allocated_delta_bytes':disks[-1]['allocated_bytes']-disks[0]['allocated_bytes'],'unique_physical_bytes_known':False}
    result['observer']={'outer_collection_ms_max':max(r['outer_collection_ms'] for r in rows),'worker_collection_ms_max':max(r['worker']['worker_collection_ms'] for r in rows),'guest_stats_collection_ms_max':max([r['worker'].get('guest_stats_duration_ms',0) for r in rows]),'host_interval_target_seconds':1,'guest_interval_target_seconds':5,'no_guest_exec':True}
    return result
def phase_events(path):
    if path is None:return []
    p=P(path);s=p.lstat();m.need(p.resolve()==p and s.st_uid==0 and s.st_mode&0o077==0,'unsafe phase input')
    out=[]
    for line in m.text(p,65536).splitlines():
        value=json.loads(line);m.need(value['phase'] in ('idle','coding','build','verification','finished'),'unknown phase')
        out.append({k:value[k] for k in ('phase','task_id','at_unix_ns') if k in value})
    return out
def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--output',required=True,type=P);p.add_argument('--seconds',type=int,default=60);p.add_argument('--label',choices=('idle','coding','build','verification'),default='idle');p.add_argument('--phase-file');a=p.parse_args()
    os.umask(0o077);m.need(os.geteuid()==0 and 10<=a.seconds<=1200,'root and bounded10–1200s duration required')
    m.need(str(a.output).startswith('/opt/baarcha-bench/cube-coding-profile-') and a.output.resolve()==a.output and not a.output.exists(),'fresh private output directory required')
    parent=a.output.parent.stat();m.need(parent.st_uid==0 and not parent.st_mode&0o022,'unsafe output parent')
    task_state();identity=outer_identity();a.output.mkdir(mode=0o700)
    code=P(m.__file__).read_text()+'\nworker_stream('+str(a.seconds)+')\n'
    (a.output/'sampler-identity.json').write_text(json.dumps({'outer':identity,'metrics_source_sha256':hashlib.sha256(P(m.__file__).read_bytes()).hexdigest(),'sampler_source_sha256':hashlib.sha256(P(__file__).read_bytes()).hexdigest()},indent=2))
    rows=[];worker=None;failure=None
    with (a.output/'worker-stderr-private.txt').open('xb') as errorlog,(a.output/'samples.jsonl').open('x') as output:
        proc=subprocess.Popen(SSH,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=errorlog,bufsize=0)
        try:
            proc.stdin.write(code.encode());proc.stdin.close();worker=read_line(proc,30)['identity']
            (a.output/'worker-identity-private.json').write_text(json.dumps(worker,indent=2))
            for index in range(a.seconds+1):
                row=read_line(proc,10);start=time.monotonic()
                m.need(row['index']==index,'worker sample sequence changed')
                value={'at_unix_ns':time.time_ns(),'outer_boottime_ns':time.clock_gettime_ns(time.CLOCK_BOOTTIME),'worker':row,'outer':outer_sample(identity),'tasks':task_state(),'phase_events':phase_events(a.phase_file)}
                value['outer_collection_ms']=(time.monotonic()-start)*1000
                output.write(json.dumps(value,separators=(',',':'))+'\n');output.flush();rows.append(value)
            m.need(proc.wait(timeout=10)==0,'worker sampler failed')
        except Exception as error:failure=str(error)
        finally:
            if proc.poll() is None:proc.terminate()
            try:proc.wait(timeout=5)
            except subprocess.TimeoutExpired:proc.kill();proc.wait()
    result={'complete':failure is None,'failure':failure,'no_lifecycle_operations':True,'samples':len(rows)}
    if len(rows)>=2:result['measurements']=summary(rows,identity,worker,a.label)
    (a.output/'summary.json').write_text(json.dumps(result,indent=2))
    print(json.dumps({'complete':result['complete'],'samples':len(rows),'output':str(a.output)}))
    if failure:raise SystemExit(1)
if __name__=='__main__':main()
