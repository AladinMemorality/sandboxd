"""Sequence the remaining reviewed acceptance gates; stop at the first failure."""
import json,os,pathlib,subprocess,time
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
out=root/'final-acceptance-50';out.mkdir(mode=0o700)
def run(name,*args):
 print(json.dumps({'stage':name,'at':time.time()}),flush=True)
 subprocess.run(['/usr/bin/python3',str(root/name),*args],check=True)
deadline=time.monotonic()+7200
for unit in ['baarcha-vps-finish-fleet-42','baarcha-vps-os-image-dedupe-resume-27','baarcha-vps-bounded-trim-49']:
 while True:
  state=subprocess.check_output(['systemctl','show',unit,'-p','ActiveState','--value'],text=True).strip()
  assert state!='failed','Inspect failed preceding operation: '+unit
  if state=='inactive':break
  assert state in ('active','activating','deactivating') and time.monotonic()<deadline
  time.sleep(10)
 assert subprocess.check_output(['systemctl','show',unit,'-p','Result','--value'],text=True).strip()=='success'
assert json.loads((root/'finish-fleet-validation-42/complete.json').read_text())['complete']
assert json.loads((root/'os-image-dedupe-resume-01/complete.json').read_text())['complete']
trims=[json.loads(p.read_text()) for p in root.glob('data-trim-*/complete.json')]
assert any(v.get('complete') and v.get('minimum_extent_bytes')==1048576 and time.time()-v['at']<900 for v in trims)
# Let the normal ten-second storage observer report the newly discarded blocks.
time.sleep(15)
run('real-preview-density.py','--plan','--generation','real-preview-density-50-balanced-07')
run('real-preview-density.py','--run','--generation','real-preview-density-50-balanced-07')
run('compact-vps-pause-memory.py')
run('trim-compacted-vps-data.py')
run('enable-vps-supervisor-maintenance.py')
run('install-pause-compaction-maintenance.py')
run('refresh-source-backup.py')
with (out/'audit.json').open('wb') as log:
 subprocess.run(['/usr/bin/python3',str(root/'final-vps-audit.py')],stdout=log,check=True)
assert json.loads((out/'audit.json').read_text())['passed']
(out/'complete.json').write_text(json.dumps({'complete':True,'b200_contacted':False,'model_calls':False,'at':time.time()}))
print(json.dumps({'complete':True,'at':time.time()}),flush=True)
