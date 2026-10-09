"""Preserve a failed capture and omit only regenerable PostgreSQL counters."""
import base64,hashlib,importlib.util,json,os,pathlib,subprocess,tempfile,time,shutil
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
old='e1f4895ba9cdd2f9bcd791be0da9ead35caae043ca41d1ca4178128619002d09';new='ad60e4c4ef79a1eaf59ee3db9583563f92cd8f13d714fd99c570cb5321965fe6'
with b.locked():
 out=root/'source-backup-stats-76';out.mkdir(mode=0o700)
 log=(root/'source-backup-diagnostic-70/guestfs.PRIVATE.log').read_text()
 assert 'tar: ./.baarcha-postgres/data/pg_stat/pgstat.stat: Cannot stat: Structure needs cleaning' in log
 assert [s for s in log.splitlines() if s.startswith('tar: ') and 'Exiting with failure' not in s]==['tar: ./.baarcha-postgres/data/pg_stat/pgstat.stat: Cannot stat: Structure needs cleaning']
 data=(root/'source-backup-exporter-stats.py').read_bytes();assert hashlib.sha256(data).hexdigest()==new
 with tempfile.TemporaryDirectory() as d:
  p=P(d);(p/'export-source.py').write_bytes(data);shutil.copy2(root/'test_source_backup_exclusions.py',p/'test_cache_exclusions.py')
  subprocess.run(['python3',str(p/'test_cache_exclusions.py')],check=True)
 code='DATA='+repr(base64.b64encode(data).decode())+'\nOLD='+repr(old)+'\nNEW='+repr(new)+'\n'+'''import base64,hashlib,importlib.util,json,os,pathlib,shutil,subprocess,sys,time
P=pathlib.Path;os.umask(0o077);assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
capture=P('/data/baarcha-source-generations/20261009T200641Z');sid='01M3D1Q0E1KM1FEM244XVHEC65'
p=P('/root/baarcha-source-backup-tools/export-source.py');prior=p.read_bytes();assert hashlib.sha256(prior).hexdigest()==OLD
out=P('/data/baarcha-source-diagnostics/20261009-pgstat');out.mkdir(mode=0o700,parents=True)
sys.argv=['export-source.py',str(capture),sid]
spec=importlib.util.spec_from_file_location('old_export',p);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
row=next(r for r in m.rows if r['sandbox_id']==sid);_,_,disk,lower,_=m.inputs(row)
for name,source in [('rootfs.ext4',disk),('base.ext4',lower)]:
 target=out/name;assert not target.exists();subprocess.run(['cp','--reflink=always','--',str(source),str(target)],check=True);target.chmod(0o600)
 assert source.stat().st_size==target.stat().st_size
shutil.copytree(capture/'export-diagnostic-70',out/'diagnostic-70')
(out/'coverage.PRIVATE.json').write_text(json.dumps(row))
folder=capture/'exports'/sid;shutil.copy2(folder/'failure.json',out/'failure-70.json');os.rename(folder/'home.tar.gz.partial',out/'home-after-diagnostic.tar.gz.partial')
backup=p.with_name(p.name+'.before-pgstat-20261009');assert not backup.exists();backup.write_bytes(prior);backup.chmod(0o600)
data=base64.b64decode(DATA);assert hashlib.sha256(data).hexdigest()==NEW;compile(data,str(p),'exec')
tmp=p.with_name(p.name+'.pending-pgstat');assert not tmp.exists();tmp.write_bytes(data);tmp.chmod(0o700);os.replace(tmp,p)
spec=importlib.util.spec_from_file_location('new_export',p);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);m.main()
receipt=json.loads((folder/'receipt.json').read_text());assert receipt['sandbox_id']==sid
print(json.dumps({'passed':True,'sandbox_id':sid,'bytes':receipt['bytes'],'retained_failed_capture':str(out),'excluded_only_postgres_counters':True,'exporter_sha256':NEW,'at':time.time()}))
'''
 b.atomic(out/'worker.py',code.encode())
 ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
 p=subprocess.run(ssh+['python3 -'],input=code.encode(),capture_output=True,timeout=600);b.atomic(out/'export.PRIVATE.log',p.stdout+p.stderr);assert p.returncode==0
 proof=json.loads(p.stdout.splitlines()[-1]);assert proof['passed'];b.atomic(out/'complete.json',b.encoded(proof));print(json.dumps(proof),flush=True)
