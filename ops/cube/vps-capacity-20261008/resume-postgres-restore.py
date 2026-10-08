"""Reinstall locked JS dependencies in the verified, fenced PostgreSQL target."""
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
    assert json.loads((job/'failed.json').read_text())['reason']=='application did not become ready'
    assert not (job/'npm-install-intent.json').exists(),'Reconcile an earlier install before retrying'
    request=json.loads((job/'worker-job.PRIVATE.json').read_text());worker=transport.Worker(request);source=request['source']
    assert json.loads((worker.root/'content-verified.json').read_text())['content_verified']
    assert rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[(source['runtime_id'],)]
    assert worker.control('GET','/status')['active_task'] is None
    worker.control('POST','/workspace/quiesce')
    def execute(name,command,timeout=180):
        argv=['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','exec','--exec-id','vps-pg-recovery-'+name,'--user','1000:1000','--cwd','/home/sandbox/workspace/app',request['runtime_id'],'/bin/sh','-c',command]
        p=subprocess.run(SSH+[shlex.join(argv)],capture_output=True,timeout=timeout)
        (job/(name+'.PRIVATE.log')).write_bytes(p.stdout+p.stderr);assert p.returncode==0,'Native command failed'
    execute('clear-uid-probe','test "$(cat .vps-recovery-uid-proof)" = 1000 && rm -- .vps-recovery-uid-proof')
    receipt='.vps-recovery-npm-result';log='.vps-recovery-npm.log'
    for name in [receipt,log]:
        try:worker.http('GET','/files/content?path='+name,timeout=5)
        except transport.HTTPFailure as e:assert e.status==404
        else:raise AssertionError('Install receipt path already exists')
    channel=subprocess.Popen([str(root/'cube-relocate-package-recovery'),str(job/'channel-request.PRIVATE.json')],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=(job/'npm-channel.PRIVATE.log').open('ab'))
    try:
        assert select.select([channel.stdout],[],[],60)[0]
        assert json.loads(channel.stdout.readline())['outbound']=='npm-registry-only'
        command='test "$(id -u)" = 1000 || exit 99; umask 077; env HOME=/home/sandbox HTTP_PROXY=http://127.0.0.1:3032 HTTPS_PROXY=http://127.0.0.1:3032 http_proxy=http://127.0.0.1:3032 https_proxy=http://127.0.0.1:3032 NO_PROXY=localhost,127.0.0.1 NPM_CONFIG_USERCONFIG=/dev/null npm ci --ignore-scripts --cache /tmp/baarcha-pg-recovery-npm --registry=https://registry.npmjs.org --no-audit --no-fund > '+log+' 2>&1; result=$?; printf "%s\\n" "$result" > '+receipt+'; exit "$result"'
        save('npm-install-intent.json',{'runtime_id':request['runtime_id'],'uid':1000,'method':'npm ci --ignore-scripts','cache':'temporary','model_calls':False})
        execute('install',command,timeout=240)
        deadline=time.monotonic()+240
        while True:
            try:code=worker.http('GET','/files/content?path='+receipt,timeout=5).strip();break
            except transport.HTTPFailure as e:assert e.status==404 and time.monotonic()<deadline;time.sleep(1)
        (job/'npm-output.PRIVATE.log').write_bytes(worker.http('GET','/files/content?path='+log,timeout=5))
        assert code==b'0','Locked dependency installation failed; private output retained'
        execute('clear-install-receipts','rm -- '+receipt+' '+log)
        prepared=root/'recovery-prepared-canonical'/sid
        worker.http('GET','/export/private-workspace-v2',export=job/'workspace-after-deps.PRIVATE.zip')
        no_deps=lambda name:name=='node_modules/' or name.startswith('node_modules/')
        assert index(prepared/'workspace.zip',no_deps)==index(job/'workspace-after-deps.PRIVATE.zip',no_deps),'Authored workspace changed during install'
        worker.http('POST','/export/private-home-v2',source['home_manifest'],export=job/'home-after-deps.PRIVATE.zip')
        postgres=lambda name:name=='.baarcha-postgres/' or name.startswith('.baarcha-postgres/')
        assert index(prepared/'home.zip',postgres)==index(job/'home-after-deps.PRIVATE.zip',postgres),'Home data outside PostgreSQL changed'
        worker.http('POST','/export/private-task-history',{'task_ids':source['task_ids']},export=job/'history-after-deps.PRIVATE.zip')
        assert transport.digest(job/'history-after-deps.PRIVATE.zip')==source['artifacts']['history']['sha256']
        save('dependency-verification.json',{'locked_install':True,'authored_workspace_unchanged':True,'home_outside_postgres_unchanged':True,'history_unchanged':True,'database_initial_import_verified':True,'database_engine_recovery_expected':True,'model_calls':False})
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
