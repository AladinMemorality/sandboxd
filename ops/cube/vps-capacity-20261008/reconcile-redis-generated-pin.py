"""Pin the generated proxy Compose file after its already approved image boots."""
import fcntl,hashlib,json,os,re,shlex,subprocess
from pathlib import Path
os.umask(0o077)
lock=open('/run/lock/cube-operator-acceptance.lock','a');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
root=Path('/opt/baarcha-bench/preview-transport-20260929/redis-pool-release')
receipt=json.loads((root/'deployed.json').read_bytes());assert receipt['worker']=='vps' and receipt['deployed']
oldenv=(root/'boot-config.before.PRIVATE').read_text()
match=re.search(r'(?m)^(?:export )?CUBE_SANDBOX_CUBE_PROXY_IMAGE=([^\n]*)$',oldenv);assert match
oldimage=shlex.split(match[1],comments=True)[0];candidate=receipt['future_boot_image']
path=Path('/usr/local/services/cubetoolbox/cubeproxy/docker-compose.yaml')
raw=path.read_bytes();assert raw.count(candidate.encode())==1
previous=raw.replace(candidate.encode(),oldimage.encode())
config=Path('/etc/baarcha-cube/lifecycle.json');old=config.read_bytes();value=json.loads(old)
sha=lambda b:hashlib.sha256(b).hexdigest()
assert sha(previous)==value['artifacts'][str(path)],'generated config changed beyond approved image'
container=json.loads(subprocess.check_output(['docker','inspect','cube-proxy']))[0]
assert container['Image']==candidate and container['State']['Running']
with (root/'lifecycle-before-generated-pin.PRIVATE.json').open('xb') as file:file.write(old)
value['artifacts'][str(path)]=sha(raw)
pending=config.with_suffix('.generated-pin');pending.write_text(json.dumps(value));pending.chmod(0o600);os.replace(pending,config)
print(json.dumps({'reconciled':True,'only_change':'approved proxy image in generated Compose','sha256':sha(raw)}))
