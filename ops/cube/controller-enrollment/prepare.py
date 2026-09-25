#!/usr/bin/env python3
"""Render a private, single-app canary Compose candidate; never install or start."""
import argparse,contextlib,copy,fcntl,grp,hashlib,ipaddress,json,os,pathlib,re,sqlite3,stat,subprocess
P=pathlib.Path
SRC=P('/opt/sandboxd/src');STATE=P('/opt/sandboxd/deploy-state')
CP='54f9d073a7c2739b55481d5bbbb80f22847cb1e1d3225fe5af9590f2f48de5f3'
IMAGE='sha256:14e9b9244729875d15b95082c100b61582c929b48deba1ecd60b38729f344f82'
RELAY='sha256:0b0c246afeb8d343d25890bfe62d5f8af8aee528cb034a596733c3b338408327'
BOOT='c1b590df-28d3-4222-b9a3-c0408c8ccc07'
PROVENANCE={'ordinary_report_sha256':'da7c003293c343d0566221cccea842b2354dec7099b0aae31dfa3469bd241232','anonymous_kernel_sha256':'336fe1ea29c70681cbe0a32db9e99686ea28678771ae71d9dd59bc7c579421cd','loaded_provenance_sha256':'cb7eec1edbee6e28003377214ca2b02fe5ec0af856ecea37eddc581b0aa9aa80','de3_summary_sha256':'a7e5c20f9c0d64535eca3dfc614897fc076afcaefd704d8151f6cc79c3926020'}
def need(v,m):
 if not v:raise ValueError(m)
def sha(p):return hashlib.sha256(P(p).read_bytes()).hexdigest()
def read(p):
 fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW)
 with os.fdopen(fd,'rb') as f:
  s=os.fstat(f.fileno());need(stat.S_ISREG(s.st_mode) and s.st_uid==0 and not s.st_mode&0o022 and s.st_size<1024*1024,'unsafe private input');raw=f.read(1024*1024)
 return json.loads(raw)
def decision_valid(d):
 need(d.get('version')==1 and d.get('reviewed') is True and d.get('scope')=='single-owned-synthetic-app' and d.get('global_rollout') is False,'scoped operator decision required')
 need(re.fullmatch('[0-9A-HJKMNP-TV-Z]{26}',d.get('app_id','')) is not None,'exact canonical app ID required')
 need(all(isinstance(d.get(k),str) and 0<len(d[k])<=256 for k in ['external_user_id','external_project_id']),'exact fixture ownership required')
 need(d.get('direct_guest_egress')=='deny-all' and d.get('reverse_egress') is True and d.get('logical_model_origin')=='https://cube-model.baarcha.tn','reviewed reverse-only routing required')
 need(d.get('worker_boot_id')==BOOT and d.get('cubelet_sha256')=='de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b' and d.get('network_provenance')==PROVENANCE,'reviewed worker evidence required')
def validate_app(db,d):
 row=db.execute('SELECT runtime_preset,external_user_id,external_project_id FROM app WHERE id=?',(d['app_id'],)).fetchone()
 need(row==('node-postgres',d['external_user_id'],d['external_project_id']),'owned synthetic app identity/preset differs')
 need(db.execute('SELECT count(*) FROM sandbox WHERE app_id=?',(d['app_id'],)).fetchone()[0]==0,'fixture already has a sandbox')
 need(db.execute('SELECT count(*) FROM runtime_binding').fetchone()[0]==0,'existing canonical Cube binding')
