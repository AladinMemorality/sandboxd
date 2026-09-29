#!/usr/bin/env python3
"""Run inside a native worker, under the coordinator's operator locks."""
import argparse, hashlib, json, os, pathlib, re, shlex, subprocess, urllib.request
P=pathlib.Path
ROOT=P('/opt/baarcha-bench/preview-transport-20260929/redis-pool-release')
BASE='sha256:b5a196be3698d820c1ae70a3a7a9fd0439badd43e74a2c4954255f7dc69f86dc'
OLD='e385b47d33af980299f1108399fe31a0d7d626fc57a5305f4da91083bf905771'
MODULE='/usr/local/openresty/nginx/lua/redis_iresty.lua'
os.umask(0o077)
def run(args):
 r=subprocess.run(args,capture_output=True,timeout=60)
 if r.returncode:raise RuntimeError('native proxy command failed: '+args[0])
 return r.stdout
def put(p,b):
 temp=p.with_name(p.name+'.redis-pool-new');temp.write_bytes(b);temp.chmod(0o600);os.replace(temp,p)
def main():
 parser=argparse.ArgumentParser();parser.add_argument('--worker',choices=['vps','b200'],required=True);parser.add_argument('--check',action='store_true');args=parser.parse_args()
 name='cube-proxy' if args.worker=='vps' else 'cube-proxy-fleet'
 health='http://10.0.2.15:8082/admin/healthz' if args.worker=='vps' else 'http://10.254.240.2:28082/admin/healthz'
 cfg=P('/usr/local/services/cubetoolbox/.one-click.env' if args.worker=='vps' else '/usr/local/services/cubetoolbox/cubeproxy-fleet/compose.json')
 before=cfg.read_bytes();inspected=json.loads(run(['docker','inspect',name]))[0]
 assert inspected['Image']==BASE and inspected['State']['Running']
 old=run(['docker','exec',name,'cat',MODULE]);assert hashlib.sha256(old).hexdigest()==OLD
 candidate=(ROOT/'image.id').read_text().strip();assert json.loads(run(['docker','image','inspect',candidate]))[0]['Id']==candidate
 desired=(ROOT/'CubeProxy/lua/redis_iresty.lua').read_bytes();digest=hashlib.sha256(desired).hexdigest();assert digest!=OLD
 # Both immutable boot configuration and current process get exactly one module.
 if args.worker=='vps':
  text=before.decode();pattern=r'(?m)^((?:export )?CUBE_SANDBOX_CUBE_PROXY_IMAGE=)([^\n]*)$';match=re.search(pattern,text);assert match
  previous_image=shlex.split(match[2],comments=True)[0]
  assert json.loads(run(['docker','image','inspect',previous_image]))[0]['Id']==BASE
  after=re.sub(pattern,lambda m:m[1]+shlex.quote(candidate),text,count=1).encode()
 else:
  compose=json.loads(before);matches=[v for v in compose['services'].values() if v.get('image') and json.loads(run(['docker','image','inspect',v['image']]))[0]['Id']==BASE]
  assert len(matches)==1;matches[0]['image']=candidate;after=json.dumps(compose,indent=2).encode()
 with urllib.request.urlopen(health,timeout=5) as r:assert r.status==200
 # Real Redis read-only PING checks use local private configuration. No key or
 # password is printed or placed in process arguments.
 conf=run(['docker','exec',name,'cat','/usr/local/openresty/nginx/conf/global/global.conf']).decode()
 def variable(name):
  match=re.search(r'\bset\s+\$'+name+r'\s+([^;]+);',conf);assert match
  return shlex.split(match[1])[0]
 options={'redis_ip':variable('redis_ip'),'redis_port':int(variable('redis_port')),'redis_pd':variable('redis_pd'),'redis_index':int(variable('redis_index')),'timeout':5000}
 secret=ROOT/'redis-test.PRIVATE.json';put(secret,json.dumps(options).encode())
 try:
  output=run(['docker','run','--rm','--network=host','--entrypoint','/usr/local/openresty/bin/resty','-v',str(secret)+':/private.json:ro','-v',str(ROOT/'CubeProxy/tests')+':/tests:ro',candidate,'-I','/usr/local/openresty/nginx/lua','/tests/redis_iresty_live_pool.lua','/private.json'])
  result=json.loads(output);assert result['credential_isolation']
 finally:secret.unlink()
 if args.check:print(json.dumps({'preflight':'pass','worker':args.worker,**result}));return
 assert not (ROOT/'deployed.json').exists(),'Release already applied'
 put(ROOT/'boot-config.before.PRIVATE',before);put(ROOT/'redis_iresty.before.lua',old)
 def install(file):
  run(['docker','cp',str(file),name+':'+MODULE+'.new'])
  run(['docker','exec',name,'chmod','0644',MODULE+'.new'])
  run(['docker','exec',name,'mv',MODULE+'.new',MODULE])
  run(['docker','exec',name,'/usr/local/openresty/nginx/sbin/nginx','-t'])
  run(['docker','exec',name,'/usr/local/openresty/nginx/sbin/nginx','-s','reload'])
 try:
  put(cfg,after);install(ROOT/'CubeProxy/lua/redis_iresty.lua')
  assert hashlib.sha256(run(['docker','exec',name,'cat',MODULE])).hexdigest()==digest
  with urllib.request.urlopen(health,timeout=5) as r:assert r.status==200
  receipt={'deployed':True,'worker':args.worker,'current_container_base':BASE,'future_boot_image':candidate,'live_module_sha256':digest,'graceful_reload':True,**result}
  put(ROOT/'deployed.json',json.dumps(receipt).encode());print(json.dumps(receipt))
 except BaseException:
  put(cfg,before);install(ROOT/'redis_iresty.before.lua')
  raise
if __name__=='__main__':main()
