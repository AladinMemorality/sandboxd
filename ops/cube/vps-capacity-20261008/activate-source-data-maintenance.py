"""Activate the canaried supervisor on awake VPS guests; never start a VM."""
import importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
deadline=time.monotonic()+18000
while True:
 state=subprocess.check_output(['systemctl','show','baarcha-vps-source-data-canary-54','-p','ActiveState','--value'],text=True).strip()
 assert state!='failed' and time.monotonic()<deadline
 if state=='inactive':break
 time.sleep(5)
proof=json.loads((root/'source-data-supervisor-canary-54/complete.json').read_text());assert proof['passed'] and len(proof['results'])==2
with b.locked():
 out=root/'source-data-maintenance-55';out.mkdir(mode=0o700)
 code='EXPECTED='+repr(proof['sha256'])+'\n'+'''import hashlib,json,os,pathlib,subprocess,time
P=pathlib.Path;assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
root=P('/opt/baarcha-vps-source-data-3b1a6f0');assert hashlib.sha256((root/'runtimed').read_bytes()).hexdigest()==EXPECTED
for name in ['runtimed','guest.py','worker.py','release.json']:
 p=root/name;assert not p.is_symlink() and p.stat().st_uid==0 and p.stat().st_mode&0o022==0
service='baarcha-published-runtime.service';timer='baarcha-published-runtime.timer'
target=P('/etc/systemd/system/baarcha-published-runtime.service.d/20-vps-reviewed-supervisor.conf')
old='[Service]\\nExecStart=\\nExecStart=/usr/bin/python3 /opt/baarcha-vps-export-recovery-2c7e700/worker.py\\n'
assert target.read_text()==old and subprocess.check_output(['systemctl','is-active',timer],text=True).strip()=='active'
(root/'timer-dropin.before').write_text(old)
subprocess.run(['systemctl','stop',timer],check=True)
try:
 deadline=time.monotonic()+300
 while subprocess.check_output(['systemctl','show',service,'-p','ActiveState','--value'],text=True).strip() not in ('inactive','failed'):
  assert time.monotonic()<deadline;time.sleep(1)
 # Update only guests already running; the updater rejects active agent tasks.
 p=subprocess.run(['python3',str(root/'worker.py')],capture_output=True,timeout=600)
 (root/'activation.PRIVATE.log').write_bytes(p.stdout+p.stderr);assert p.returncode==0
 rows=[json.loads(line) for line in p.stdout.splitlines()]
 assert rows and all(r.get('status') in ('current','updated') and r.get('sha256')==EXPECTED for r in rows)
 new=old.replace('/opt/baarcha-vps-export-recovery-2c7e700/','/opt/baarcha-vps-source-data-3b1a6f0/')
 temporary=target.with_suffix('.pending');assert not temporary.exists()
 with temporary.open('x') as f:f.write(new);f.flush();os.fsync(f.fileno())
 temporary.chmod(0o644);os.replace(temporary,target)
 subprocess.run(['systemctl','daemon-reload'],check=True)
finally:subprocess.run(['systemctl','start',timer],check=True)
assert subprocess.check_output(['systemctl','is-active',timer],text=True).strip()=='active'
print(json.dumps({'enabled':True,'sha256':EXPECTED,'awake_guests_updated':len(rows),'wakes_guests':False,'model_calls':False,'b200_contacted':False,'at':time.time()}))
'''
 b.atomic(out/'worker-install.py',code.encode())
 p=subprocess.run(ssh+['python3','-'],input=code.encode(),capture_output=True,timeout=1000)
 b.atomic(out/'install.PRIVATE.log',p.stdout+p.stderr);assert p.returncode==0
 result=json.loads(p.stdout);b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result),flush=True)
