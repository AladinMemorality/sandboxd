"""Sequentially reprofile reviewed stopped VPS Vite apps; stop on any failure."""
import contextlib,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');batch=root/'reprofile-batch-768'
assert json.loads((root/'recovery-moves/vps-reprofile-01m16mv7kzsf3ynaj1vkwkyed5-768-01/complete.json').read_text())['restored']
with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
 selected=[r[0] for r in db.execute("select b.sandbox_id from runtime_binding b join cube_admission a on a.runtime_id=b.runtime_id join sandbox s on s.id=b.sandbox_id where a.worker_id='vps' and b.template_id='tpl-78e4edb3d629465e9d8372c1' and s.status='stopped' and a.state='released' and a.charged=0 order by b.sandbox_id")]
batch.mkdir(mode=0o700);(batch/'scope.json').write_text(json.dumps({'selected':selected,'model_calls':False,'b200_contacted':False}))
results=[]
for sid in selected:
 with (batch/(sid+'.PRIVATE.log')).open('wb') as log:
  result=subprocess.run(['/usr/bin/python3',str(root/'reprofile-vps.py'),sid,'01'],stdout=log,stderr=subprocess.STDOUT)
 item={'sandbox_id':sid,'passed':result.returncode==0};results.append(item)
 (batch/'progress.json').write_text(json.dumps({'results':results,'total':len(selected),'at':time.time()}))
 print(json.dumps({'completed':len(results),'total':len(selected),**item}),flush=True)
 assert result.returncode==0,'Profile upgrade stopped; reconcile journal before proceeding'
(batch/'complete.json').write_text(json.dumps({'results':results,'total':len(selected),'model_calls':False,'b200_contacted':False}))
