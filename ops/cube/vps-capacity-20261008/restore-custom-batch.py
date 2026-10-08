"""Review the custom recipes while the standard batch waits at its barrier."""
import json,os,pathlib,subprocess,time
os.umask(0o077);root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
plan=json.loads((root/'custom-restore-plan.json').read_text());assert plan['source_contacted'] is False and plan['model_calls'] is False
barrier=root/'restore-barrier.json';gate=json.loads(barrier.read_text())
assert gate['purpose']=='reviewed-restore-barrier' and set(gate['allowed_sandboxes'])==set(plan['profiles'])
batch=root/'custom-restore-batch';batch.mkdir(mode=0o700,exist_ok=False)
results=[]
for sid,profile in plan['profiles'].items():
    with (batch/(sid+'.PRIVATE.log')).open('wb') as log:
        process=subprocess.run(['/usr/bin/python3',str(root/'restore-from-backup.py'),sid,profile],stdout=log,stderr=subprocess.STDOUT)
    item={'sandbox_id':sid,'profile':profile,'restored':process.returncode==0};results.append(item)
    value={'results':results,'total':len(plan['profiles']),'at':time.time()}
    (batch/'progress.json').write_text(json.dumps(value));print(json.dumps(item),flush=True)
    assert process.returncode==0,'Custom restore stopped for reconciliation; source retained'
(batch/'complete.json').write_text(json.dumps(value))
assert json.loads(barrier.read_text())==gate
barrier.unlink()
