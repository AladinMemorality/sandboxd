"""Fixed-scope proc/cgroup metrics. Never reads environ, cmdline or app contents."""
import hashlib,json,os,pathlib,re,stat,subprocess,time
P=pathlib.Path
PROVIDER='095a0076b90b4b009ab995837909e276'
SANDBOX='01M3D1Q0E1KM1FEM244XVHEC65'
DATA_UUID='793c3349-db9c-4815-9842-989ed484f1f8'
def need(ok,message):
    if not ok:raise ValueError(message)
def text(path,limit=131072):
    with open(path,'rb') as f:b=f.read(limit+1)
    need(len(b)<=limit,'metric exceeds read bound');return b.decode()
def run(args,timeout=5,limit=1048576):
    # Every caller supplies a fixed tool/metric path, never guest command text.
    p=subprocess.run(args,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=timeout)
    need(p.returncode==0 and len(p.stdout)<=limit,'read-only metric command failed')
    return p.stdout.decode()
def counters(raw):
    out={}
    for line in raw.splitlines():
        fields=line.replace(':',' ').split()
        if len(fields) in (2,3) and fields[1].isdigit():out[fields[0]]=int(fields[1])
    return out
def proc_stat(raw):
    end=raw.rfind(')');need(end>0,'invalid proc stat');f=raw[end+2:].split()
    need(len(f)>=22,'short proc stat')
    return {'start_ticks':int(f[19]),'minor_faults':int(f[7]),'major_faults':int(f[9]),'user_ticks':int(f[11]),'system_ticks':int(f[12]),'threads':int(f[17]),'virtual_bytes':int(f[20]),'rss_pages':int(f[21])}
def process(pid,expected=None):
    root=P('/proc')/str(pid);before=proc_stat(text(root/'stat',8192))
    if expected is not None:need(before['start_ticks']==expected,'PID generation changed')
    result={'pid':pid,**before,'comm':text(root/'comm',256).strip(),'cgroup':text(root/'cgroup',4096).strip(),'io':counters(text(root/'io',4096))}
    # smaps_rollup gives sharing-adjusted PSS; no address ranges/pathnames leave.
    smaps=counters(text(root/'smaps_rollup'))
    fields=('Rss','Pss','Pss_Anon','Pss_File','Pss_Shmem','Private_Clean','Private_Dirty','Shared_Clean','Shared_Dirty','Swap','SwapPss')
    result['memory_bytes']={k:smaps[k]*1024 for k in fields if k in smaps}
    need(proc_stat(text(root/'stat',8192))['start_ticks']==before['start_ticks'],'PID replaced during sample')
    return result
def cgroup(path):
    need(path.startswith('/') and '..' not in P(path).parts,'invalid cgroup path')
    root=P('/sys/fs/cgroup')/path.lstrip('/');need(root.resolve()==root,'cgroup symlink')
    out={}
    for name in ('memory.current','memory.peak','memory.stat','memory.events','memory.swap.current','memory.max','memory.high','cpu.stat','cpu.max','cpu.pressure','memory.pressure','io.stat'):
        p=root/name
        out[name]=text(p) if p.exists() else None
    return out
def host_memory():
    m=counters(text('/proc/meminfo'));fields=('MemTotal','MemFree','MemAvailable','Buffers','Cached','SReclaimable','Shmem','SwapTotal','SwapFree')
    need(all(k in m for k in fields),'missing host memory counters')
    return {k:m[k]*1024 for k in fields}
def boot():return text('/proc/sys/kernel/random/boot_id',128).strip()
def parse_guest_stats(raw):
    lines=raw.splitlines();need(any(PROVIDER in line for line in lines[:3]),'metrics provider mismatch')
    keys={'memory.usage_in_bytes','memory.limit_in_bytes','memory.stat.cache','cpuacct.usage'};out={}
    for line in lines:
        f=line.split()
        if len(f)==2 and f[0] in keys:
            need(f[0] not in out and f[1].isdigit(),'invalid guest metric');out[f[0]]=int(f[1])
    need(set(out)==keys and out['memory.limit_in_bytes']==2*1024**3,'guest metrics unavailable or resource changed')
    # Current shim's MemoryStat encoder does not populate cache. ctr displays
    # the protobuf default zero: this is unavailable, not measured zero cache.
    out['memory.stat.cache']=None
    out['cache_supported']=False
    return out