def render(existing,active,d,stop,templates,relays):
 decision_valid(d)
 need(stop['controller_id']==CP and stop['worker_boot_id']==BOOT,'stop generation differs')
 need(stop['admission']['max_active']==4 and stop['admission']['writable_disk_mb']==10240 and stop['admission']['storage_guard']['expected_boot_id']==BOOT,'guarded four-slot profile required')
 ids={r['preset']:r['template_id'] for r in templates}
 need(set(ids)=={'node-postgres','react-pro','marketplace','react-vite','nextjs','node-express','fastapi','worker'} and ids.get('node-postgres')=='tpl-ce1ee426e686460bbc8c3bfc' and set(ids.values())==set(stop['admission']['templates']),'reviewed template set required')
 for r in templates:need(r['state']=='READY' and r['cpu']==2 and r['memory_mb']==2048 and r['writable_layer']=='10Gi' and r['deny_out']==['0.0.0.0/0'],'template contract differs')
 out=copy.deepcopy(existing);svc=out.setdefault('services',{});controller=svc.setdefault('sandboxd',{})
 env=controller.setdefault('environment',{});need(isinstance(env,dict),'mapping environment required')
 values={'SANDBOXD_CUBE_ENABLED':'true','SANDBOXD_CUBE_ROLLOUT':'allowlist','SANDBOXD_CUBE_APP_IDS':d['app_id'],'SANDBOXD_CUBE_API_URL':'http://127.0.0.1:20300','SANDBOXD_CUBE_PROXY_URL':'http://127.0.0.1:20080','SANDBOXD_CUBE_API_KEY':stop['api_key'],'SANDBOXD_CUBE_DOMAIN':'cube.app','SANDBOXD_CUBE_TEMPLATES':json.dumps(ids,separators=(',',':')),'SANDBOXD_CUBE_ADMISSION':json.dumps(stop['admission'],separators=(',',':')),'SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS':'','SANDBOXD_CUBE_AGENT_RELAY_ORIGIN':'https://cube-model.baarcha.tn','SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED':'true','SANDBOXD_CUBE_REVERSE_EGRESS':'true','SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE':'proxy-http-v1','SANDBOXD_CUBE_BRIDGE_URL':'https://baarcha.tn/api/bridge','SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS':'65.108.225.153/32,104.21.33.103/32,172.67.189.206/32,104.21.18.94/32,172.67.181.138/32','SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS':'baarcha.tn,cube-model.baarcha.tn,hh1.dovisual.com,bp.tn,cube.app,preview.65.108.225.153.sslip.io','SANDBOXD_CUBE_APP_HTTP_SERVICES':'{}'}
 env.update(values);controller['image']=IMAGE
 volumes=controller.setdefault('volumes',[])
 need(all(isinstance(v,dict) and v.get('target')!='/run/sandboxd-cube-storage' for v in volumes),'existing storage mount needs explicit review')
 volumes.append({'type':'bind','source':'/run/sandboxd-cube-storage','target':'/run/sandboxd-cube-storage','read_only':True,'bind':{'create_host_path':False}})
 for name in ['cube-management-api','cube-management-proxy']:
  need(name not in svc,'existing relay needs explicit review');s=copy.deepcopy(relays[name])
  need(s['image']==RELAY and s['network_mode']=='service:sandboxd' and not s.get('ports') and s.get('read_only') is True,'invalid relay boundary');svc[name]=s
 new_active=copy.deepcopy(active);ac=new_active.setdefault('services',{}).setdefault('sandboxd',{});ac['image']=IMAGE;ac.setdefault('environment',{}).update(values)
 return out,new_active,values
