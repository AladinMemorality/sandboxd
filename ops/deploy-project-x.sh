#!/usr/bin/env bash
# Exact-revision controller release. Preserves the active runtime; never migrates.
# Install outside the checkout as /opt/sandboxd/deploy.sh after reviewing it.
set -Eeuo pipefail
umask 077
sha=${1:?usage: deploy-project-x.sh <full-40-character-commit-sha>}
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo 'A full commit SHA is required' >&2; exit 2; }
src=${PROJECT_X_SRC_DIR:-/opt/sandboxd/src}
state=${PROJECT_X_DEPLOY_STATE:-/opt/sandboxd/deploy-state}
mkdir -p "$state"
chmod 700 "$state"
exec 9>"$state/deploy.lock"
flock -n 9 || { echo 'Another runtime deployment holds the lock' >&2; exit 1; }
run="$state/releases/$(date -u +%Y%m%dT%H%M%S)-${sha:0:12}-$$"
mkdir -p "$run"
active="$state/active-images.json"
runtime_compose="$state/runtime-compose.json"
base_compat=sandboxd-base:0.3.0
base_tag="sandboxd-base:release-$sha"
control_tag="sandboxd-control-plane:release-$sha"
activation_started=0
controller_attempted=0
check_container=''
project=''
worker_stop=${PROJECT_X_WORKER_STOP_CONFIG:-/etc/baarcha-cube/worker-stop.json}

