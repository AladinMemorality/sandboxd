#!/usr/bin/env python3
"""VPS-only point-in-time source backup with verified, bounded retention."""
import contextlib,datetime,fcntl,hashlib,importlib.util,json,os,pathlib,shlex,shutil,sqlite3,subprocess,sys,tarfile,time,re
P=pathlib.Path;os.umask(0o077)
ROOT=P('/var/backups/baarcha-vps-exited2-20261009');TOOLS=P('/usr/local/libexec/baarcha-vps-source-backup')
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','-oConnectTimeout=10','-oServerAliveInterval=15','-oServerAliveCountMax=4','root@127.0.0.1']
INNER='/root/baarcha-source-backup-tools'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
def run(args,timeout=180):
    try:return subprocess.check_output(args,stderr=subprocess.PIPE,timeout=timeout)
    except subprocess.CalledProcessError as error:
        if 'dest' in globals():b.atomic(dest/'command-error.PRIVATE.log',error.stderr or b'')
        raise
def remote(*args,timeout=180):return run(SSH+[shlex.join(args)],timeout)
def save(path,value):b.atomic(path,b.encoded(value))
def sha(path):
    with path.open('rb') as stream:return hashlib.file_digest(stream,'sha256').hexdigest()
@contextlib.contextmanager
def operator_locks():
    deadline=time.monotonic()+1800
    while True:
        stack=contextlib.ExitStack()
        try:stack.enter_context(b.locked())
        except BlockingIOError:
            stack.close();assert time.monotonic()<deadline,'operator maintenance busy';time.sleep(2)
        else:break
    with stack:yield
