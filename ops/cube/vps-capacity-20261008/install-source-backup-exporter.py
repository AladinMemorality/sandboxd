"""Install the reviewed cache exclusions on the VPS backup worker only."""
import base64,hashlib,importlib.util,json,os,pathlib,subprocess
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
old='e5baabd8a8694bbbf8b45db4315aa5831ac8a258bfb560257c973adde8572a2f';new='e1f4895ba9cdd2f9bcd791be0da9ead35caae043ca41d1ca4178128619002d09'
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
with b.locked():
 data=(root/'source-backup-exporter-pnpm.py').read_bytes();assert hashlib.sha256(data).hexdigest()==new;compile(data,'export-source.py','exec')
 code="""
import base64,hashlib,json,os,pathlib
p=pathlib.Path('/root/baarcha-source-backup-tools/export-source.py');data=base64.b64decode(DATA);old=OLD;new=NEW
assert pathlib.Path('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
assert not p.is_symlink() and p.stat().st_uid==0
prior=p.read_bytes();assert hashlib.sha256(prior).hexdigest()==old
backup=p.with_name(p.name+'.before-20261009-pnpm-cache');assert not backup.exists()
with backup.open('xb') as f:f.write(prior);f.flush();os.fsync(f.fileno())
backup.chmod(0o600)
assert hashlib.sha256(data).hexdigest()==new;compile(data,str(p),'exec')
tmp=p.with_name(p.name+'.pending-pnpm-cache')
with tmp.open('xb') as f:f.write(data);f.flush();os.fsync(f.fileno())
tmp.chmod(p.stat().st_mode & 0o777);os.replace(tmp,p)
assert hashlib.sha256(p.read_bytes()).hexdigest()==new
print(json.dumps({'installed':True,'old_sha256':old,'new_sha256':new,'worker':'vps','b200_contacted':False}))
"""
 code='DATA='+repr(base64.b64encode(data).decode())+'\nOLD='+repr(old)+'\nNEW='+repr(new)+'\n'+code
 result=json.loads(subprocess.check_output(ssh+['python3 -'],input=code.encode(),timeout=30));b.atomic(root/'source-backup-exporter-installed.json',b.encoded(result));print(json.dumps(result))
