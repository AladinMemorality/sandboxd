"""Run storage/template preparation only after the owned load test fully exits."""
import json,pathlib,subprocess,time
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
density=pathlib.Path('/opt/baarcha/operations/vps-density-20261008-02')
while subprocess.run(['systemctl','is-active','--quiet','baarcha-vps-density-20261008-02']).returncode==0:time.sleep(5)
r=json.loads((density/'result.json').read_text());v=json.loads((density/'final-verification.json').read_text())
assert r['cleanup_verified'] and v['bindings_unchanged'] and v['quota_unchanged'] and not v['monitor_failures']
assert json.loads((density/'operator-exit.json').read_text())['exit_code']==0
for script in ['trim-data.py','prepare-standard-1024.py','prepare-builder-3072.py']:
 subprocess.run(['/usr/bin/python3',str(root/script)],check=True)
(root/'after-density-complete.json').write_text(json.dumps({'complete':True,'at':time.time()}))
