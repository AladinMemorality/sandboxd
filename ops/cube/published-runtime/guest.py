"""Privileged, bounded in-guest supervisor update. Never restart an active task."""
import hashlib, json, os, pathlib, stat, sys, time, tty, urllib.request, urllib.error
P=pathlib.Path
EXE=P('/usr/local/bin/runtimed')
FENCE=P('/home/sandbox/.runtimed/workspace-quiesced')
BACKUP=P('/usr/local/lib/baarcha-runtimed')
http=urllib.request.build_opener(urllib.request.ProxyHandler({}))

def sha(path): return hashlib.sha256(path.read_bytes()).hexdigest()
def report(**data): print('RUNTIME_RECEIPT='+json.dumps(data),flush=True)
def main():
    assert os.geteuid()==0
    if not EXE.exists() or EXE.is_symlink() or not stat.S_ISREG(EXE.stat().st_mode):
        report(status='unmanaged'); return
    before=sha(EXE)
    pids=[]
    for proc in P('/proc').iterdir():
        if not proc.name.isdigit(): continue
        try:
            if os.readlink(proc/'exe').removesuffix(' (deleted)')==str(EXE):pids.append(proc)
        except OSError: pass
    if len(pids)!=1:report(status='no_unique_supervisor',sha256=before);return
    proc=pids[0]
    env=dict(v.split('=',1) for v in (proc/'environ').read_bytes().decode().split('\0') if '=' in v)
    if env.get('RUNTIMED_CUBE_GUEST')!='1' or not env.get('RUNTIMED_HTTP_TOKEN'):
        report(status='unmanaged'); return
    def call(path,body=None):
        req=urllib.request.Request('http://127.0.0.1:3031'+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':'Bearer '+env['RUNTIMED_HTTP_TOKEN'],'Content-Type':'application/json'},method='GET' if body is None else 'POST')
        with http.open(req,timeout=5) as r:return r.status,json.loads(r.read() or b'{}')
    _,status=call('/status')
    if sys.argv[1]=='probe':report(status='managed',sha256=before,active_task=bool(status.get('active_task')));return
    cfg=json.loads(sys.argv[1]);target=cfg['sha256']
    if before==target and sha(proc/'exe')==target:report(status='current',sha256=target);return
    if before not in cfg['previous']:report(status='unreviewed_binary',sha256=before);return
    if status.get('active_task') or FENCE.exists():report(status='busy',sha256=before);return
    original=env.get('RUNTIMED_APP_CONFIG_REVISION','')
    if len(original)>180 or (not original and env.get('RUNTIMED_APP_ENV_KEYS')):report(status='unreviewed_config');return
    # Fresh templates have no app configuration/revision yet. Give the empty
    # environment an internal revision so the existing guarded restart API can
    # re-exec it; later owner configuration replaces this normally.
    original=original or 'runtimed-empty-config'
    appenv={k:env[k] for k in env.get('RUNTIMED_APP_ENV_KEYS','').split(',') if k and k in env}
    def configure(revision):call('/config',{'env':appenv,'revision':revision})
    def wait(revision):
        deadline=time.monotonic()+20
        while time.monotonic()<deadline:
            try:
                if call('/status')[1].get('app_config_revision')==revision:return
            except (urllib.error.URLError,TimeoutError,ConnectionError):pass
            time.sleep(.2)
        raise RuntimeError('supervisor readiness')
    # The operator sends only the reviewed public binary, after the terminal is
    # raw. Credentials stay inside the guest and never enter this stream.
    tty.setraw(0)
    print('RUNTIME_READY',flush=True)
    data=sys.stdin.buffer.read(cfg['bytes'])
    if len(data)!=cfg['bytes'] or hashlib.sha256(data).hexdigest()!=target:raise RuntimeError('binary transfer incomplete')
    temp=EXE.with_name('.runtimed-preview-'+str(os.getpid()))
    fd=os.open(temp,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o755)
    with os.fdopen(fd,'wb') as f:f.write(data);f.flush();os.fsync(f.fileno())
    owned=False;installed=False
    try:
        # The supervisor atomically checks its task lock before quiescing. A
        # task submitted during transfer wins, leaving its process untouched.
        call('/workspace/quiesce',{});owned=True
        BACKUP.mkdir(mode=0o700,exist_ok=True)
        backup=BACKUP/before
        if backup.exists():assert sha(backup)==before
        else:os.link(EXE,backup) # rollback point shares blocks with the old binary
        os.replace(temp,EXE);installed=True
        transient=original+':frontend-'+target[:12]
        configure(transient);wait(transient)
        configure(original);wait(original)
        try:code,_=call('/files?path=dist&recursive=false')
        except urllib.error.HTTPError as e:code=e.code
        assert code in cfg.get('dist_status',[200,404]),'build-read readiness contract failed'
        call('/workspace/resume',{});owned=False
        report(status='updated',before=before,sha256=target,config_preserved=True)
    except BaseException:
        # Do not interfere if another actor already resumed the workspace.
        if owned and FENCE.exists():
            if installed:
                rollback=EXE.with_name('.runtimed-preview-rollback')
                os.link(BACKUP/before,rollback);os.replace(rollback,EXE)
                transient=original+':frontend-rollback'
                configure(transient);wait(transient)
                configure(original);wait(original)
            call('/workspace/resume',{})
        raise
    finally:
        if temp.exists():temp.unlink()

try:main()
except BaseException as error:
    report(status='failed',error_type=type(error).__name__)
    sys.exit(1)