compose() {
  local args=(-p "$project" --env-file "$src/.env" -f "$src/docker-compose.yml")
  [[ ! -f "$runtime_compose" ]] || args+=(-f "$runtime_compose")
  [[ ! -f "$active" ]] || args+=(-f "$active")
  docker compose "${args[@]}" "$@"
}
override() {
  python3 - "$1" "$active" "$runtime_mode" <<'PY'
import json,os,sys
image,path,mode=sys.argv[1:]
# Keep the complete operator override. In particular, a release must never
# silently disable Cube, discard trusted mappings, or remove transport mounts.
data=json.load(open(path)) if os.path.isfile(path) else {'services':{'sandboxd':{}}}
if mode=='docker':
 data['services']['sandboxd'].setdefault('environment',{}).update({
  'SANDBOXD_CUBE_ENABLED':'false','SANDBOXD_CUBE_REVERSE_EGRESS':'false','SANDBOXD_IMAGE':'sandboxd-base:0.3.0'})
data['services']['sandboxd']['image']=image
with open(path+'.new','w') as f: json.dump(data,f)
os.replace(path+'.new',path)
PY
}
isolation() { SANDBOXD_SRC_DIR="$src" "$src/host/sandbox-isolation.sh"; }
worker_pin() {
  local action=$1 expected_image=$2 current
  current=$(compose ps -q sandboxd)
  python3 - "$action" "$worker_stop" "$run" "$db" "$current" "$expected_image" "$runtime_mode" "$project" <<'PY'
import contextlib,hashlib,json,os,re,sqlite3,stat,subprocess,sys,tempfile
from pathlib import Path
action,name,directory,database,controller,image,mode,project=sys.argv[1:]
p=Path(name);root=Path(directory);expected=root/'worker-stop.expected.json'
def need(value):
 if not value:raise RuntimeError('Worker coordinator identity refresh refused; preserve configuration for operator review')
def private(path):
 need(path.is_absolute() and path.resolve(strict=True)==path)
 s=path.stat();uid=0 if name=='/etc/baarcha-cube/worker-stop.json' else os.geteuid()
 need(stat.S_ISREG(s.st_mode) and s.st_uid==uid and s.st_nlink==1 and stat.S_IMODE(s.st_mode)==0o600 and s.st_size<=131072)
 need(path.parent.stat().st_uid==uid and path.parent.stat().st_mode&0o022==0)
 return path.read_bytes()
def decode(raw):
 def unique(pairs):
  result={}
  for k,v in pairs:need(k not in result);result[k]=v
  return result
 return json.loads(raw,object_pairs_hook=unique)
def atomic(path,raw):
 fd,temp=tempfile.mkstemp(prefix='.'+path.name+'.release-',dir=path.parent)
 try:
  with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
  os.replace(temp,path)
  fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
  try:os.fsync(fd)
  finally:os.close(fd)
 finally:
  if os.path.exists(temp):os.unlink(temp)
if action=='prepare' and not p.exists() and not p.is_symlink():
 need(mode=='docker');(root/'worker-stop.absent').write_text('No worker coordinator was installed.\n');sys.exit(0)
if action in ('candidate','refresh') and (root/'worker-stop.absent').exists():
 need(not p.exists() and not p.is_symlink());sys.exit(0)
raw=private(p);c=decode(raw)
need(re.fullmatch('[0-9a-f]{64}',controller) and c.get('database')==database)
need(not Path(database+'.worker-stop.json').exists() and not Path(database+'.worker-stop.json').is_symlink())
need(c.get('worker_boot_id')==c.get('admission',{}).get('storage_guard',{}).get('expected_boot_id'))
need(c.get('data_uuid')==c['admission']['storage_guard'].get('inner_fs_uuid') and c.get('worker_machine_id')==c['admission']['storage_guard'].get('worker_machine_id'))
migrations=Path(c['migrations']);need(migrations.is_dir() and migrations.resolve()==migrations and migrations.stat().st_uid==p.stat().st_uid and migrations.stat().st_mode&0o022==0)
ids=sorted(int(q.name.split('_',1)[0]) for q in migrations.glob('*.sql') if re.fullmatch(r'[0-9]{4}_.+\.sql',q.name))
need(ids and 34 in ids and ids==list(range(1,max(ids)+1)))
with contextlib.closing(sqlite3.connect('file:'+database+'?mode=ro',uri=True,timeout=5)) as db:
 need([row[0] for row in db.execute('SELECT id FROM migration ORDER BY id')]==ids)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oBatchMode=yes','-oConnectTimeout=5','-oServerAliveInterval=3','-oServerAliveCountMax=1','-oStrictHostKeyChecking=yes','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1','cat /proc/sys/kernel/random/boot_id']
boot=subprocess.run(ssh,check=True,capture_output=True,text=True,timeout=12).stdout.strip()
need(re.fullmatch('[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}',boot) and boot==c['worker_boot_id'])
live=json.loads(subprocess.check_output(['docker','inspect',controller]))[0]
need(live['Id']==controller and live['Image']==image and live['State']['Running'] and live['Config']['Labels']['com.docker.compose.project']==project)
if action=='prepare':
 need(c.get('controller_id')==controller and not expected.exists())
 atomic(root/'worker-stop.before.json',raw);atomic(expected,raw)
elif action=='candidate':
 need(raw==private(expected) and c.get('controller_id')==controller)
 candidate=root/'source/control-plane/migrations'
 def manifest(directory):
  result={}
  for path in directory.glob('*.sql'):
   need(re.fullmatch(r'[0-9]{4}_.+\.sql',path.name) and path.is_file() and not path.is_symlink())
   result[path.name]=hashlib.sha256(path.read_bytes()).hexdigest()
  return result
 # Refuse a new schema BEFORE the candidate can migrate the live DB. A
 # binary rollback cannot undo such a migration or upgrade old coordinators.
 need(candidate.is_dir() and manifest(candidate)==manifest(migrations))
elif action=='refresh':
 need(raw==private(expected))
 c['controller_id']=controller;updated=(json.dumps(c,indent=2)+'\n').encode()
 # CAS recheck immediately before replacement under the existing deploy lock.
 need(private(p)==raw and not Path(database+'.worker-stop.json').exists())
 atomic(p,updated);atomic(expected,updated)
 receipt={'controller_id':controller,'worker_boot_id':boot,'config_sha256':hashlib.sha256(updated).hexdigest(),'only_controller_id_changed':True,'pause_receipt_created':False}
 atomic(root/('worker-stop-pin-'+controller+'.json'),(json.dumps(receipt)+'\n').encode())
else:need(False)
PY
}
reconnect_management() {
  [[ "$runtime_mode" == cube ]] || return 0
  compose up -d --no-deps --no-build --pull never --force-recreate cube-management-api cube-management-proxy
  local controller api_relay proxy_relay
  controller=$(compose ps -q sandboxd)
  api_relay=$(compose ps -q cube-management-api)
  proxy_relay=$(compose ps -q cube-management-proxy)
  [[ -n "$controller" && -n "$api_relay" && -n "$proxy_relay" ]]
  python3 - "$controller" "$api_relay" "$proxy_relay" <<'PY'
import json,subprocess,sys,time
deadline=time.monotonic()+45
while True:
 controller,*relays=json.loads(subprocess.check_output(['docker','inspect',*sys.argv[1:]]))
 assert controller['State']['Running'], 'Controller stopped while connecting Cube relays'
 for relay in relays:
  assert relay['HostConfig']['NetworkMode']=='container:'+controller['Id'], 'Cube relay attached to an obsolete controller namespace'
  assert not relay['HostConfig'].get('PortBindings'), 'Cube relay publishes host ports'
 if all(r['State']['Running'] and r['State'].get('Health',{}).get('Status')=='healthy' for r in relays): break
 if time.monotonic()>=deadline: raise RuntimeError('Cube management relay health deadline exceeded')
 time.sleep(1)
PY
}
verify() {
  local expected=$1 cid
  cid=$(compose ps -q sandboxd)
  [[ -n "$cid" ]]
  docker inspect "$cid" >"$run/verify-container.json"
  python3 - "$run/verify-container.json" "$expected" "$run/config.json" "$cid" <<'PY'
import json,subprocess,sys,time,urllib.request,urllib.error
container=json.load(open(sys.argv[1]))[0]
assert container['Image']==sys.argv[2], 'Running controller image differs from selected immutable image'
assert container['State']['Running'], 'Controller is not running'
env=dict(x.split('=',1) for x in container['Config']['Env'] if '=' in x)
cfg=json.load(open(sys.argv[3]))['services']['sandboxd']['environment']
cube_env=lambda values:{k:str(v) for k,v in values.items() if k.startswith('SANDBOXD_CUBE_') and str(v) not in ('','false')}
assert cube_env(env)==cube_env(cfg), 'Running Cube configuration differs from preserved deployment configuration'
assert str(env.get('SANDBOXD_API_AUTH_DISABLED','false')).lower()=='false', 'API authentication disabled'
tokens=cfg.get('SANDBOXD_API_TOKENS','')
token=next((p.split('=',1)[1].strip() for p in tokens.split(',') if '=' in p and p.split('=',1)[1].strip()),None)
assert token, 'Authenticated readiness requires an existing API token'
ports=json.load(open(sys.argv[3]))['services']['sandboxd'].get('ports',[])
port=next(p for p in ports if int(p['target'])==9000)
assert port.get('host_ip') in ('127.0.0.1','::1'), 'Readiness must use a loopback API binding'
host='[::1]' if port['host_ip']=='::1' else '127.0.0.1'
base=f"http://{host}:{port['published']}"
deadline=time.monotonic()+60
while True:
 try:
  for path in ('/healthz','/readyz','/v1/agents'):
   req=urllib.request.Request(base+path,headers={'Authorization':'Bearer '+token})
   with urllib.request.urlopen(req,timeout=3) as r: assert r.status==200
  try:
   urllib.request.urlopen(base+'/v1/agents',timeout=3)
  except urllib.error.HTTPError as e:
   assert e.code in (401,403), 'Unauthenticated API is not denied'
  else: raise AssertionError('Unauthenticated API unexpectedly succeeded')
  break
 except (OSError,urllib.error.URLError):
  if time.monotonic()>=deadline: raise RuntimeError('Runtime readiness deadline exceeded') from None
  time.sleep(1)
if env.get('SANDBOXD_CUBE_ENABLED')=='true':
 # Exercise the actual controller network namespace and key without exposing
 # the secret in argv, process environment, response bodies or release output.
 endpoint=env['SANDBOXD_CUBE_API_URL'].rstrip('/')+'/sandboxes?limit=1'
 for authenticated in (False,True):
  options='url = '+json.dumps(endpoint)+'\n'
  if authenticated: options+='header = '+json.dumps('X-API-Key: '+env['SANDBOXD_CUBE_API_KEY'])+'\n'
  result=subprocess.run(['docker','exec','-i',sys.argv[4],'curl','--silent','--show-error','--max-time','20','--output','/dev/null','--write-out','%{http_code}','--config','-'],input=options,text=True,capture_output=True,timeout=25)
  assert result.returncode==0, 'Cube API transport failed in controller namespace'
  assert result.stdout in (('200',) if authenticated else ('401','403')), 'Cube API authentication/readiness failed in controller namespace'
PY
}
rollback() {
  local code=$1
  trap - ERR INT TERM
  set +e
  echo "Runtime deployment failed; evidence retained in $run" >&2
  [[ -z "$check_container" ]] || docker rm -f "$check_container" >/dev/null 2>&1
  if [[ "$activation_started" == 1 ]]; then
    # Never replace data beneath a running new controller. Database files are
    # deliberately NOT restored: additive schema rollback retains live writes.
    if [[ "$controller_attempted" == 1 ]]; then
      compose stop -t 30 sandboxd || { echo 'Cannot stop candidate controller; manual recovery required' >&2; exit 1; }
    fi
    git -C "$src" checkout --detach "$old_sha" &&
      cp -p "$run/env.before" "$src/.env" &&
      docker image tag "$old_base" "$base_compat" &&
      override "$rollback_control" || { echo 'Cannot restore release configuration; controller remains stopped' >&2; exit 1; }
    if [[ "$controller_attempted" == 1 ]]; then
      compose up -d --no-deps --no-build --pull never sandboxd && reconnect_management && isolation && verify "$old_control" && worker_pin refresh "$old_control" || {
        compose stop -t 30 sandboxd
        echo 'Rollback readiness/isolation failed; controller left stopped for operator recovery' >&2
        exit 1
      }
    fi
    echo 'Previous runtime image/source restored; SQLite and tenant data were not rewound' >&2
  fi
  exit "$code"
}
trap 'rollback $?' ERR
trap 'rollback 130' INT
trap 'rollback 143' TERM

