#!/bin/sh
# Builds source only in an existing isolated complete control-plane snapshot.
set -eu
: "${GO:=go}"
repo=$(CDPATH= cd -- "$(dirname "$0")/../../../.." && pwd)
: "${SNAPSHOT:?set SNAPSHOT to an isolated control-plane directory}"
test -f "$SNAPSHOT/go.mod"
test "$SNAPSHOT" != "$repo/control-plane"
cmd="$SNAPSHOT/cmd/operator-journal-acceptance"
mkdir "$cmd"
cp "$repo/ops/cube/recovery-journal/acceptance/main.go" "$repo/ops/cube/recovery-journal/acceptance/main_test.go" "$cmd/"
cp "$repo/ops/cube/production-worker/recovery/replacement/missing_evidence.go" "$repo/ops/cube/production-worker/recovery/replacement/missing_evidence_test.go" "$cmd/"
"$GO" run "$repo/ops/cube/recovery-journal/acceptance/extract_helpers.go" -source "$repo/ops/cube/production-worker/acceptance/crash/main.go" -out "$cmd/crash_helpers.go" -names must,nonce,durable,save,privateJSON,worker,bootID,record,detach,attach,request,ready,validateEvidence,probe,holdWorkerLease
"$GO" run "$repo/ops/cube/recovery-journal/acceptance/extract_helpers.go" -source "$repo/ops/cube/production-worker/recovery/replacement/replacement_main.go" -out "$cmd/import_helpers.go" -names hashBytes,hashFile,openArchive,zipMember,validateOldPID,deriveHome,validateArchiveMarker,quiesced,exactOldInventory
"$GO" fmt "$cmd"/*.go
printf 'Prepared %s; no guest calls were made.\n' "$cmd"
