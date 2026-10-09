"""Enable the canaried static supervisor for future idle VPS guests only."""
import importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
with b.locked():
 proof=json.loads((root/'fleet-wake-validation-01/complete.json').read_text());assert proof['complete'] and proof['count']>=136
 expected=json.loads((root/'supervisor-canary-2c7e700/passed.json').read_text())['receipt']['sha256']
 assert all(row['supervisor_sha256']==expected for row in proof['results'])
 out=root/'vps-supervisor-maintenance-2c7e700';out.mkdir(mode=0o700)
 code='EXPECTED='+repr(expected)+'\n'+'''import hashlib,importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077)
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
release=P('/opt/baarcha-vps-export-recovery-2c7e700')
for name in ['worker.py','guest.py','runtimed','release.json']:
 p=release/name;assert not p.is_symlink() and p.stat().st_uid==0 and p.stat().st_mode&0o022==0
cfg=json.loads((release/'release.json').read_text());binary=(release/'runtimed').read_bytes()
assert cfg['sha256']==EXPECTED and hashlib.sha256(binary).hexdigest()==EXPECTED and len(binary)==cfg['bytes']
assert "ROOT=P('/opt/baarcha-vps-export-recovery-2c7e700')" in (release/'worker.py').read_text()
spec=importlib.util.spec_from_file_location('guest',release/'guest.py');g=importlib.util.module_from_spec(spec);spec.loader.exec_module(g);g.require_static_supervisor(binary)
timer='baarcha-published-runtime.timer';service='baarcha-published-runtime.service'
assert subprocess.run(['systemctl','is-enabled',timer],capture_output=True,text=True).stdout.strip()=='disabled'
assert subprocess.run(['systemctl','is-active',timer],capture_output=True,text=True).stdout.strip()=='inactive'
assert subprocess.run(['systemctl','is-active',service],capture_output=True,text=True).stdout.strip() in ('inactive','failed')
probe=subprocess.check_output(['python3',str(release/'worker.py'),'--probe'],timeout=300)
receipts=[json.loads(line) for line in probe.splitlines()]
assert receipts and all(r.get('sha256')==EXPECTED and r.get('status')=='managed' and not r.get('active_task') for r in receipts), 'Live guest differs from completed fleet validation'
directory=P('/etc/systemd/system/baarcha-published-runtime.service.d');directory.mkdir(exist_ok=True)
target=directory/'20-vps-reviewed-supervisor.conf';assert not target.exists()
with target.open('x') as f:
 f.write('[Service]\\nExecStart=\\nExecStart=/usr/bin/python3 /opt/baarcha-vps-export-recovery-2c7e700/worker.py\\n');f.flush();os.fsync(f.fileno())
target.chmod(0o644)
subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','enable','--now',timer],check=True,capture_output=True)
assert subprocess.run(['systemctl','is-active',timer],capture_output=True,text=True).stdout.strip()=='active'
print(json.dumps({'enabled':True,'worker':'vps','sha256':EXPECTED,'static_elf_verified':True,'live_probe_count':len(receipts),'wakes_guests':False,'model_calls':False,'b200_contacted':False,'at':time.time()}))
'''
 # Keep the exact reviewed installer for recovery if systemd rejects a step.
 b.atomic(out/'worker-install.py',code.encode())
 result=json.loads(subprocess.check_output(ssh+['python3','-'],input=code.encode(),timeout=360))
 assert json.loads(subprocess.check_output(['/usr/local/libexec/baarcha-cube-worker-start','--observe'],timeout=60))['consistent']
 b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result))
