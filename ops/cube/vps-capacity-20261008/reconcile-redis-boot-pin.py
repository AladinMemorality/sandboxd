"""VPS worker only: reconcile the previously deployed Redis pool boot image.

The September release updated .one-click.env but omitted its lifecycle hash.
Require its saved original to match the existing pin, and prove the deployed
change consists solely of that release's exact immutable proxy image.
"""
import fcntl, hashlib, json, os, re, shlex, subprocess
from pathlib import Path
os.umask(0o077)
lock=open('/run/lock/cube-operator-acceptance.lock','a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
root=Path('/opt/baarcha-bench/preview-transport-20260929/redis-pool-release')
path=Path('/usr/local/services/cubetoolbox/.one-click.env')
config=Path('/etc/baarcha-cube/lifecycle.json')
before=(root/'boot-config.before.PRIVATE').read_bytes()
old=config.read_bytes();value=json.loads(old)
sha=lambda b:hashlib.sha256(b).hexdigest()
assert sha(before)==value['artifacts'][str(path)]
receipt=json.loads((root/'deployed.json').read_bytes());assert receipt['deployed'] and receipt['worker']=='vps'
candidate=receipt['future_boot_image']
assert re.fullmatch(r'sha256:[a-f0-9]{64}',candidate)
expected=re.sub(r'(?m)^((?:export )?CUBE_SANDBOX_CUBE_PROXY_IMAGE=)([^\n]*)$',lambda m:m[1]+shlex.quote(candidate),before.decode(),count=1).encode()
assert expected==path.read_bytes()
image=json.loads(subprocess.check_output(['docker','image','inspect',candidate]))[0]
assert image['Id']==candidate
assert not (root/'lifecycle-before-capacity.PRIVATE.json').exists()
(root/'lifecycle-before-capacity.PRIVATE.json').write_bytes(old)
value['artifacts'][str(path)]=sha(expected)
pending=config.with_suffix('.capacity-pending');pending.write_text(json.dumps(value));pending.chmod(0o600);os.replace(pending,config)
print(json.dumps({'reconciled':True,'only_change':'previously deployed immutable Redis proxy image','sha256':sha(expected)}))