[[ -f "$src/.env" && -f "$src/docker-compose.yml" ]]
[[ -z "$(git -C "$src" status --porcelain --untracked-files=no)" ]] || { echo 'Tracked live changes require review before deployment' >&2; exit 1; }
old_sha=$(git -C "$src" rev-parse HEAD)
printf '%s\n' "$old_sha" >"$run/checkout.before"
cp -p "$src/.env" "$run/env.before"
chmod 600 "$run/env.before"
[[ ! -f "$active" ]] || cp -p "$active" "$run/active-images.before.json"
[[ ! -f "$runtime_compose" ]] || cp -p "$runtime_compose" "$run/runtime-compose.before.json"
# Resolve the existing project from the live controller, never the worktree name.
cid=$(docker compose --env-file "$src/.env" -f "$src/docker-compose.yml" ps -q sandboxd)
[[ -n "$cid" ]]
docker inspect "$cid" >"$run/container.before.json"
project=$(python3 - "$run/container.before.json" <<'PY'
import json,sys
c=json.load(open(sys.argv[1]))[0]
assert c['State']['Running'], 'Existing controller must be running'
env=dict(x.split('=',1) for x in c['Config']['Env'] if '=' in x)
print(c['Config']['Labels']['com.docker.compose.project'])
PY
)
compose config --format json >"$run/config.json"
runtime_mode=$(python3 - "$run/container.before.json" "$run/config.json" "$runtime_compose" <<'PY'
import json,os,re,sys
c=json.load(open(sys.argv[1]))[0]
live=dict(x.split('=',1) for x in c['Config']['Env'] if '=' in x)
config=json.load(open(sys.argv[2])); e=config['services']['sandboxd']['environment']
cube_env=lambda values:{k:str(v) for k,v in values.items() if k.startswith('SANDBOXD_CUBE_') and str(v) not in ('','false')}
assert cube_env(live)==cube_env(e), 'Release cannot change Cube configuration or perform a cutover'
enabled=str(e.get('SANDBOXD_CUBE_ENABLED','false'))
assert enabled in ('false','true'), 'Invalid Cube enabled value'
if enabled=='true':
 assert os.path.isfile(sys.argv[3]), 'Active Cube release requires durable runtime-compose.json'
 for key,value in {'SANDBOXD_CUBE_ROLLOUT':'global','SANDBOXD_CUBE_REVERSE_EGRESS':'true','SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED':'true','SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE':'proxy-http-v1'}.items():
  assert str(e.get(key,''))==value, 'Cube deployment must retain its accepted global configuration'
 for name in ('cube-management-api','cube-management-proxy'):
  service=config['services'].get(name,{})
  assert service.get('network_mode')=='service:sandboxd', 'Cube relay must share the controller network namespace'
  assert not service.get('ports'), 'Cube relay must not publish host ports'
  assert re.fullmatch(r'(?:[^\s]+@)?sha256:[0-9a-f]{64}',service.get('image','')), 'Cube relay requires an immutable image digest or local image ID'
 print('cube')
else:
 assert str(e.get('SANDBOXD_CUBE_REVERSE_EGRESS','false'))=='false', 'Reverse egress requires Cube enabled'
 print('docker')
PY
)
db=$(python3 - "$run/config.json" <<'PY'
import json,os,sqlite3,sys
e=json.load(open(sys.argv[1]))['services']['sandboxd']['environment']
assert str(e.get('SANDBOXD_API_AUTH_DISABLED','false')).lower()=='false', 'Authentication must remain enabled'
assert e.get('SANDBOXD_IMAGE','sandboxd-base:0.3.0')=='sandboxd-base:0.3.0', 'Nonstandard base image requires reviewed deployment'
db=e.get('SANDBOXD_DB') or os.path.join(e.get('SANDBOXD_DATA_DIR','/var/lib/sandboxd'),'state','sandboxd.db')
assert os.path.isabs(db) and os.path.isfile(db), 'Existing SQLite database missing'
with sqlite3.connect('file:'+db+'?mode=ro',uri=True) as handle:
 tables={row[0] for row in handle.execute("SELECT name FROM sqlite_master WHERE type='table'")}
 for table,column in [('runtime_binding','provider'),('app_runtime','provider'),('sandbox','runtime_provider')]:
  if table not in tables: continue
  columns={row[1] for row in handle.execute(f'PRAGMA table_info({table})')}
  if column in columns:
   if str(e.get('SANDBOXD_CUBE_ENABLED','false'))!='true':
    assert handle.execute(f"SELECT count(*) FROM {table} WHERE {column}='cube'").fetchone()[0]==0, 'Cannot deploy a disabled runtime over existing Cube ownership'
print(db)
PY
)
old_control=$(docker inspect -f '{{.Image}}' "$cid")
old_base=$(docker image inspect -f '{{.Id}}' "$base_compat")
worker_pin prepare "$old_control"
rollback_control="sandboxd-control-plane:rollback-$(basename "$run")"
docker image tag "$old_control" "$rollback_control"
docker image tag "$old_base" "sandboxd-base:rollback-$(basename "$run")"
printf '%s\n%s\n' "$old_control" "$old_base" >"$run/images.before"
if [[ -d "$src/traefik/dynamic" ]]; then cp -a "$src/traefik/dynamic" "$run/traefik-dynamic.before"; fi

