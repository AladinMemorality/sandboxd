import pathlib,subprocess,json,re
r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007')
ssh=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=5','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/rescue-operator-20260925/known_hosts','-i','/opt/baarcha-cube/rescue-operator-20260925/operator-key','-p','20223','root@127.0.0.1']
remote='/var/lib/cube-rescue/vps-sandbox-20261007'
manifest=json.loads((r/'native/rescue-input.json').read_text());assert manifest['sandbox_id']=='f51584459b354470b04c966ae8429293'
digest=manifest['artifacts'][0]['sha256'];assert re.fullmatch('[a-f0-9]{64}',digest)
code="""import pathlib,subprocess
r=pathlib.Path('/var/lib/cube-rescue/vps-sandbox-20261007')
assert (r/'diagnostic-type').read_text().strip()=='ext4' and (r/'diagnostic-fsck-status').read_text().strip()=='4'
assert (r/'exit-status').read_text().strip()=='1'
assert str(r/'export') not in pathlib.Path('/proc/self/mountinfo').read_text()
p=r/'attempt1';p.mkdir(mode=0o700)
for n in ['export','exit-status','export.log','diagnostic-type','diagnostic-blkid-status','diagnostic-fsck-status','diagnostic-fsck.log']:(r/n).rename(p/n)
s=(r/'run.sh').read_text();needle='--work "$stage/export"';assert s.count(needle)==1
s=s.replace(needle,needle+' --repair-current-sha256 DIGEST')
(r/'run-repair.sh').write_text(s)
""".replace('DIGEST',digest)
subprocess.run(ssh+['python3 -'],input=code.encode(),check=True)
subprocess.run(ssh+['systemd-run --unit=vps-sandbox-rescue-repair-20261007 --property=RuntimeMaxSec=1000 --property=MemoryMax=2800M --property=CPUQuota=200% /bin/bash '+remote+'/run-repair.sh'],check=True)
print('explicit repair of fresh disposable clone launched; captured and original disks retained')
