"""Discard only the failed uncommitted 512 MiB target, then retry at 1 GiB."""
import importlib.util,json,os,pathlib,subprocess,sys
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
sid='01M1HH74TCHSB8JX4MP456KN2N';job=root/'recovery-moves'/('vps-restore-'+sid.lower())
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
with b.locked():
    failure=json.loads((job/'failed.json').read_text());assert failure['reason']=='application did not become ready'
    assert not (job/'verified.json').exists() and not (job/'complete.json').exists()
    request=json.loads((job/'worker-job.PRIVATE.json').read_text());worker=transport.Worker(request)
    assert worker.control('GET','/status')['active_task'] is None
    worker.control('POST','/workspace/quiesce')
    b.atomic(job/'last-web-log.PRIVATE.json',worker.http('GET','/processes/web/logs'))
    operation={'Action':'discard-target','ID':job.name,'Directory':str(job),'Migrations':str(root/'queue-release-d463b2d/migrations')}
    result=subprocess.run([str(root/'cube-relocate-reconcile')],input=json.dumps(operation).encode(),capture_output=True,timeout=180)
    assert result.returncode==0,'Target cleanup needs explicit reconciliation'
    outcome=json.loads(result.stdout);assert outcome['source_retained'] and outcome['relocation_aborted']
    b.atomic(job/'discarded.json',b.encoded(outcome));print(json.dumps(outcome),flush=True)
subprocess.run(['/usr/bin/python3',str(root/'restore-from-backup.py'),sid,'standard','standard'],check=True)
subprocess.run(['/usr/bin/python3',str(root/'restore-from-backup.py'),'01M415VT76M0M88GDY2NWWE942','standard'],check=True)
