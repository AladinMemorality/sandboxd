import pathlib,subprocess,json,os,hashlib
os.umask(0o077);r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007')
worker=['ssh','-C','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(r/'worker-known-hosts'),'-i',str(r/'worker-key'),'root@10.254.240.2']
rescue=['ssh','-C','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/rescue-operator-20260925/known_hosts','-i','/opt/baarcha-cube/rescue-operator-20260925/operator-key','-p','20223','root@127.0.0.1']
manifest=json.loads((r/'native/rescue-input.json').read_text())
assert manifest['purpose']=='CUBE_CURRENT_DISK_RESCUE_INPUT'
assert {a['file'] for a in manifest['artifacts']}=={'current.ext4','lower-000.ext4'}
for a in manifest['artifacts']:
 assert (r/'native'/a['file']).stat().st_size==a['bytes']
# The native copies have already passed independent digest checks. The isolated
# exporter hashes both received images again before parsing or mounting them.
subprocess.run(['ionice','-c','2','-n','7','-p',str(os.getpid())],check=True)
os.nice(10)
p=subprocess.Popen(['tar','--sparse','-cf','-','-C',str(r/'native'),'current.ext4','lower-000.ext4','rescue-input.json'],stdout=subprocess.PIPE)
q=subprocess.run(rescue+['umask 077; tar --sparse -xf - -C /var/lib/cube-rescue/vps-sandbox-20261007/input'],stdin=p.stdout);p.stdout.close();assert p.wait()==0 and q.returncode==0
subprocess.run(rescue+['systemd-run --unit=vps-sandbox-rescue-20261007 --property=RuntimeMaxSec=1000 --property=MemoryMax=2800M --property=CPUQuota=200% /bin/bash /var/lib/cube-rescue/vps-sandbox-20261007/run.sh'],check=True)
print('isolated rescue export launched',flush=True)
