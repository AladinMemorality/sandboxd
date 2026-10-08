"""Sequential VPS-local restores of the reviewed standard Vite startup recipe.

Stop on the first failure. Each restore independently validates source identity,
holds operator locks, verifies all content and tests stop/wake before continuing.
"""
import json,os,pathlib,sqlite3,subprocess,sys,time,zipfile
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
limit=int(sys.argv[1]);assert 1<=limit<=125
name=sys.argv[2];assert name.isalnum()
batch=root/('vite-batch-'+name);batch.mkdir(mode=0o700,exist_ok=False)
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
    ids=[x[0] for x in db.execute("select b.sandbox_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id join sandbox s on s.id=b.sandbox_id where a.worker_id='b200-01' and a.state='released' and s.status='stopped' order by b.sandbox_id")]
recipe='command: "[ -x node_modules/.bin/vite ] || pnpm install; pnpm exec vite --host 0.0.0.0 --port 3000"'
selected=[];skipped=[]
for sid in ids:
    candidates=[root/group/sid for group in ['recovery-prepared-v2','recovery-prepared']]
    source=next((p for p in candidates if (p/'export-result.PRIVATE.json').exists()),None)
    if not source:skipped.append({'sandbox_id':sid,'reason':'needs archive preparation'});continue
    with zipfile.ZipFile(source/'workspace.zip') as z:
        manifest=z.read('sandbox.yaml').decode() if 'sandbox.yaml' in z.namelist() else ''
        if recipe not in manifest or '\nworkers:' in manifest:
            skipped.append({'sandbox_id':sid,'reason':'different startup recipe'});continue
    valid=subprocess.run([str(root/'artifact-validator'),str(source)],capture_output=True,timeout=180)
    if valid.returncode:
        skipped.append({'sandbox_id':sid,'reason':'import contract review required'});continue
    assert not (root/'recovery-moves'/('vps-restore-'+sid.lower())).exists(),'Existing recovery journal requires explicit reconciliation'
    selected.append({'sandbox_id':sid,'source_group':source.parent.name})
    if len(selected)==limit:break
(batch/'scope.json').write_text(json.dumps({'selected':selected,'skipped':skipped,'source_contacted':False,'model_calls':False}))
results=[]
for entry in selected:
    sid=entry['sandbox_id'];prepared=root/'recovery-prepared-canonical'/sid
    with (batch/(sid+'.PRIVATE.log')).open('wb') as log:
        if not prepared.exists():
            subprocess.run(['/usr/bin/python3',str(root/'canonicalize-recovery-zip.py'),sid,entry['source_group']],check=True,stdout=log,stderr=subprocess.STDOUT,timeout=300)
        subprocess.run([str(root/'artifact-validator'),str(prepared)],check=True,stdout=log,stderr=subprocess.STDOUT,timeout=180)
        result=subprocess.run(['/usr/bin/python3',str(root/'restore-from-backup.py'),sid],stdout=log,stderr=subprocess.STDOUT)
    item={'sandbox_id':sid,'restored':result.returncode==0};results.append(item)
    (batch/'progress.json').write_text(json.dumps({'results':results,'total':len(selected),'at':time.time()}))
    print(json.dumps({'completed':len(results),'total':len(selected),**item}),flush=True)
    assert result.returncode==0,'Recovery paused for reconciliation; source retained'
(batch/'complete.json').write_text(json.dumps({'results':results,'total':len(selected),'at':time.time(),'source_contacted':False,'model_calls':False}))
