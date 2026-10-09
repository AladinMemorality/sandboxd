"""Continue only after the first compaction, then bounded fleet wake batches."""
import importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
out=root/'finish-storage-fleet-26';out.mkdir(mode=0o700)
def run(name,*args):
 print(json.dumps({'stage':name,'at':time.time()}),flush=True)
 subprocess.run(['/usr/bin/python3',str(root/name),*args],check=True)
# The initial run predates the incremental processed index. Seed it only from
# its completed, byte-verified batch receipts; retain every original journal.
with b.locked():
 completed=list((root/'memory-compaction').glob('fleet-*/complete.json'));assert len(completed)==1
 first=json.loads(completed[0].read_text());assert first['complete'] and first['files']>=130
 state=root/'memory-compaction/processed.json';assert not state.exists();ids=set()
 for path in completed[0].parent.glob('batch-[0-9][0-9][0-9].json'):
  proof=json.loads(path.read_text());assert proof['passed'] and proof['contents_identical']
  ids.update(r['snapshot_id'] for r in proof['files'])
 assert len(ids)==first['files']
 b.atomic(state,b.encoded({'snapshot_ids':sorted(ids),'last_generation':first['generation'],'at':time.time()}))
run('reconcile-fleet-storage-interruption.py')
run('dedupe-vps-os-images.py')
run('dedupe-vps-os-images.py','--all')
for batch in range(12):
 if (root/'fleet-wake-validation-01/complete.json').exists():break
 previous=len(list((root/'fleet-wake-validation-01').glob('*/passed.json')))
 run('verify-vps-fleet-wakes.py','--resume','--max-new','16')
 if (root/'fleet-wake-validation-01/complete.json').exists():break
 current=len(list((root/'fleet-wake-validation-01').glob('*/passed.json')))
 assert current>previous,'No new wake proof; inspect capacity before another batch'
 run('compact-vps-pause-memory.py')
else:raise RuntimeError('Fleet batch bound exceeded')
proof=json.loads((root/'fleet-wake-validation-01/complete.json').read_text());assert proof['complete']
run('compact-vps-pause-memory.py')
b.atomic(out/'complete.json',b.encoded({'complete':True,'fleet_count':proof['count'],'b200_contacted':False,'model_calls':False,'at':time.time()}))
