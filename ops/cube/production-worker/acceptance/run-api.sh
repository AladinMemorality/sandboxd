#!/bin/sh
# Explicit coordinator invocation only. Staging this file does not run a guest.
set -eu
umask 077
[ "$(id -u)" = 0 ] || { echo 'root-owned operator stage required' >&2; exit 2; }
[ "$(hostname)" = baarcha-cube-worker-01 ] || { echo 'fresh worker required' >&2; exit 2; }
[ "$#" = 2 ] || { echo 'usage: run-api.sh postgres|reload UNIQUE_RUN_ID' >&2; exit 2; }
kind=$1
run_id=$2
case "$run_id" in ''|*[!a-z0-9-]*) echo 'invalid run ID' >&2; exit 2;; esac
[ "${#run_id}" -le 64 ] || exit 2
stage=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd -P)
[ "$(cat "$stage/disposable-api-stage")" = 'DISPOSABLE_CUBE_API_ACCEPTANCE_ONLY' ] || exit 2
[ "$(stat -c '%u:%a' "$stage")" = '0:700' ] || { echo 'private root stage required' >&2; exit 2; }
cd "$stage"
sha256sum --check binary.sha256 > /dev/null
exec 9>/run/lock/cube-operator-acceptance.lock
flock -n 9 || { echo 'another operator fixture owns the worker' >&2; exit 2; }
case "$kind" in
  postgres) template=tpl-ce1ee426e686460bbc8c3bfc; test_name=TestOperatorCubePostgresLifecycle; report_name=postgres-lifecycle-report.json; cleanup_key=all_vms_deleted;;
  reload) template=tpl-98b45d63cfcc48c5b6ba9104; test_name=TestOperatorCubeViteReload; report_name=reload-report.json; cleanup_key=vm_deleted;;
  *) echo 'unsupported fixture' >&2; exit 2;;
esac
run="$stage/runs/$kind-$run_id"
mkdir -p "$stage/runs" /data/acceptance-temp
[ "$(stat -c '%u:%a' /data/acceptance-temp)" = '0:700' ] || { echo 'private temp parent required' >&2; exit 2; }
mkdir "$run" # Refuse to overwrite any previous run, including failed runs.
python3 "$stage/api_preflight.py" "$stage" "$kind" "$run_id"
mkdir "$run/tmp" "/data/acceptance-temp/$kind-$run_id"
export TMPDIR="/data/acceptance-temp/$kind-$run_id"
cp "$stage/reload-regression.mjs" "$run/reload-regression.mjs"
: > "$run/disposable-reload"
unset CUBE_POSTGRES_FUNCTIONAL CUBE_RELOAD_FUNCTIONAL
if [ "$kind" = postgres ]; then
  export CUBE_POSTGRES_FUNCTIONAL=1 CUBE_POSTGRES_TEMPLATE="$template" CUBE_POSTGRES_STAGE="$run"
else
  export CUBE_RELOAD_FUNCTIONAL=1 CUBE_RELOAD_TEMPLATE="$template" CUBE_RELOAD_STAGE="$run"
fi
# Keep source-layout-relative ../../migrations and regular API fixtures intact.
cd "$stage/source/control-plane/internal/api"
set +e
timeout --signal=INT --kill-after=30s 13m "$stage/api-acceptance.test" -test.v -test.timeout=12m30s -test.run "^${test_name}$" > "$run/output.txt" 2>&1
result=$?
set -e
python3 - "$run" "$report_name" "$cleanup_key" "$result" <<'PY'
import json, pathlib, sys
run, name, key, status = pathlib.Path(sys.argv[1]), *sys.argv[2:]
try:
    report = json.loads((run/name).read_text())
    cleaned = report.get(key) is True
except (OSError, ValueError):
    cleaned = False
out = {'exit_code': int(status), 'cleanup_verified': cleaned, 'live_fixture_pass': int(status) == 0 and cleaned,
       'owned_identity_journal': 'owned-guests.ndjson', 'production_accepted': False}
with (run/'runner-result.json').open('x') as f:
    json.dump(out, f, indent=2)
print(json.dumps(out))
if not out['live_fixture_pass']: sys.exit(1)
PY
# Preserve logs and any failure's temporary store for diagnosis. Successful Go
# t.TempDir cleanup already removes its private SQLite files; remove empty parent.
rmdir "$TMPDIR" 2>/dev/null || true
