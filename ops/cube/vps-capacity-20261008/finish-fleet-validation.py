"""Finish bounded VPS wake batches, compacting only verified pause memory."""
import json,os,pathlib,subprocess,time
os.umask(0o077);root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
out=root/'finish-fleet-validation-35';out.mkdir(mode=0o700)
def run(name,*args):
 print(json.dumps({'stage':name,'at':time.time()}),flush=True)
 subprocess.run(['/usr/bin/python3',str(root/name),*args],check=True)
assert json.loads((root/'public-wake-deploy-02/complete.json').read_text())['complete']
deadline=time.monotonic()+3600
while True:
 state=subprocess.check_output(['systemctl','show','baarcha-vps-stalled-source-recovery-34','-p','ActiveState','--value'],text=True).strip()
 assert state!='failed','Review source recovery before resuming fleet'
 if state=='inactive':break
 assert state in ('active','activating') and time.monotonic()<deadline;time.sleep(5)
assert json.loads((root/'finish-stalled3-recovery-34/complete.json').read_text())['passed']
for batch in range(5):
 run('compact-vps-pause-memory.py')
 if (root/'fleet-wake-validation-01/complete.json').exists():break
 previous=len(list((root/'fleet-wake-validation-01').glob('*/passed.json')))
 run('verify-vps-fleet-wakes.py','--resume','--max-new','16')
 current=len(list((root/'fleet-wake-validation-01').glob('*/passed.json')))
 assert current>previous or (root/'fleet-wake-validation-01/complete.json').exists(),'No new proof; review before resuming'
else:raise RuntimeError('Fleet batch bound exceeded')
proof=json.loads((root/'fleet-wake-validation-01/complete.json').read_text());assert proof['complete']
(out/'complete.json').write_text(json.dumps({'complete':True,'count':proof['count'],'model_calls':False,'b200_contacted':False,'at':time.time()}))