git -C "$src" fetch --no-tags origin "$sha"
[[ "$(git -C "$src" rev-parse "$sha^{commit}")" == "$sha" ]]
# Public checked-in source must keep normal Git 0644/0755 modes: Docker COPY
# preserves them, and sandbox users must read root-owned files in the image.
# Scope this umask only to Git; the enclosing release directory stays 0700 and
# environment, database and inspection artifacts retain the private 077 umask.
(umask 022; git -C "$src" worktree add --detach "$run/source" "$sha")
worker_pin candidate "$old_control"
# A retry may reuse a completed immutable artifact, but cannot overwrite it or
# accept an unrelated pre-existing tag. Only this reviewed source SHA is valid.
build_image() {
  local tag=$1
  shift
  if docker image inspect "$tag" >/dev/null 2>&1; then
    [[ "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$tag")" == "$sha" ]] || return 1
  else
    docker build --pull=false --label "org.opencontainers.image.revision=$sha" -t "$tag" "$@"
  fi
}
build_image "$base_tag" -f "$run/source/image/Dockerfile" "$run/source"
build_image "$control_tag" --build-arg "VERSION=$sha" --build-arg "GIT_COMMIT=$sha" -f "$run/source/control-plane/Dockerfile" "$run/source/control-plane"
new_base=$(docker image inspect -f '{{.Id}}' "$base_tag")
new_control=$(docker image inspect -f '{{.Id}}' "$control_tag")
# Copy only these checked-in public fixtures to explicit readable individual
# bind mounts; never relax permissions on release directories or secret files.
mkdir -m 700 "$run/check-fixtures"
for fixture in scripts/vite-reload-regression.mjs image/services/postgres/paths.test.mjs image/services/postgres/worker.test.mjs; do
  git -C "$run/source" ls-files --error-unmatch -- "$fixture" >/dev/null
  install -m 644 "$run/source/$fixture" "$run/check-fixtures/${fixture##*/}"
done
# Acceptance executes the exact built artifacts, with no network or host data
# mounts. A timeout removes only the uniquely named disposable check container.
acceptance() {
  local name="sandboxd-deploy-check-$(basename "$run")-$1" seconds=$2
  shift 2
  check_container=$name
  if ! timeout --foreground --kill-after=30 "$seconds" docker run --rm --name "$name" --init --network none --cpus 1 --memory 1g "$@"; then
    docker rm -f "$name" >/dev/null 2>&1 || true
    check_container=''
    return 1
  fi
  check_container=''
}
acceptance version 30 --entrypoint /usr/local/bin/sandboxd "$control_tag" version >"$run/version.txt"
[[ "$(cat "$run/version.txt")" == "sandboxd $sha (${sha:0:12})" ]]
acceptance opt-in 30 --entrypoint node "$base_tag" -e '
 const fs=require("fs"),os=require("os"),path=require("path");
 if(fs.existsSync(path.join(os.homedir(),".baarcha-postgres")))throw Error("PostgreSQL data initialized without opt-in");
 for(const pid of fs.readdirSync("/proc").filter(x=>/^\d+$/.test(x))){
  try{if(fs.readFileSync(`/proc/${pid}/comm`,"utf8").trim()==="postgres")throw Error("PostgreSQL running without opt-in");}
  catch(e){if(e.code!=="ENOENT"&&e.code!=="ESRCH")throw e;}
 }'
acceptance vite 180 --entrypoint bash \
  -v "$run/check-fixtures/vite-reload-regression.mjs:/opt/reload-regression.mjs:ro" \
  -e RELOAD_USE_INSTALLED_PATCH=1 -e RELOAD_EXIT_AFTER_REPORT=1 "$base_tag" -lc '
 set -eu
 mkdir -p /tmp/reload-app
 cp -a /opt/templates/react-pro/. /tmp/reload-app/
 cd /tmp/reload-app
 cp /opt/reload-regression.mjs reload-regression.mjs
 echo DISPOSABLE_COLD_RELOAD_ONLY > README.reload-fixture.txt
 node reload-regression.mjs
 cat reload-results.json' >"$run/vite-acceptance.txt"
acceptance postgres 180 --entrypoint node \
  -v "$run/check-fixtures/paths.test.mjs:/opt/services/postgres/paths.test.mjs:ro" \
  -v "$run/check-fixtures/worker.test.mjs:/opt/services/postgres/worker.test.mjs:ro" \
  "$base_tag" --test /opt/services/postgres/paths.test.mjs /opt/services/postgres/worker.test.mjs >"$run/postgres-acceptance.txt"
# SQLite online backup includes committed WAL frames while the old controller
# remains live; never cp an open database or restore this snapshot automatically.
python3 - "$db" "$run/sandboxd.backup.sqlite" <<'PY'
import os,sqlite3,sys,time
source,target=sys.argv[1:]
assert not os.path.exists(target)
deadline=time.monotonic()+60
def progress(*_):
 if time.monotonic()>deadline: raise RuntimeError('SQLite backup deadline exceeded')
with sqlite3.connect('file:'+source+'?mode=ro',uri=True,timeout=5) as src, sqlite3.connect(target) as dst:
 src.backup(dst,pages=256,progress=progress,sleep=.1)
 assert dst.execute('PRAGMA quick_check').fetchone()[0]=='ok', 'Backup integrity failed'
os.chmod(target,0o600)
PY
# No clean/force checkout: untracked owner routing files remain byte-for-byte.
activation_started=1
git -C "$src" checkout --detach "$sha"
python3 - "$src/.env" "$sha" <<'PY'
from pathlib import Path
import os,sys
p=Path(sys.argv[1]); sha=sys.argv[2]
lines=[x for x in p.read_text().splitlines() if not x.startswith(('SANDBOXD_VERSION=','SANDBOXD_GIT_COMMIT='))]
p.with_name('.env.deploy-new').write_text('\n'.join(lines+[f'SANDBOXD_VERSION={sha}',f'SANDBOXD_GIT_COMMIT={sha}'])+'\n')
os.replace(p.with_name('.env.deploy-new'),p)
PY
docker image tag "$new_base" "$base_compat"
override "$control_tag"
controller_attempted=1
compose up -d --no-deps --no-build --pull never sandboxd
reconnect_management
isolation
verify "$new_control"
worker_pin refresh "$new_control"
if [[ -f "$run/traefik-dynamic.before/myhometroc.yml" ]]; then
  cmp "$run/traefik-dynamic.before/myhometroc.yml" "$src/traefik/dynamic/myhometroc.yml"
fi
printf '%s\n' "$sha" >"$run/succeeded"
echo "Runtime release $sha verified; preserved runtime: $runtime_mode; evidence: $run"
