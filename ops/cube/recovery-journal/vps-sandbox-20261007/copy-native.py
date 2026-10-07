import pathlib,subprocess,json,os,hashlib
os.umask(0o077);r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007')
worker=['ssh','-C','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','root@127.0.0.1']
rescue=['ssh','-C','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/rescue-operator-20260925/known_hosts','-i','/opt/baarcha-cube/rescue-operator-20260925/operator-key','-p','20223','root@127.0.0.1']
(r/'native').mkdir(mode=0o700)
p=subprocess.Popen(worker+['tar --sparse -cf - -C /data/cube-recovery/vps-sandbox-20261007/capture current.ext4 lower-000.ext4 rescue-input.json'],stdout=subprocess.PIPE)
q=subprocess.run(['tar','--sparse','-xf','-','-C',str(r/'native')],stdin=p.stdout);p.stdout.close();assert p.wait()==0 and q.returncode==0
manifest=json.loads((r/'native/rescue-input.json').read_text())
for a in manifest['artifacts']:
 with (r/'native'/a['file']).open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==a['sha256']
print('current disk and immutable lower copied to VPS; both hashes verified',flush=True)