def worker_identity():
    b=json.loads(run(['cubecli','cubebox','inspect',PROVIDER],20))
    need(b['ID']==b['sandbox_id']==PROVIDER and b['namespace']=='default','provider metadata mismatch')
    containers=b['ContainersMap']['ContainerMap'];need(set(containers)=={PROVIDER} and containers[PROVIDER]['ID']==PROVIDER,'container scope changed')
    group=b['CGroupPath'];need(re.fullmatch(r'/cube_sandbox/sandbox/[0-9]+',group) is not None,'unexpected guest cgroup')
    pid=int(b['endpoint']['Pid']);info=process(pid)
    need(info['comm']=='containerd-shim' and info['cgroup']=='0::'+group,'shim/cgroup mismatch')
    members=[int(x) for x in text(P('/sys/fs/cgroup'+group)/'cgroup.procs').split()]
    need(members==[pid],'unexpected guest cgroup members')
    # KVM lives in this shim, rather than a separately named VMM child.
    kvm=[]
    for path in (P('/proc')/str(pid)/'fd').iterdir():
        try:name=os.readlink(path)
        except OSError:continue
        if name=='/dev/kvm' or name.startswith('anon_inode:kvm'):kvm.append(name)
    need('/dev/kvm' in kvm and 'anon_inode:kvm-vm' in kvm,'target is not the KVM-owning shim')
    raw=run(['cubecli','storage','ls','--raw'],20,16*1024**2);rows=[]
    for line in raw.splitlines():
        key,sep,value=line.partition('\t')
        if sep and key==PROVIDER:rows.append(json.loads(value))
    need(len(rows)==1 and rows[0]['sandboxID']==PROVIDER,'current volume metadata unavailable')
    volumes=[x for x in rows[0]['volumes'] if x['name']=='cube_rootfs_rw'];need(len(volumes)==1,'ambiguous writable volume')
    volume=volumes[0];path=P(volume['file_path'])
    need(str(path).startswith('/data/cubelet/storage/xfs/objects/volumes/') and path.name=='sb-'+PROVIDER+'-rootfs-gen0' and path.resolve()==path,'unreviewed writable generation/path')
    st=path.stat();need(stat.S_ISREG(st.st_mode) and st.st_size==10*1024**3,'writable disk changed')
    need(run(['findmnt','-n','-o','UUID','--target','/data']).strip()==DATA_UUID,'data filesystem changed')
    return {'worker_boot_id':boot(),'provider_id':PROVIDER,'pid':pid,'start_ticks':info['start_ticks'],'cgroup':group,'assigned':b['ResourceWithOverHead'],'template_id':b['LocalRunTemplate']['distributionReference']['templateID'],'disk_path':str(path),'disk_inode':st.st_ino,'disk_device':st.st_dev,'disk_logical_bytes':st.st_size,'kvm_vcpus':len([x for x in kvm if 'kvm-vcpu:' in x])}
def worker_sample(identity,guest=False):
    need(boot()==identity['worker_boot_id'],'worker rebooted')
    p=process(identity['pid'],identity['start_ticks']);need(p['cgroup']=='0::'+identity['cgroup'],'shim moved cgroup')
    disk=P(identity['disk_path']);st=disk.stat()
    need(disk.resolve()==disk and st.st_ino==identity['disk_inode'] and st.st_dev==identity['disk_device'] and st.st_size==identity['disk_logical_bytes'],'current disk generation changed')
    row={'worker_boottime_ns':time.clock_gettime_ns(time.CLOCK_BOOTTIME),'process':p,'cgroup':cgroup(identity['cgroup']),'worker_memory':host_memory(),'disk':{'logical_bytes':st.st_size,'allocated_bytes':st.st_blocks*512,'mtime_ns':st.st_mtime_ns,'inode':st.st_ino}}
    if guest:
        at=time.monotonic();row['guest_stats']=parse_guest_stats(run(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','metrics',PROVIDER],5,8192));row['guest_stats_duration_ms']=(time.monotonic()-at)*1000
    return row
def worker_stream(seconds):
    need(10<=seconds<=1200,'invalid duration');identity=worker_identity()
    print(json.dumps({'identity':identity}),flush=True)
    start=time.monotonic()
    for index in range(seconds+1):
        at=time.monotonic();row=worker_sample(identity,index%5==0);row['index']=index;row['worker_collection_ms']=(time.monotonic()-at)*1000
        print(json.dumps(row),flush=True)
        if index<seconds:time.sleep(max(0,start+index+1-time.monotonic()))
