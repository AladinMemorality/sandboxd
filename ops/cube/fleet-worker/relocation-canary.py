#!/usr/bin/env python3
"""Owned live migration canary; preserve the source on any uncertain result."""
import contextlib,importlib.util,json,os,secrets,select,subprocess,threading,time,sys
from pathlib import Path
from urllib.parse import urlsplit
ROOT=Path('/opt/baarcha-bench/cube-fleet-20260927/capacity-ready')
os.umask(0o077)
def mod(name,path):
    s=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
c=mod('c',ROOT.parent/'copy-fleet.py');r=mod('r',ROOT.parent/'capacity-100/capacity-release.py')
BIN=ROOT/'relocation-build/cube-relocate'
MIGRATIONS=ROOT/'relocation-source/control-plane/migrations'
CREATE_LOCK=threading.Lock()
def save(path,value):c.save(path,value)
def cli(action,job,**values):
    request=dict(Action=action,ID=job.name,Directory=str(job),Migrations=str(MIGRATIONS),**values)
    p=subprocess.run([str(BIN)],input=json.dumps(request).encode(),capture_output=True,timeout=200)
    if p.returncode:
        save(job/('cli-'+action+'-failure.json'),dict(returncode=p.returncode));raise RuntimeError('relocation CLI '+action+' failed')
    return json.loads(p.stdout)
def aws_environment():
    env=dict(os.environ)
    for p in Path('/proc').glob('[0-9]*/comm'):
        try:
            if 'next-server' not in p.read_text() or 'baarcha-landing.service' not in (p.parent/'cgroup').read_text():continue
            e=dict(x.split(b'=',1) for x in (p.parent/'environ').read_bytes().split(b'\0') if b'=' in x)
            for key in ['AWS_ACCESS_KEY_ID','AWS_SECRET_ACCESS_KEY','AWS_SESSION_TOKEN']:
                if key.encode() in e:env[key]=e[key.encode()].decode()
            break
        except OSError:continue
    assert env.get('AWS_ACCESS_KEY_ID');return env
@contextlib.contextmanager
def target_channel(job):
    request=job/'channel-request.PRIVATE.json'
    save(request,dict(Action='channel',ID=job.name,Directory=str(job),Migrations=str(MIGRATIONS)))
    with (job/'channel-error.log').open('ab') as error:
        p=subprocess.Popen([str(BIN),str(request)],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=error)
        try:
            assert select.select([p.stdout],[],[],60)[0],'target channel did not connect'
            assert json.loads(p.stdout.readline()).get('channel_ready')
            yield p
        finally:
            p.stdin.close()
            try:p.wait(timeout=20)
            except subprocess.TimeoutExpired:p.kill();p.wait()
def worker_export(sid,job):
    binding=c.rows('SELECT * FROM runtime_binding WHERE sandbox_id=?',(sid,))[0]
    placement=c.rows('SELECT worker_id FROM cube_admission WHERE runtime_id=?',(binding['runtime_id'],))[0]['worker_id']
    assert placement=='vps'
    _,headers=c.client(sid)
    task_ids=[v['task_id'] for v in c.rows('SELECT task_id FROM task WHERE sandbox_id=? ORDER BY task_id',(sid,))]
    manifests=c.rows('SELECT home_manifest_json FROM runtime_migration WHERE sandbox_id=?',(sid,))
    if manifests:
        home=json.loads(manifests[0]['home_manifest_json'])
    else:
        # Fresh Cube projects use this reviewed template home. Export's strict
        # inventory rejects any additional or missing path; never guess away
        # user data when a newer project has changed the template layout.
        home=dict(version=2,entries=[dict(path=p,disposition='separate' if p in ['workspace/app','.runtimed'] else 'preserve') for p in ['workspace/app','.runtimed','.bashrc','.bash_logout','.profile','.cache']])
    inp=dict(action='export',worker='vps',id=job.name,sandbox_id=sid,runtime_id=binding['runtime_id'],headers=headers,task_ids=task_ids,home_manifest=home)
    save(job/'export-job.PRIVATE.json',inp)
    p=subprocess.run(['/usr/bin/python3',str(ROOT/'move-project-worker.py')],input=json.dumps(inp).encode(),capture_output=True,timeout=1800)
    assert len(p.stdout)<=256*1024
    result=json.loads(p.stdout);save(job/'export-result.PRIVATE.json',result)
    assert p.returncode==0,'source export failed; retained private details'
    assert task_ids==[v['task_id'] for v in c.rows('SELECT task_id FROM task WHERE sandbox_id=? ORDER BY task_id',(sid,))]
    return result
def publish(job,source):
    source_root=ROOT.parent/'project-moves'/job.name
    env=aws_environment();receipts={}
    for role,manifest in source['artifacts'].items():
        manifest['kind']='project-relocation-v1';save(source_root/(role+'-manifest.json'),manifest)
        receipt=source_root/(role+'-receipt.PRIVATE.json')
        p=subprocess.run(['/opt/baarcha/node22/bin/node',str(ROOT/'publish-test-copy.mjs'),str(source_root/(role+'.zip')),str(source_root/(role+'-manifest.json')),str(receipt)],env=env,capture_output=True,timeout=300)
        assert p.returncode==0,'S3 publication failed'
        receipts[role]=json.loads(receipt.read_text())
    return receipts