def write(p,x):
 raw=(json.dumps(x,indent=2)+'\n').encode();fd=os.open(p,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
def main():
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--decision',required=True,type=P);p.add_argument('--out',required=True,type=P);args=p.parse_args()
 os.umask(0o077);need(os.geteuid()==0 and args.out.is_absolute() and args.out.parent.resolve()==args.out.parent and not args.out.exists(),'new canonical private directory required')
 with contextlib.ExitStack() as stack:
  for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
   path=P(name);s=path.lstat();need(path.resolve()==path and s.st_uid==0 and not s.st_mode&0o022,'unsafe lock');fd=os.open(path,os.O_RDWR|os.O_NOFOLLOW);stack.callback(os.close,fd);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  d=read(args.decision);decision_valid(d)
  cp=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];live=dict(x.split('=',1) for x in cp['Config']['Env'] if '=' in x)
  need(cp['Id']==CP and cp['Image']==IMAGE and cp['State']['Running'] and live.get('SANDBOXD_CUBE_ENABLED')=='false' and live.get('SANDBOXD_CUBE_REVERSE_EGRESS')=='false','controller generation/rollout changed')
  need(grp.getgrnam('cube-management').gr_gid==982,'socket GID differs')
  subprocess.run(['docker','image','inspect',RELAY],check=True,stdout=subprocess.DEVNULL)
  stop=read(P('/etc/baarcha-cube/worker-stop.json'));guard=P('/etc/baarcha-cube/storage-guard.json')
  need(sha(guard)=='9df51e0cf0af1735388506192ba7758214a683c81da2536224f998ffa6bf3bd8' and stop['admission']['storage_guard']==read(guard),'installed guard differs')
  with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=2)) as db:
   validate_app(db,d)
  tp=SRC/'ops/cube/production-worker/templates-2026-09-24.json';relay=SRC/'ops/cube/management-transport/compose.override.yml'
  need(sha(tp)=='bef0756e930600cf6e9f51b4066ef3f3e90de3de07d2a429f1da5c909377917a' and sha(relay)=='d09101cc13a39ddb4b550d12c8b0e342d18bd60868896c92b1d81eb98fc7cb4a','reviewed template/relay source changed')
  active=read(STATE/'active-images.json');runtime=STATE/'runtime-compose.json';existing=read(runtime) if runtime.exists() else {}
  env=os.environ.copy();env.update(CUBE_MANAGEMENT_RELAY_IMAGE=RELAY,CUBE_MANAGEMENT_GID='982')
  base=['docker','compose','-p','src','--env-file',str(SRC/'.env'),'-f',str(SRC/'docker-compose.yml')]
  resolved=json.loads(subprocess.check_output(base+['-f',str(relay),'config','--format','json'],env=env))
  out,new_active,values=render(existing,active,d,stop,json.loads(tp.read_text()),resolved['services'])
  inventory=json.loads(subprocess.check_output(['ip','-j','address','show','scope','global'],text=True));denies=set(values['SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS'].split(','));dns={}
  for interface in inventory:
   for address in interface.get('addr_info',[]):
    if address.get('family')=='inet':denies.add(str(ipaddress.IPv4Address(address['local']))+'/32')
  for domain in ['baarcha.tn','www.baarcha.tn','hh1.dovisual.com','bp.tn']:
   dns[domain]={}
   for family,command in [('ipv4','ahostsv4'),('ipv6','ahostsv6')]:
    r=subprocess.run(['getent',command,domain],text=True,capture_output=True,timeout=4);need(r.returncode in (0,2),'DNS inventory command failed')
    answers=sorted({str(ipaddress.ip_address(line.split()[0])) for line in r.stdout.splitlines() if line.split()});dns[domain][family]=answers
    if family=='ipv4':denies.update(a+'/32' for a in answers)
  values['SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS']=','.join(sorted(denies))
  for candidate in [out,new_active]:candidate['services']['sandboxd']['environment'].update(values)
  args.out.mkdir(mode=0o700);write(args.out/'runtime-compose.json',out);write(args.out/'active-images.json',new_active)
  write(args.out/'protected-address-observation.json',{'interfaces':inventory,'dns':dns,'protected_ipv4_cidrs':sorted(denies),'ipv6_policy':'native IPv6 denied; reverse broker IPv4 only'})
  merged=json.loads(subprocess.check_output(base+['-f',str(args.out/'runtime-compose.json'),'-f',str(args.out/'active-images.json'),'config','--format','json'],env=env))
  need(merged['services']['sandboxd']['image']==IMAGE and all(str(merged['services']['sandboxd']['environment'].get(k))==v for k,v in values.items()),'merged precedence changed canary configuration')
  write(args.out/'merged-compose.private.json',merged)
  report={'version':1,'installed':False,'scope':'one-owned-synthetic-app','app_id':d['app_id'],'controller_before':CP,'controller_image':IMAGE,'relay_image':RELAY,'worker_boot_id':BOOT,'decision_sha256':sha(args.decision),'global_rollout':False,'no_public_model_route_claim':True,'files':{n:sha(args.out/n) for n in ['runtime-compose.json','active-images.json','merged-compose.private.json']},'current_active_images_sha256':sha(STATE/'active-images.json'),'current_runtime_compose_sha256':sha(runtime) if runtime.exists() else None,'source_sha256':sha(P(__file__))}
  report.update(runtime_revision=subprocess.check_output(['git','-C',str(SRC),'rev-parse','HEAD'],text=True).strip(),platform_revision=subprocess.check_output(['git','-C','/opt/baarcha/app/landing','rev-parse','HEAD'],text=True).strip(),base_compose_sha256=sha(SRC/'docker-compose.yml'),source_env_sha256=sha(SRC/'.env'),controller_env_sha256=hashlib.sha256(json.dumps(live,sort_keys=True,separators=(',',':')).encode()).hexdigest(),observer_mount=out['services']['sandboxd']['volumes'][-1],protected_inventory_sha256=sha(args.out/'protected-address-observation.json'))
  write(args.out/'review-manifest.json',report)
  print(json.dumps({'out':str(args.out),'review_manifest_sha256':sha(args.out/'review-manifest.json'),'installed':False}))
if __name__=='__main__':main()
