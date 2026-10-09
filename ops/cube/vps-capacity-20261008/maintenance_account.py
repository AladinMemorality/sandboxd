"""Hold platform account locks only for the explicitly selected maintenance apps."""
import contextlib,json,pathlib,select,sqlite3,subprocess,threading,time,urllib.request
ROOT=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
def keepalive_value(sid):
    with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
        return db.execute('select keepalive_until from sandbox where id=?',(sid,)).fetchone()[0]
@contextlib.contextmanager
def active_maintenance(ids,journal):
    env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env'])
    token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
    before={sid:keepalive_value(sid) for sid in ids};owned={};stop=threading.Event();failed=[]
    (journal/'keepalive-before.json').write_text(json.dumps(before))
    def set_until(sid,until):
        request=urllib.request.Request('http://127.0.0.1:9090/sandbox/'+sid+'/keepalive',method='POST',data=json.dumps({'until':until}).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
        with urllib.request.urlopen(request,timeout=10) as response:
            assert response.status==200
            return json.load(response)['keepalive_until']
    def renew():
        for sid in ids:
            current=keepalive_value(sid);until=int(time.time())+900
            if (current or 0)>=until:continue
            owned[sid]=set_until(sid,until)
    def heartbeat():
        while not stop.wait(60):
            try:renew()
            except BaseException as error:failed.append(type(error).__name__);return
    thread=None
    try:
        renew();thread=threading.Thread(target=heartbeat,daemon=True);thread.start()
        yield
        assert not failed,'Maintenance keepalive renewal failed'
    finally:
        stop.set()
        if thread:thread.join(timeout=520);assert not thread.is_alive()
        for sid,value in owned.items():
            if keepalive_value(sid)==value:set_until(sid,max(before[sid] or 0,int(time.time())+1))
@contextlib.contextmanager
def account_maintenance(ids, journal):
    assert ids and len(ids)<=50 and len(set(ids))==len(ids)
    assert all(len(sid)==26 and sid.isalnum() for sid in ids)
    with (journal/'account-maintenance.PRIVATE.log').open('ab') as log:
        command=['/opt/baarcha/node22/bin/node','--env-file=/opt/baarcha/landing.env',str(ROOT/'maintenance-account.mjs'),*ids]
        process=subprocess.Popen(command,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=log,bufsize=0)
        try:
            deadline=time.monotonic()+1810
            while True:
                assert time.monotonic()<deadline,'Account maintenance lease timed out'
                assert select.select([process.stdout],[],[],30)[0] or process.poll() is None,'Account guard exited'
                if not select.select([process.stdout],[],[],0)[0]:continue
                line=process.stdout.readline();assert line,'Account guard exited; private diagnostic retained'
                log.write(line);log.flush()
                if line.startswith(b'MAINTENANCE_READY='):
                    value=json.loads(line.split(b'=',1)[1]);assert value['sandboxes']==ids
                    break
            with active_maintenance(ids,journal):yield
        finally:
            process.stdin.close();process.stdin=None
            try:
                output,_=process.communicate(timeout=180);log.write(output)
                assert process.returncode==0,'Account guard did not finish its accounting checkpoint'
            except subprocess.TimeoutExpired:
                process.terminate();output,_=process.communicate(timeout=30);log.write(output)
                raise
            finally:log.flush()
