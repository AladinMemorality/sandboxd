"""Install bounded, byte-verified VPS snapshot maintenance after acceptance."""
import contextlib,hashlib,importlib.util,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
EXPECTED={'compact-vps-pause-memory.py': '0a185c44dbe36648eeabe7f208971d32f57793110d108998e0aa59c000f2cd94', 'baarcha-vps-pause-compaction.service': 'be47b80e689f2a39968a5b8cfde167a3ea60dd28a98eb21dfdec8e2298cbcb37', 'baarcha-vps-pause-compaction.timer': '269bdc67d09bb2a5838656734afb5b7f976821b73a9a1b55ff9623bb05e942e5'}
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
with contextlib.ExitStack() as stack:
 deadline=time.monotonic()+1800
 while True:
  try:stack.enter_context(b.locked());break
  except BlockingIOError:
   assert time.monotonic()<deadline;time.sleep(2)
 assert json.loads((root/'fleet-wake-validation-01/complete.json').read_text())['complete']
 assert json.loads((root/'real-preview-density-50-balanced-07/result.json').read_text())['passed']
 assert json.loads((root/'real-preview-density-50-balanced-07/cleanup.json').read_text())['complete']
 out=root/'pause-compaction-maintenance-01';out.mkdir(mode=0o700)
 for name,sha in EXPECTED.items():
  p=root/name;assert not p.is_symlink() and p.stat().st_uid==0 and hashlib.sha256(p.read_bytes()).hexdigest()==sha
  target=P('/usr/local/libexec/baarcha-vps-pause-compaction.py') if name.endswith('.py') else P('/etc/systemd/system')/name
  assert not target.exists(),'Maintenance already installed; review its receipt'
  with target.open('xb') as f:f.write(p.read_bytes());f.flush();os.fsync(f.fileno())
  target.chmod(0o644)
 subprocess.run(['systemd-analyze','verify','/etc/systemd/system/baarcha-vps-pause-compaction.service','/etc/systemd/system/baarcha-vps-pause-compaction.timer'],check=True,capture_output=True)
 subprocess.run(['systemctl','daemon-reload'],check=True)
 subprocess.run(['systemctl','enable','--now','baarcha-vps-pause-compaction.timer'],check=True,capture_output=True)
 code="""import json,pathlib,subprocess,time
P=pathlib.Path
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
assert subprocess.check_output(['findmnt','-n','-o','UUID','--target','/data'],text=True).strip()=='793c3349-db9c-4815-9842-989ed484f1f8'
assert subprocess.check_output(['systemctl','is-active','fstrim.timer'],text=True).strip()=='active'
p=P('/etc/systemd/system/fstrim.timer.d/20-vps-sandbox-discard.conf');p.parent.mkdir(exist_ok=True)
with p.open('x') as f:f.write('[Timer]\\nOnCalendar=\\nOnCalendar=hourly\\nAccuracySec=1min\\nRandomizedDelaySec=10min\\n')
p.chmod(0o644)
# A byte-level dedupe creates many tiny free extents. Skip sub-MiB extents
# during scheduled discard; they stay reusable inside XFS.
s=P('/etc/systemd/system/fstrim.service.d/20-vps-sandbox-discard.conf');s.parent.mkdir(exist_ok=True)
with s.open('x') as f:f.write('[Service]\\nExecStart=\\nExecStart=/usr/sbin/fstrim --listed-in /etc/fstab:/proc/self/mountinfo --verbose --quiet-unsupported --minimum 1MiB\\n')
s.chmod(0o644)
subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','restart','fstrim.timer'],check=True)
print(json.dumps({'trim_schedule':'hourly','minimum_extent_bytes':1048576,'worker':'vps','at':time.time()}))
"""
 b.atomic(out/'install-worker-trim.py',code.encode())
 worker=json.loads(subprocess.check_output(ssh+['python3','-'],input=code.encode(),timeout=60))
 result={'installed':True,'scheduled_max_files':32,'interval_minutes':10,'busy_operator_skips':True,'failed_compaction_requires_review':True,'files':EXPECTED,'worker':worker,'b200_contacted':False,'model_calls':False,'at':time.time()}
 b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result))
