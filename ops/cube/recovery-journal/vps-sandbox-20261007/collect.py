import pathlib,subprocess,time,os,json,hashlib
os.umask(0o077)
r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007');remote='/var/lib/cube-rescue/vps-sandbox-20261007'
ssh=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=5','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/rescue-operator-20260925/known_hosts','-i','/opt/baarcha-cube/rescue-operator-20260925/operator-key','-p','20223','root@127.0.0.1']
end=time.monotonic()+1000
while time.monotonic()<end:
 try: result=subprocess.run(ssh+['test -f '+remote+'/exit-status && cat '+remote+'/exit-status'],capture_output=True,timeout=10)
 except subprocess.TimeoutExpired:time.sleep(5);continue
 if result.returncode==0:
  assert result.stdout.strip()==b'0','rescue export failed; inspect private log before further work'
  break
 time.sleep(5)
else:raise RuntimeError('rescue completion deadline; do not infer success')
for name in ['home.tar','export-report.json']:
 with (r/name).open('xb') as f:subprocess.run(ssh+['cat '+remote+'/export/'+name],stdout=f,check=True,timeout=300)
with (r/'home.tar').open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==json.loads((r/'export-report.json').read_text())['archive_sha256']
report=json.loads((r/'export-report.json').read_text())
if report.get('explicit_repair_receipt_sha256'):
 with (r/'repair-receipt.json').open('xb') as f:subprocess.run(ssh+['cat '+remote+'/export/repair-receipt.json'],stdout=f,check=True,timeout=30)
 receipt=json.loads((r/'repair-receipt.json').read_text())
 assert hashlib.sha256((r/'repair-receipt.json').read_bytes()).hexdigest()==report['explicit_repair_receipt_sha256']
 assert receipt['clean_verified'] is True and receipt['source_sha256']==report['captured_disk_sha256']
 assert receipt['clone_after_sha256']==report['explicit_repair_clone_sha256']
 assert [(s['option'],s['log']) for s in receipt['steps']]==[('-fy','repair-fsck.log'),('-fn','verify-fsck.log')]
 for step in receipt['steps']:
  with (r/step['log']).open('xb') as f:subprocess.run(ssh+['cat '+remote+'/export/'+step['log']],stdout=f,check=True,timeout=30)
  assert hashlib.sha256((r/step['log']).read_bytes()).hexdigest()==step['log_sha256']
 print('disposable clone repair receipt and filesystem verification logs retained',flush=True)
print('isolated home export collected and independently verified',flush=True)
