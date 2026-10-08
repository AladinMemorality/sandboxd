"""Resume the existing fenced canary; never create or import a second target."""
import hashlib,importlib.util,json,os,pathlib,select,sqlite3,stat,subprocess,sys,time,traceback,zipfile
os.umask(0o077)
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
sid='01M16MV7KZSF3YNAJ1VKWKYED5';job=root/'recovery-moves'/('vps-restore-'+sid.lower())
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
BIN=root/'cube-relocate-package-recovery'
def save(name,value):b.atomic(job/name,b.encoded(value))
with b.locked():
    try:
        with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
            assert db.execute('select phase from cube_relocation where id=?',(job.name,)).fetchone()==('fenced',)
            assert db.execute("select count(*) from task where sandbox_id=? and status='running'",(sid,)).fetchone()==(0,)
        assert not (job/'verified.json').exists()
        prepared=root/'recovery-prepared-canonical'/sid
        subprocess.run([str(root/'artifact-validator'),str(prepared)],check=True,capture_output=True,timeout=180)
        request=json.loads((job/'worker-job.PRIVATE.json').read_text())
        canonical=json.loads((prepared/'export-result.PRIVATE.json').read_text())
        assert canonical['runtime_id']==request['source']['runtime_id']
        assert canonical['home_manifest']==request['source']['home_manifest']
        assert canonical['task_ids']==request['source']['task_ids']
        request['source']=canonical;request['receipts']=canonical['artifacts']
        save('canonical-worker-job.PRIVATE.json',request)
        channel_request=json.loads((job/'channel-request.PRIVATE.json').read_text())
        channel_request['PackageDownloads']=True
        save('package-channel-request.PRIVATE.json',channel_request)
        # Original worker-job/export receipts remain immutable for audit.
        with (job/'resume-channel-error.log').open('ab') as error:
            channel=subprocess.Popen([str(BIN),str(job/'package-channel-request.PRIVATE.json')],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=error)
            try:
                assert select.select([channel.stdout],[],[],60)[0]
                ready=json.loads(channel.stdout.readline())
                assert ready.get('channel_ready') and ready['outbound']=='npm-registry-only'
                worker=transport.Worker(request)
                # The denied first install created only partial node_modules.
                # Reimport the verified source only after proving no source
                # file, mode or private data was changed during that attempt.
                worker.control('POST','/workspace/quiesce')
                current=worker.root/'before-package-retry.zip'
                worker.http('GET','/export/private-workspace-v2',export=current)
                workspace=prepared/'workspace.zip'
                with zipfile.ZipFile(workspace) as src,zipfile.ZipFile(current) as dst:
                    names=set(src.namelist());actual=set(dst.namelist())
                    assert names<=actual and not any(n.startswith('node_modules/') for n in names)
                    assert all(n.startswith('node_modules/') for n in actual-names)
                    for name in names:
                        assert src.getinfo(name).external_attr==dst.getinfo(name).external_attr
                        assert hashlib.sha256(src.read(name)).digest()==hashlib.sha256(dst.read(name)).digest()
                    added=len(actual-names)
                if added:
                    assert not (job/'partial-dependencies-reset.json').exists()
                    save('partial-dependencies-reset.json',{'original_source_unchanged':True,'added_dependency_entries':added,'before_archive':str(current)})
                    boot=worker.control('GET','/status')['runtimed']['booted_at']
                    with workspace.open('rb') as src:worker.http('PUT','/import/private-workspace-v2',src,workspace.stat().st_size)
                    deadline=time.monotonic()+90
                    while worker.control('GET','/status')['runtimed']['booted_at']==boot:
                        assert time.monotonic()<deadline;time.sleep(.5)
                # The first install also created a new, otherwise empty PNPM
                # store. Preserve it explicitly and prove every original home
                # entry still matches before adopting these generated entries.
                worker.control('POST','/workspace/quiesce')
                home=json.loads(json.dumps(request['source']['home_manifest']))
                assert not any(e['path']=='.local' for e in home['entries'])
                home['entries'].append({'path':'.local','disposition':'preserve'})
                current_home=worker.root/'home-package-bootstrap.zip'
                worker.http('POST','/export/private-home-v2',home,export=current_home)
                with zipfile.ZipFile(prepared/'home.zip') as src,zipfile.ZipFile(current_home) as dst:
                    names=set(src.namelist());actual=set(dst.namelist());assert names<=actual
                    for name in names:
                        assert src.getinfo(name).external_attr==dst.getinfo(name).external_attr
                        assert hashlib.sha256(src.read(name)).digest()==hashlib.sha256(dst.read(name)).digest()
                    for name in actual-names:
                        entry=dst.getinfo(name)
                        assert name in ('.local/','.local/share/','.local/share/pnpm/') or name.startswith('.local/share/pnpm/store/')
                        if not entry.is_dir():
                            assert name=='.local/share/pnpm/store/v10/projects/be105daff285c17decb2d4df263cad2d'
                            assert stat.S_ISLNK(entry.external_attr>>16)
                            assert dst.read(name)==b'../../../../../../workspace/app'
                new_home={'sha256':transport.digest(current_home),'archive_bytes':current_home.stat().st_size,'local_path':str(current_home)}
                save('generated-home-evidence.json',{'original_home_unchanged':True,'original_home_sha256':request['receipts']['home']['sha256'],'verified_home_sha256':new_home['sha256'],'generated_entries':len(actual-names)})
                request['source']['home_manifest']=home
                request['receipts']['home']=new_home
                save('bootstrap-worker-job.PRIVATE.json',request)
                proof=worker.verify();save('verified.json',proof)
            finally:
                channel.stdin.close();channel.wait(timeout=30)
        print(json.dumps({'verified':True,'sandbox_id':sid,'ready_for_commit':True}),flush=True)
    except BaseException as error:
        save('resume-failed.json',{'error':type(error).__name__,'reason':str(error)[:512],'operation':getattr(error,'operation',None),'status':getattr(error,'status',None),'line':traceback.extract_tb(error.__traceback__)[-1].lineno,'at':time.time(),'source_retained':True})
        raise
