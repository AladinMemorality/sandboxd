"""Read and validate every remaining VPS-local source archive; no worker calls."""
import json,pathlib,sqlite3,subprocess,time
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'recovery-preparation';out.mkdir(mode=0o700,exist_ok=False)
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
 ids=[r[0] for r in db.execute("select b.sandbox_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id where a.worker_id='b200-01' and a.state='released' order by b.sandbox_id")]
results=[]
for sid in ids:
 with (out/(sid+'.PRIVATE.log')).open('wb') as log:
  p=subprocess.run(['/usr/bin/python3',str(root/'prepare-recovery.py'),sid],stdout=log,stderr=subprocess.STDOUT,timeout=300)
 result={'sandbox_id':sid,'prepared':p.returncode==0};results.append(result)
 (out/'progress.json').write_text(json.dumps({'total':len(ids),'results':results,'at':time.time()}))
 print(json.dumps({'processed':len(results),'total':len(ids),'passed':sum(r['prepared'] for r in results)}),flush=True)
(out/'complete.json').write_text(json.dumps({'total':len(ids),'results':results,'at':time.time(),'workers_contacted':[]}))
