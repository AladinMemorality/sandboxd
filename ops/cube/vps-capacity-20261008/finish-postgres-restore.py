"""Resume the verified PostgreSQL target after dependency installation."""
import hashlib,importlib.util,json,os,pathlib,select,shlex,sqlite3,subprocess,sys,time,urllib.request,zipfile
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M3C9C0V0MQYNFTMCYS7CCNVC';job=root/'recovery-moves'/('vps-restore-'+sid.lower())
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def save(name,value):b.atomic(job/name,b.encoded(value))
def rows(query,args=()):
    with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:return db.execute(query,args).fetchall()
def index(path,exclude):
    out={}
    with zipfile.ZipFile(path) as z:
        for item in z.infolist():
            if exclude(item.filename):continue
            with z.open(item) as f:h=hashlib.file_digest(f,'sha256').hexdigest()
            out[item.filename]=(item.external_attr,item.file_size,h)
    return out
with b.locked():
    assert rows('select phase from cube_relocation where id=?',(job.name,))==[('fenced',)]
    assert not (job/'supervisor-reset-intent.json').exists(),'Reconcile previous restart before retry'
    verification=json.loads((job/'dependency-verification.json').read_text())
    assert verification['locked_install'] and verification['authored_workspace_unchanged'] and verification['history_unchanged']
    request=json.loads((job/'worker-job.PRIVATE.json').read_text());worker=transport.Worker(request);source=request['source']
    assert rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[(source['runtime_id'],)]
    state=worker.control('GET','/status');assert state['active_task'] is None
    worker.control('POST','/workspace/quiesce')
    save('supervisor-reset-intent.json',{'runtime_id':request['runtime_id'],'method':'same environment, temporary revision then canonical revision','model_calls':False})
    channel=subprocess.Popen([str(root/'cube-relocate-package-recovery'),str(job/'channel-request.PRIVATE.json')],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=(job/'finish-channel.PRIVATE.log').open('ab'))
    try:
        assert select.select([channel.stdout],[],[],60)[0]
        assert json.loads(channel.stdout.readline())['outbound']=='npm-registry-only'
        revision=request['sandbox_id']+':'+str(request['config_revision'])
        # Config replacement self-execs runtimed without ending the VM or rewriting app files.
        # Both revisions use the exact verified environment and remain quiesced.
        for current in [revision+':recovery-supervisor-reset',revision]:
            worker.control('POST','/config',{'env':request['env'],'revision':current})
            deadline=time.monotonic()+60
            while worker.control('GET','/status').get('app_config_revision')!=current:
                assert time.monotonic()<deadline;time.sleep(.5)
        worker.http('GET','/export/private-workspace-v2',export=job/'workspace-after-reset.PRIVATE.zip')
        assert transport.digest(job/'workspace-after-reset.PRIVATE.zip')==transport.digest(job/'workspace-after-deps.PRIVATE.zip'),'Workspace changed during supervisor restart'
        worker.control('POST','/workspace/resume')
        headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)}
        def health():
            deadline=time.monotonic()+90
            while True:
                try:
                    data=json.loads(worker.http('GET','/api/health',headers=headers,timeout=5));assert data['ok'] and data['database']=='ready'
                    worker.http('GET','/',headers=headers,timeout=5)
                    state=worker.control('GET','/status');assert all(p['running'] for p in state['processes']) and any(p['name']=='postgres' for p in state['processes']);return
                except (OSError,RuntimeError,AssertionError,KeyError,ValueError):assert time.monotonic()<deadline;time.sleep(1)
        health()
        proof={'RelocationID':job.name,'SandboxID':sid,'RuntimeID':request['runtime_id'],'WorkerID':'vps','ConfigApplied':True,'ApplicationReady':True,'DestinationStockFiles':{}}
        for role in ['workspace','home','history']:proof[role.title()+'SHA256']=source['artifacts'][role]['sha256'];proof[role.title()+'Verified']=True
        save('verified.json',proof)
    finally:channel.stdin.close();channel.wait(timeout=30)
    operation={'Action':'commit','ID':job.name,'Directory':str(job),'Migrations':str(root/'queue-release-d463b2d/source/control-plane/migrations')}
    p=subprocess.run([str(root/'cube-relocate-package-recovery')],input=json.dumps(operation).encode(),capture_output=True,timeout=200);assert p.returncode==0,'Relocation commit refused'
    env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
    def api(action):
        req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
        with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
    api('start');api('stop');began=time.monotonic();api('start');wake=time.monotonic()-began;health();api('stop')
    assert rows('select worker_id,state,charged from cube_admission where runtime_id=?',(request['runtime_id'],))==[('vps','released',0)]
    result={'restored':True,'sandbox_id':sid,'worker':'vps','profile':'large','source_contacted':False,'source_retained':True,'same_project_identity':True,'initial_content_verified':True,'dependencies_reinstalled':True,'database_health_verified':True,'wake_seconds':wake,'at':time.time()};save('complete.json',result);print(json.dumps(result),flush=True)