ROOT.mkdir(mode=0o700,exist_ok=True)
with (ROOT/'operator.lock').open('a') as lock:
    fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    assert shutil.disk_usage(ROOT).free>40*1024**3,'backup disk free-space guard'
    resume=len(sys.argv)==3 and sys.argv[1]=='--resume'
    assert len(sys.argv)==1 or resume
    stamp=sys.argv[2] if resume else datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    assert re.fullmatch(r'[0-9]{8}T[0-9]{6}Z',stamp)
    dest=ROOT/stamp
    if resume:assert dest.is_dir() and not dest.is_symlink() and not (dest/'VERIFIED.json').exists()
    else:dest.mkdir(mode=0o700)
    inner='/data/baarcha-source-generations/'+stamp
    try:
        if not resume:
            with operator_locks():
                with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
                    assert db.execute("select count(*) from cube_relocation where phase='fenced'").fetchone()[0]==0,'unfinished recovery; postpone backup'
                    with sqlite3.connect(dest/'controller.PRIVATE.sqlite') as backup:db.backup(backup)
                with sqlite3.connect(dest/'controller.PRIVATE.sqlite') as db:
                    assert db.execute('pragma integrity_check').fetchone()[0]=='ok'
                    db.row_factory=sqlite3.Row
                    all_bindings=[dict(r) for r in db.execute('select b.sandbox_id,b.runtime_id,a.worker_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id')]
                    rows=[r for r in all_bindings if r['sandbox_id']=='01M2QDV0PGEJ1AAKE2MJXGK4P8'];assert rows==[{'sandbox_id':'01M2QDV0PGEJ1AAKE2MJXGK4P8','runtime_id':'5373f2de0dac4906987503f741534ccb','worker_id':'vps'}]
                save(dest/'bindings.PRIVATE.json',all_bindings)
                shutil.copy2('/var/lib/sandboxd/secrets.key',dest/'controller-secrets.PRIVATE.key')
                scope={'worker':'vps','bindings':rows};save(dest/'scope.json',scope)
                remote('mkdir','-p','/data/baarcha-source-generations');remote('mkdir','-m','700',inner)
                subprocess.run(SSH+['cat > '+shlex.quote(inner+'/scope.json')],input=json.dumps(scope).encode(),check=True,capture_output=True,timeout=30)
                capture=json.loads(remote('/usr/bin/python3',INNER+'/capture.py',inner,timeout=900));save(dest/'capture-summary.json',capture)
                # Routing changes during capture invalidate this generation's mapping.
                with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
                    current=dict(db.execute('select sandbox_id,runtime_id from runtime_binding'))
                    assert all(current.get(r['sandbox_id'])==r['runtime_id'] for r in rows)
        else:
            scope=json.loads((dest/'scope.json').read_text());assert scope['worker']=='vps'
            rows=scope['bindings'];all_bindings=json.loads((dest/'bindings.PRIVATE.json').read_text())
            assert rows==[r for r in all_bindings if r['sandbox_id']=='01M2QDV0PGEJ1AAKE2MJXGK4P8']
            assert json.loads((dest/'capture-summary.json').read_text())['captured']==len(rows)
            assert json.loads(remote('cat',inner+'/scope.json'))==scope
            with sqlite3.connect('file:'+str(dest/'controller.PRIVATE.sqlite')+'?mode=ro',uri=True) as db:
                assert db.execute('pragma integrity_check').fetchone()[0]=='ok'
        # Export only immutable private clones. No provider or app call is made.
        # Serialize nested-VM export with operator restores to avoid load spikes.
        with operator_locks(), (dest/'export.PRIVATE.log').open('ab') as log:
            subprocess.run(SSH+[shlex.join(['nice','-n','15','ionice','-c','3','/usr/bin/python3',INNER+'/export-source.py',inner])],check=True,stdout=log,stderr=subprocess.STDOUT,timeout=6*3600)
        exports=dest/'sandboxes';exports.mkdir(mode=0o700,exist_ok=resume)
        rsync_ssh=shlex.join(SSH[:-1])
        run(['rsync','-a','--bwlimit=10240','--safe-links','--chmod=Du=rwx,Dgo=,Fu=rw,Fgo=','-e',rsync_ssh,SSH[-1]+':'+inner+'/exports/',str(exports)+'/'],timeout=3600)
        receipts=[]
        for row in rows:
            folder=exports/row['sandbox_id'];receipt=json.loads((folder/'receipt.json').read_text());archive=folder/'home.tar.gz'
            assert receipt['sandbox_id']==row['sandbox_id'] and receipt['runtime_id']==row['runtime_id'] and receipt['worker']=='vps'
            assert archive.stat().st_size==receipt['bytes'] and sha(archive)==receipt['sha256']
            run(['gzip','-t',str(archive)],timeout=600)
            # Read the whole stream without extracting tenant files onto the host.
            entries=0
            with tarfile.open(archive,'r|gz') as tar:
                for item in tar:
                    parts=P(item.name).parts
                    assert 'node_modules' not in parts,'dependency exclusion failed'
                    assert not any(parts[:len(prefix)]==prefix for prefix in [('.local','share','pnpm','store'),('.pnpm-store',),('.cache','pnpm')]),'PNPM cache exclusion failed'
                    entries+=1
                    # Python 3.12 retains TarInfo objects even in stream mode.
                    # We only count headers, so bound memory for large Git trees.
                    tar.members.clear()
            assert entries>0
            audit=json.loads((folder/'dependency-audit.json').read_text())
            if audit.get('archive_sha256'):
                dep=folder/'dependency-modifications.tar.gz';assert sha(dep)==audit['archive_sha256'] and dep.stat().st_size==audit['archive_bytes']
            save(folder/'verified.json',{'sha256':receipt['sha256'],'bytes':receipt['bytes'],'entries':entries,'verified_at':time.time()});receipts.append(receipt)
        result={'verified':True,'generation':stamp,'worker':'vps','sandboxes':len(rows),'bytes':sum(r['bytes'] for r in receipts),'other_worker_bindings':sum(r['worker_id']!='vps' for r in all_bindings),'unselected_bindings':len(all_bindings)-len(rows),'generated_pnpm_caches_excluded':True,'b200_contacted':False,'completed_at':time.time(),'scope':'Merged /home/sandbox excluding recorded reproducible dependencies/caches; controller SQLite and decryption key. Filesystem copies are crash-consistent, not logical database dumps.'}
        save(dest/'VERIFIED.json',result);save(ROOT/'latest.json',result)
        # Remove only this job's clones after independently verifying the copies.
        remote('rm','-rf','--',inner,timeout=900)
        verified=sorted(p for p in ROOT.iterdir() if p.is_dir() and (p/'VERIFIED.json').is_file())
        for prior in verified[:-2]:
            if not prior.is_symlink() and time.time()-(prior/'VERIFIED.json').stat().st_mtime>7*86400:shutil.rmtree(prior)
        print(json.dumps(result),flush=True)
    except BaseException as error:
        save(dest/'FAILED.json',{'type':type(error).__name__,'at':time.time(),'private_clones_retained':True})
        raise
