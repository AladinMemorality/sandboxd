#!/usr/bin/env bash
# Run only after an operator has verified no Cube installation/workloads and
# cleanly powered off this new VM. Preserves the stopped pilot container.
set -euo pipefail
umask 077
worker_directory=/raid/baarcha-cube-worker-b200-01
tool_image=sha256:7fa680c70d6e663af59f8ed63514a0eb5a5ecd9f16ebc3553906a3e1eeb2f9f5
worker_name=baarcha-cube-worker-b200-01
[[ $(stat -c %u "$worker_directory") == "$(id -u)" ]]
[[ $(stat -c %a "$worker_directory") == 700 ]]
[[ $(docker inspect "$worker_name" --format '{{.Id}}') == "$(cat "$worker_directory/container-id")" ]]
[[ $(docker inspect "$worker_name" --format '{{.State.Status}} {{.State.ExitCode}}') == 'exited 0' ]]
[[ $(docker inspect "$worker_name" --format '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}') == '8000000000 38654705664' ]]
[[ $(docker inspect "$worker_name" --format '{{.Config.Image}}') == "$tool_image" ]]
[[ ! -e "$worker_directory/pilot-before-capacity-50" ]]
[[ $(df --output=avail -B1 "$worker_directory" | tail -1) -gt 2199023255552 ]]
docker run --rm --runtime=runc --network=none --cpus=1 --memory=512m \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --user "$(id -u):$(id -g)" --entrypoint qemu-img \
  --mount "type=bind,src=$worker_directory,dst=/worker,readonly" "$tool_image" \
  info --output=json /worker/data.qcow2 | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d["format"]=="qcow2" and d["virtual-size"]==256*1024**3
assert not d.get("backing-filename")
'
mkdir -m 700 "$worker_directory/pilot-before-capacity-50"
docker inspect "$worker_name" > "$worker_directory/pilot-before-capacity-50/container.json"
docker run --rm --runtime=runc --network=none --cpus=1 --memory=512m \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --user "$(id -u):$(id -g)" --entrypoint qemu-img \
  --mount "type=bind,src=$worker_directory,dst=/worker" "$tool_image" \
  resize /worker/data.qcow2 1T
docker rename "$worker_name" "${worker_name}-pilot-stopped"
mv "$worker_directory/container-id" "$worker_directory/pilot-before-capacity-50/"
if [[ -S "$worker_directory/qmp.sock" ]]; then
  mv "$worker_directory/qmp.sock" "$worker_directory/pilot-before-capacity-50/"
fi
mv "$worker_directory/serial.log" "$worker_directory/pilot-before-capacity-50/"
echo 'Offline disk expanded; pilot container and evidence preserved. Start capacity-50 and grow XFS inside the guest.'
