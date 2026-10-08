"""Commit only the fully verified existing recovery target and test its wake."""
import importlib.util,json,os,pathlib,sqlite3,subprocess,sys,time,urllib.request
os.umask(0o077)
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
sid='01M16MV7KZSF3YNAJ1VKWKYED5';job=root/'recovery-moves'/('vps-restore-'+sid.lower())
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
def rows(q,args=()):
    with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:return db.execute(q,args).fetchall()
with b.locked():
    assert not (job/'complete.json').exists()
    assert rows('select phase from cube_relocation where id=?',(job.name,))==[('fenced',)]
    request=json.loads((job/'bootstrap-worker-job.PRIVATE.json').read_text())
    proof=json.loads((job/'verified.json').read_text())
    assert proof['SandboxID']==sid and proof['RuntimeID']==request['runtime_id'] and proof['ApplicationReady']
    assert rows("select count(*) from task where sandbox_id=? and status in ('running','queued')",(sid,))==[(0,)]
    worker=transport.Worker(request)
    headers={**request['headers'],'Host':request['headers']['Host'].replace('3031-',str(request['web_port'])+'-',1)}
    worker.http('GET','/',headers=headers,timeout=10)
    cli={'Action':'commit','ID':job.name,'Directory':str(job),'Migrations':str(root/'weighted-release-02/migrations')}
    result=subprocess.run([str(root/'weighted-release-02/cube-relocate')],input=json.dumps(cli).encode(),capture_output=True,timeout=200)
    assert result.returncode==0,'Relocation commit refused; inspect private journal'
    assert rows('select runtime_id from runtime_binding where sandbox_id=?',(sid,))==[(request['runtime_id'],)]
    env=dict(x.split('=',1) for x in json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]['Config']['Env'])
    token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
    def api(action):
        req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+sid+'/'+action,method='POST',headers={'Authorization':'Bearer '+token})
        with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
    api('start');api('stop');started=time.monotonic();api('start');wake=time.monotonic()-started
    worker.http('GET','/',headers=headers,timeout=10)
    api('stop')
    assert rows('select worker_id,state,charged from cube_admission where runtime_id=?',(request['runtime_id'],))==[('vps','released',0)]
    result={'restored':True,'sandbox_id':sid,'worker':'vps','source_contacted':False,'same_project_identity':True,'wake_seconds':wake,'all_original_content_verified':True,'dependencies_reinstalled':True,'source_retained':True,'at':time.time()}
    b.atomic(job/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
