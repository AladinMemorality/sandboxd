#!/bin/sh
set -eu
: "${GO:=go}"
: "${SNAPSHOT:?isolated complete control-plane snapshot required}"
repo=$(CDPATH= cd -- "$(dirname "$0")/../../../../.." && pwd)
test -f "$SNAPSHOT/go.mod"
test "$SNAPSHOT" != "$repo/control-plane"
cmd="$SNAPSHOT/cmd/operator-clean-worker"
mkdir "$cmd"
cp "$repo/ops/cube/production-worker/acceptance/durable-worker/main.go" "$repo/ops/cube/production-worker/acceptance/durable-worker/main_test.go" "$cmd/"
"$GO" run "$repo/ops/cube/recovery-journal/acceptance/extract_helpers.go" -source "$repo/ops/cube/production-worker/acceptance/crash/main.go" -out "$cmd/crash_helpers.go" -names validMachineIdentity,must,nonce,durable,save,privateJSON,worker,bootID,record,detach,attach,remote,ownership,request,ready,validateEvidence,probe,buildProbeArchive,importPrivateProbe,install,homeManifest,export,cleanup,prepare,holdWorkerLease,recordFailure
# Only specialize the handoff/checkpoint labels; all create/export/marker logic remains reviewed.
python3 - "$cmd/crash_helpers.go" <<'PYCODE'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
s = p.read_text()
for before, after in {
    '"DISPOSABLE_CUBE_CRASH_HANDOFF"': 'expectedHandoffPurpose(c)',
    '"fresh crash handoff required"': '"fresh clean handoff required"',
    '"WAITING_FOR_OPERATOR_POWER_LOSS"': '"LATEST_MARKERS_READY_BEFORE_PAUSE"',
    '"CHECKPOINT_READY: owned synthetic latest app/home/SQL markers and older independent exports are durable; no power operation performed"': '"LATEST_MARKERS_READY: proceeding to clean pause/resume; do not perform any power action yet"',
}.items():
    if s.count(before) != 1:
        raise SystemExit("reviewed helper changed; specialization must be reviewed")
    s = s.replace(before, after)
p.write_text(s)
PYCODE
"$GO" fmt "$cmd"/*.go
printf 'Prepared %s; no guest calls or power operations.\n' "$cmd"