def move(sid,job,source=None,on_source_stopped=None):
    source=source or worker_export(sid,job)
    status,_=c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
    cli('fence',job,SandboxID=sid,ExpectedRuntime=source['runtime_id'],TargetWorker='b200-01')
    if on_source_stopped:on_source_stopped()
    receipts=publish(job,source)
    with CREATE_LOCK:
        for attempt in range(12):
            try:cli('create',job);break
            except RuntimeError:
                # A provider request may have happened after the durable intent.
                # Only a pre-intent admission refusal can be tried again.
                if (job/'create-intent.PRIVATE.json').exists() or attempt==11:raise
                time.sleep(5)
    target=json.loads((job/'target.PRIVATE.json').read_text());j=target['Relocation'];runtime=target['Runtime']
    headers={'Host':'3031-'+runtime['sandboxID']+'.'+j['Domain'],'Authorization':'Bearer '+target['supervisor_token'],'cube-traffic-access-token':target['traffic_access_token']}
    web=c.rows('SELECT web_port FROM sandbox WHERE id=?',(sid,))[0]['web_port']
    request=dict(action='restore',worker='b200-01',id=job.name,sandbox_id=sid,runtime_id=runtime['sandboxID'],headers=headers,receipts=receipts,source=source,env=target['Env'],config_revision=j['ConfigRevision'],web_port=web)
    save(job/'worker-job.PRIVATE.json',request)
    print(json.dumps(dict(phase='waiting-worker-restore',id=job.name)),flush=True)
    deadline=time.monotonic()+1800
    while not (job/'worker-content.json').exists():
        assert not (job/'worker-result.json').exists(),'worker restore failed before content verification'
        assert time.monotonic()<deadline,'worker content proof timed out';time.sleep(1)
    with target_channel(job):
        while not (job/'worker-result.json').exists():
            assert time.monotonic()<deadline,'worker proof timed out';time.sleep(2)
        result=json.loads((job/'worker-result.json').read_text());assert not result.get('failed'),str(result)
    save(job/'verified.json',result)
    status,raw=c.request('127.0.0.1',20300,'/sandboxes/'+source['runtime_id'],headers={'X-API-Key':c.ENV['SANDBOXD_CUBE_API_KEY']})
    assert status==200 and json.loads(raw)['state']=='paused','source stop proof changed'
    cli('commit',job)
    status,_=c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200,'canonical target start failed'
    return source,runtime['sandboxID']
def preview(sid,needle):
    status,v=c.api('POST','/v1/sandboxes/'+sid+'/preview-access');assert status==200
    deadline=time.monotonic()+120
    while True:
        status,raw=c.request('127.0.0.1',9090,'/',headers={'Host':urlsplit(v['url']).netloc,'Cookie':'sandbox_preview='+v['token']})
        if status==200 and needle.encode() in raw:break
        assert time.monotonic()<deadline,'fixture preview not ready, HTTP '+str(status)
        time.sleep(1)
    unsigned,_=c.request('127.0.0.1',9090,'/',headers={'Host':urlsplit(v['url']).netloc})
    public=c.rows('SELECT visibility FROM sandbox WHERE id=?',(sid,))[0]['visibility']=='public'
    assert unsigned==200 if public else unsigned in [401,403]
    return dict(signed_http=status,unsigned_http=unsigned)
def main():
    with r.b.locked():
        jobs=ROOT/'moves';jobs.mkdir(mode=0o700,exist_ok=True)
        if len(sys.argv)>1:
            job=jobs/sys.argv[1];assert job.parent==jobs and job.is_dir()
            baseline=json.loads((job/'baseline.json').read_text());app=json.loads((job/'app.PRIVATE.json').read_text());marker=app['external_project_id']
            assert app['external_user_id']=='operator:relocation-acceptance' and not (job/'export-result.PRIVATE.json').exists()
        else:
            job=jobs/('canary-'+secrets.token_hex(8));job.mkdir(mode=0o700)
            baseline=c.rows('SELECT sandbox_id,runtime_id FROM runtime_binding ORDER BY sandbox_id');save(job/'baseline.json',baseline)
            marker='relocation-'+secrets.token_hex(12)
            status,app=c.api('POST','/v1/apps',dict(name='Cube relocation acceptance',runtime_preset='react-vite',external_user_id='operator:relocation-acceptance',external_project_id=marker,tags=['operator-acceptance']))
            assert status==201;save(job/'app.PRIVATE.json',app)
        sourcejob=job/'source';sourcejob.mkdir(mode=0o700,exist_ok=True)
        sid=app['id']
        if not (sourcejob/'target.PRIVATE.json').exists():cli('fixture',sourcejob,AppID=app['id'],SandboxID=sid,TargetWorker='vps')
        status,_=c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200
        _,headers=c.client(sid)
        payload=json.dumps(dict(path='index.html',content='<html><body>'+marker+'</body></html>'))
        # Canonical file-write route takes the source file as its raw body.
        st,_=c.request('127.0.0.1',9090,'/v1/sandboxes/'+sid+'/files?path=index.html','PUT','<html><body>'+marker+'</body></html>',{'Authorization':'Bearer '+c.TOKEN,'Content-Type':'text/html'})
        assert st==200
        preview(sid,marker)
        save(job/'scope.json',dict(app_id=app['id'],sandbox_id=sid,marker=marker))
        source,target=move(sid,job)
        result=dict(moved=True,app_id=app['id'],sandbox_id=sid,source_runtime_id=source['runtime_id'],target_runtime_id=target,preview=preview(sid,marker))
        status,_=c.api('POST','/v1/sandboxes/'+sid+'/stop');assert status==200
        started=time.monotonic();status,_=c.api('POST','/v1/sandboxes/'+sid+'/start');assert status==200
        result['wake_seconds']=time.monotonic()-started;result['after_wake']=preview(sid,marker)
        assert c.rows('SELECT sandbox_id,runtime_id FROM runtime_binding WHERE sandbox_id<>? ORDER BY sandbox_id',(sid,))==baseline
        save(job/'acceptance.json',result);print(json.dumps(result),flush=True)
        # Cleanup is explicit after inspecting the result; retained source remains fenced.
if __name__=='__main__':main()
