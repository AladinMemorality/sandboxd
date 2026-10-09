"""Approve one reproduced production supervisor; preserve the rejected attempt."""
import importlib.util,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
with b.locked():
 proof=json.loads((root/'b300f9-source-review.json').read_text())
 old='b300f9ce69e2377851c978bb546ac30e553abae388f7ac38807764189eb78434'
 assert proof['approved_previous_sha256']==proof['normalized_reproduced_sha256']==old and proof['all_other_bytes_identical'] and proof['only_go_build_id_differs']
 assert proof['source_revision']=='d7ee603' and proof['static_elf_verified'] and proof['config_and_quiescence_contract_unchanged']
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  assert db.execute("select count(*) from task where status in ('running','queued')").fetchone()[0]==0
  state,runtime,admission,charged=db.execute("select s.status,b.runtime_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id='01M3CKN99ZF90BEEA4DS66YAQV'").fetchone()
  assert runtime=='6516e4dc614d4a1990360e186344c709' and (state,admission,charged) in [('stopped','released',0),('running','active',1)]
 artifact=root/'review-motion-supervisor/capture-02/runtimed';assert __import__('hashlib').sha256(artifact.read_bytes()).hexdigest()==old
 out=root/'review-b300f9';out.mkdir(mode=0o700)
 code='OLD='+repr(old)+'\n'+'''import hashlib,importlib.util,json,os,pathlib
P=pathlib.Path;os.umask(0o077)
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
root=P('/opt/baarcha-vps-export-recovery-2c7e700');p=root/'release.json'
assert not p.is_symlink() and p.stat().st_uid==0 and p.stat().st_mode&0o777==0o600
before=p.read_bytes();cfg=json.loads(before);assert cfg['sha256']=='8bbddf74142ed5ffe1cc3c8ddcd1883fde66e165bddea8bcaf0d6aeba3c26669' and OLD not in cfg['previous']
saved=p.with_name('release.before-b300f9.json')
with saved.open('xb') as f:f.write(before);f.flush();os.fsync(f.fileno())
cfg['previous'].append(OLD);data=(json.dumps(cfg,indent=2)+'\\n').encode();temp=p.with_name('release.pending-b300f9.json')
with temp.open('xb') as f:f.write(data);f.flush();os.fsync(f.fileno())
os.replace(temp,p);fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY)
try:os.fsync(fd)
finally:os.close(fd)
print(json.dumps({'manifest_before_sha256':hashlib.sha256(before).hexdigest(),'manifest_after_sha256':hashlib.sha256(data).hexdigest(),'approved_previous_sha256':OLD}))
'''
 b.atomic(out/'worker-install.py',code.encode())
 result=json.loads(subprocess.check_output(ssh+['python3','-'],input=code.encode(),timeout=30))
 proof.update(result);proof.update(reviewed=True,worker='vps',b200_contacted=False,at=time.time())
 b.atomic(out/'complete.json',b.encoded(proof));print(json.dumps(proof))
