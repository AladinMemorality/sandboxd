"""Finish the reviewed VPS-only recovery stages, stopping at any failed proof.

Each stage acquires its own existing operator locks and checks for user tasks.
No model calls, B200 requests, blind retries, or original-source deletion.
"""
import contextlib,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
out=root/'finish-vps-recovery-17';out.mkdir(mode=0o700)
def run(stage,args,timeout):
    (out/'stage.json').write_text(json.dumps({'stage':stage,'at':time.time()}))
    print(json.dumps({'stage':stage,'at':time.time()}),flush=True)
    with (out/(stage+'.PRIVATE.log')).open('wb') as log:
        result=subprocess.run(args,stdout=log,stderr=subprocess.STDOUT,timeout=timeout)
    assert result.returncode==0,stage+' failed; retained private diagnostics and journals'
try:
    stage=root/'parallel-restore-08';deadline=time.monotonic()+5400
    while True:
        if (stage/'complete.json').exists():break
        assert not (stage/'paused.json').exists(),'Restore batch paused; review required'
        assert time.monotonic()<deadline,'Restore deadline exceeded'
        time.sleep(10)
    assert json.loads((stage/'complete.json').read_text())['complete']
    assert json.loads((root/'supervisor-canary-2c7e700/passed.json').read_text())['passed']
    with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
        workers=db.execute("select a.worker_id,count(*) from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id group by a.worker_id").fetchall()
        assert workers==[('vps',135)],'Incomplete VPS placement'
        assert not db.execute("select id from cube_relocation where phase='fenced'").fetchall()
        assert not db.execute("select admission_key from cube_admission where state='pending'").fetchall()
    (out/'all-vps.json').write_text(json.dumps({'sandboxes':135,'worker':'vps','b200_contacted':False,'at':time.time()}))
    run('backup-exporter',['/usr/bin/python3',str(root/'install-source-backup-exporter.py')],120)
    plan=json.loads(subprocess.check_output(['/usr/bin/python3',str(root/'real-preview-density.py'),'--plan'],timeout=90))
    assert plan['ready'];(out/'density-plan.json').write_text(json.dumps(plan))
    run('density',['/usr/bin/python3',str(root/'real-preview-density.py'),'--run'],5400)
    assert json.loads((root/'real-preview-density-50-balanced-03/result.json').read_text())['passed']
    run('source-backup',['/usr/bin/python3','/usr/local/libexec/baarcha-vps-source-backup/backup.py'],7*3600)
    result={'complete':True,'sandboxes_on_vps':135,'real_preview_test_passed':True,'fresh_source_backup_completed':True,'model_calls':False,'b200_contacted':False,'at':time.time()}
    (out/'complete.json').write_text(json.dumps(result));print(json.dumps(result),flush=True)
except BaseException as error:
    (out/'failed.json').write_text(json.dumps({'error':type(error).__name__,'reason':str(error)[:400],'at':time.time()}))
    raise
