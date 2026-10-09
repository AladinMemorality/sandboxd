"""Finish reviewed binaries after correcting an invalid Docker base digest."""
import hashlib,json,os,pathlib,subprocess,time
P=pathlib.Path;os.umask(0o077)
root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'source-data-release-867d7de';revision='867d7de'
assert not (out/'built.json').exists() and not (out/'image.id').exists()
assert 'ok  \tgithub.com/tastyeffectco/sandboxd/control-plane/internal/runtime' in (out/'tests.log').read_text()
base='sha256:028b53215b95194140bfbb0356e1d6e1e1cee47f8b42a2fe5e21f7d1966e70aa'
assert subprocess.check_output(['docker','inspect','src-sandboxd-1','--format','{{.Image}}'],text=True).strip()==base
assert subprocess.check_output(['docker','image','inspect',base,'--format','{{.Id}}'],text=True).strip()==base
old=(out/'Dockerfile').read_text();assert old.startswith('FROM sha256:028b53215b95194140bfbb0356e1d6e1cee47f8b42a2fe5e21f7d1966e70aa\n')
(out/'Dockerfile.failed-56').write_text(old)
(out/'Dockerfile').write_text('FROM '+base+'\n'+old.split('\n',1)[1])
proof={'reason':'corrected mistyped base digest; existing tests and binaries retained','base':base,'binary_sha256':{n:hashlib.sha256((out/n).read_bytes()).hexdigest() for n in ['cube-controller','runtimed']},'at':time.time()}
(out/'packaging-resume-64.json').write_text(json.dumps(proof))
subprocess.run(['docker','build','--network=none','--pull=false','-t','baarcha-cube-controller:source-data-'+revision,'--iidfile',str(out/'image.id'),str(out)],check=True,env={**os.environ,'DOCKER_BUILDKIT':'0'})
binary=(out/'runtimed').read_bytes()
result={'revision':revision,'image':(out/'image.id').read_text().strip(),'runtimed_sha256':hashlib.sha256(binary).hexdigest(),'runtimed_bytes':len(binary),'tests_passed':True,'b200_contacted':False,'at':time.time()}
(out/'built.json').write_text(json.dumps(result));print(json.dumps(result),flush=True)
