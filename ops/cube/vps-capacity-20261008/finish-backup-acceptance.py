"""Resume only the immutable backup after a reviewed failed export diagnostic."""
import json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');generation='20261009T200641Z'
while True:
 state=subprocess.check_output(['systemctl','show','baarcha-vps-backup-stats-76','-p','ActiveState','--value'],text=True).strip()
 assert state!='failed','Review failed export diagnostic before accepting backup'
 if state=='inactive':break
 time.sleep(10)
assert json.loads((root/'source-backup-stats-76/complete.json').read_text())['passed']
assert subprocess.check_output(['systemctl','show','baarcha-vps-final-acceptance-50','-p','ActiveState','--value'],text=True).strip()=='failed'
for name,key in [('real-preview-density-50-balanced-07/result.json','passed'),('real-preview-density-50-balanced-07/cleanup.json','complete'),('pause-compaction-maintenance-01/complete.json','installed'),('finish-fleet-validation-42/complete.json','complete')]:assert json.loads((root/name).read_text())[key]
out=root/'final-acceptance-71';out.mkdir(mode=0o700)
subprocess.run(['/usr/bin/python3','/usr/local/libexec/baarcha-vps-source-backup/backup.py','--resume',generation],check=True,timeout=7*3600)
latest=json.loads(P('/var/backups/baarcha-vps-source/latest.json').read_text());assert latest['verified'] and latest['generation']==generation and latest['sandboxes']==136
(root/'source-backup-final-refresh.json').write_text(json.dumps({'verified':True,'refreshed':True,'generation':generation,'sandboxes':136,'at':time.time()}))
with (out/'audit.json').open('wb') as log:subprocess.run(['/usr/bin/python3',str(root/'final-vps-audit.py')],stdout=log,check=True)
assert json.loads((out/'audit.json').read_text())['passed']
(out/'complete.json').write_text(json.dumps({'complete':True,'previous_failed_operation':'final-acceptance-50','resumed_only':'immutable backup export and verification','b200_contacted':False,'model_calls':False,'at':time.time()}))
print(json.dumps({'complete':True,'backup_generation':generation}),flush=True)
