"""Stage, but do not activate, the reviewed static source-export supervisor."""
import hashlib,io,json,os,pathlib,shlex,subprocess,tarfile,time
P=pathlib.Path;os.umask(0o077);root=P('/opt/baarcha/operations/vps-50-profiles-20261008');release=root/'source-data-release-3b1a6f0'
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
deadline=time.monotonic()+10800
while not (release/'built.json').exists():
 state=subprocess.check_output(['systemctl','show','baarcha-vps-source-data-build-51','-p','ActiveState','--value'],text=True).strip()
 assert state!='failed' and time.monotonic()<deadline
 time.sleep(5)
assert not (release/'supervisor-staged.json').exists()
binary=(release/'runtimed').read_bytes();built=json.loads((release/'built.json').read_text());assert hashlib.sha256(binary).hexdigest()==built['runtimed_sha256']
old=json.loads(subprocess.check_output(ssh+['cat /opt/baarcha-vps-export-recovery-2c7e700/release.json'],timeout=30))
new={**old,'revision':'3b1a6f0','sha256':built['runtimed_sha256'],'bytes':len(binary),'previous':sorted(set(old['previous']+[old['sha256']]))}
source=release/'source/ops/cube/published-runtime'
worker=(source/'worker.py').read_bytes().replace(b"ROOT=P('/opt/baarcha-published-runtime')",b"ROOT=P('/opt/baarcha-vps-source-data-3b1a6f0')")
assert b"ROOT=P('/opt/baarcha-vps-source-data-3b1a6f0')" in worker
files={'runtimed':binary,'guest.py':(source/'guest.py').read_bytes(),'worker.py':worker,'release.json':json.dumps(new).encode()}
buffer=io.BytesIO()
with tarfile.open(fileobj=buffer,mode='w') as tar:
 for name,data in files.items():
  entry=tarfile.TarInfo(name);entry.size=len(data);entry.mode=0o755 if name=='runtimed' else 0o600;tar.addfile(entry,io.BytesIO(data))
code='EXPECTED='+repr({name:hashlib.sha256(data).hexdigest() for name,data in files.items()})+'\n'+'''import hashlib,io,json,pathlib,sys,tarfile,importlib.util
P=pathlib.Path
assert P('/etc/machine-id').read_text().strip()=='2b9e31d4abd345e3bd4b966591e61296'
root=P('/opt/baarcha-vps-source-data-3b1a6f0');root.mkdir(mode=0o700)
with tarfile.open(fileobj=io.BytesIO(sys.stdin.buffer.read()),mode='r:') as tar:
 entries=tar.getmembers();assert {e.name for e in entries}==set(EXPECTED) and len(entries)==4
 for e in entries:
  assert e.isfile();data=tar.extractfile(e).read();assert hashlib.sha256(data).hexdigest()==EXPECTED[e.name]
  target=root/e.name
  with target.open('xb') as f:f.write(data)
  target.chmod(0o755 if e.name=='runtimed' else 0o600)
spec=importlib.util.spec_from_file_location('guest',root/'guest.py');g=importlib.util.module_from_spec(spec);spec.loader.exec_module(g);g.require_static_supervisor((root/'runtimed').read_bytes())
print(json.dumps({'staged':True,'static_elf_verified':True,'activated':False,'files':EXPECTED}))
'''
p=subprocess.run(ssh+['python3 -c '+shlex.quote(code)],input=buffer.getvalue(),capture_output=True,timeout=120)
(release/'supervisor-stage.PRIVATE.log').write_bytes(p.stdout+p.stderr);assert p.returncode==0
proof=json.loads(p.stdout);(release/'supervisor-release.json').write_text(json.dumps(new));(release/'supervisor-staged.json').write_text(json.dumps(proof));print(json.dumps({'staged':True,'sha256':new['sha256']}),flush=True)
