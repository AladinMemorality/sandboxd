"""Diagnose one rejected immutable source capture after the fleet export ends."""
import importlib.util,json,os,pathlib,shlex,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
unit='baarcha-vps-final-acceptance-50';generation='20261009T200641Z';sid='01M3D1Q0E1KM1FEM244XVHEC65'
while subprocess.check_output(['systemctl','show',unit,'-p','ActiveState','--value'],text=True).strip() in ('activating','active','deactivating'):time.sleep(10)
assert subprocess.check_output(['systemctl','show',unit,'-p','ActiveState','--value'],text=True).strip()=='failed'
backup=P('/var/backups/baarcha-vps-source')/generation;assert (backup/'FAILED.json').exists() and not (backup/'VERIFIED.json').exists()
with b.locked():
 out=root/'source-backup-diagnostic-70';out.mkdir(mode=0o700)
 code='GENERATION='+repr(generation)+'\nSID='+repr(sid)+'\n'+'''import hashlib,importlib.util,json,os,pathlib,shutil,sys,time,guestfs
P=pathlib.Path;os.umask(0o077)
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
capture=P('/data/baarcha-source-generations')/GENERATION
failures=json.loads((capture/'exports/failures.json').read_text())
assert len(failures)==1 and failures[0]['sandbox_id']==SID and failures[0]['message']=='file receive cancelled by daemon'
out=capture/'export-diagnostic-70';out.mkdir(mode=0o700)
for name in ['failures.json','receipts.json','progress.json']:
 shutil.copy2(capture/'exports'/name,out/name)
folder=capture/'exports'/SID
for name in ['failure.json','progress.json']:shutil.copy2(folder/name,out/name)
partial=folder/'home.tar.gz.partial';assert partial.exists()
with partial.open('rb') as f:digest=hashlib.file_digest(f,'sha256').hexdigest()
(out/'partial-before.json').write_text(json.dumps({'sha256':digest,'bytes':partial.stat().st_size,'at':time.time()}))
os.rename(partial,out/'home.tar.gz.partial')
factory=guestfs.GuestFS
def verbose(*args,**kwargs):
 g=factory(*args,**kwargs);g.set_verbose(True);return g
guestfs.GuestFS=verbose
sys.argv=['export-source.py',str(capture),SID]
spec=importlib.util.spec_from_file_location('export','/root/baarcha-source-backup-tools/export-source.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);m.main()
receipt=json.loads((folder/'receipt.json').read_text());assert receipt['sandbox_id']==SID
print(json.dumps({'diagnostic_export_passed':True,'sandbox_id':SID,'bytes':receipt['bytes']}))
'''
 b.atomic(out/'diagnose.py',code.encode())
 ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
 with (out/'guestfs.PRIVATE.log').open('wb') as log:
  result=subprocess.run(ssh+['python3 -'],input=code.encode(),stdout=log,stderr=subprocess.STDOUT,timeout=600)
 b.atomic(out/'result.json',b.encoded({'returncode':result.returncode,'at':time.time(),'generation':generation,'sandbox_id':sid}))
 assert result.returncode==0,'Review private guestfs diagnostic; no archive accepted'
