#!/usr/bin/env bash
# Exact-revision Docker release. This does not enable or migrate Cube.
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
base_compat=sandboxd-base:0.3.0
base_tag="sandboxd-base:release-$sha"
control_tag="sandboxd-control-plane:release-$sha"
activation_started=0
controller_attempted=0
check_container=''
project=''

compose() {
  local args=(-p "$project" --env-file "$src/.env" -f "$src/docker-compose.yml")
  [[ ! -f "$active" ]] || args+=(-f "$active")
  docker compose "${args[@]}" "$@"
}
override() {
  python3 - "$1" "$active" <<'PY'
import json,os,sys
image,path=sys.argv[1:]
data={'services':{'sandboxd':{'image':image,'environment':{
 'SANDBOXD_CUBE_ENABLED':'false','SANDBOXD_CUBE_REVERSE_EGRESS':'false','SANDBOXD_IMAGE':'sandboxd-base:0.3.0'}}}}
with open(path+'.new','w') as f: json.dump(data,f)
os.replace(path+'.new',path)
PY
}
isolation() { SANDBOXD_SRC_DIR="$src" "$src/host/sandbox-isolation.sh"; }
verify() {
  local expected=$1 cid
  cid=$(compose ps -q sandboxd)
  [[ -n "$cid" ]]
  docker inspect "$cid" >"$run/verify-container.json"
  python3 - "$run/verify-container.json" "$expected" "$run/config.json" <<'PY'
import json,sys,time,urllib.request,urllib.error
container=json.load(open(sys.argv[1]))[0]
assert container['Image']==sys.argv[2], 'Running controller image differs from selected immutable image'
assert container['State']['Running'], 'Controller is not running'
env=dict(x.split('=',1) for x in container['Config']['Env'] if '=' in x)
assert env.get('SANDBOXD_CUBE_ENABLED','false').lower()=='false', 'Cube must remain disabled'
assert env.get('SANDBOXD_CUBE_REVERSE_EGRESS','false').lower()=='false', 'Cube reverse egress must remain disabled'
cfg=json.load(open(sys.argv[3]))['services']['sandboxd']['environment']
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
      compose up -d --no-deps --no-build --pull never sandboxd && isolation && verify "$old_control" || {
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
# Resolve the existing project from the live controller, never the worktree name.
cid=$(docker compose --env-file "$src/.env" -f "$src/docker-compose.yml" ps -q sandboxd)
[[ -n "$cid" ]]
docker inspect "$cid" >"$run/container.before.json"
project=$(python3 - "$run/container.before.json" <<'PY'
import json,sys
c=json.load(open(sys.argv[1]))[0]
assert c['State']['Running'], 'Existing controller must be running'
env=dict(x.split('=',1) for x in c['Config']['Env'] if '=' in x)
assert env.get('SANDBOXD_CUBE_ENABLED','false').lower()=='false', 'Refusing Cube-enabled deployment'
assert env.get('SANDBOXD_CUBE_REVERSE_EGRESS','false').lower()=='false', 'Refusing Cube reverse-egress deployment'
print(c['Config']['Labels']['com.docker.compose.project'])
PY
)
compose config --format json >"$run/config.json"
db=$(python3 - "$run/config.json" <<'PY'
import json,os,sqlite3,sys
e=json.load(open(sys.argv[1]))['services']['sandboxd']['environment']
assert str(e.get('SANDBOXD_CUBE_ENABLED','false')).lower()=='false', 'Cube enabled in compose configuration'
assert str(e.get('SANDBOXD_CUBE_REVERSE_EGRESS','false')).lower()=='false', 'Cube reverse egress enabled in compose configuration'
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
   assert handle.execute(f"SELECT count(*) FROM {table} WHERE {column}='cube'").fetchone()[0]==0, 'Existing Cube ownership requires a separately reviewed deployment'
print(db)
PY
)
old_control=$(docker inspect -f '{{.Image}}' "$cid")
old_base=$(docker image inspect -f '{{.Id}}' "$base_compat")
rollback_control="sandboxd-control-plane:rollback-$(basename "$run")"
docker image tag "$old_control" "$rollback_control"
docker image tag "$old_base" "sandboxd-base:rollback-$(basename "$run")"
printf '%s\n%s\n' "$old_control" "$old_base" >"$run/images.before"
if [[ -d "$src/traefik/dynamic" ]]; then cp -a "$src/traefik/dynamic" "$run/traefik-dynamic.before"; fi

git -C "$src" fetch --no-tags origin "$sha"
[[ "$(git -C "$src" rev-parse "$sha^{commit}")" == "$sha" ]]
git -C "$src" worktree add --detach "$run/source" "$sha"
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
  -v "$run/source/scripts/vite-reload-regression.mjs:/opt/reload-regression.mjs:ro" \
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
  -v "$run/source/image/services/postgres/paths.test.mjs:/opt/services/postgres/paths.test.mjs:ro" \
  -v "$run/source/image/services/postgres/worker.test.mjs:/opt/services/postgres/worker.test.mjs:ro" \
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
isolation
verify "$new_control"
if [[ -f "$run/traefik-dynamic.before/myhometroc.yml" ]]; then
  cmp "$run/traefik-dynamic.before/myhometroc.yml" "$src/traefik/dynamic/myhometroc.yml"
fi
printf '%s\n' "$sha" >"$run/succeeded"
echo "Runtime release $sha verified; Cube remains disabled; evidence: $run"
