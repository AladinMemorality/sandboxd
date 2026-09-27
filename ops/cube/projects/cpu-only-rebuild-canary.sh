#!/usr/bin/env bash
# Run only the disposable source-rebuild fixture, never customer workspaces.
set -euo pipefail
if [[ $# != 2 || ! $1 =~ ^sha256:[a-f0-9]{64}$ ]]; then
  echo 'usage: cpu-only-rebuild-canary.sh LOCAL_IMAGE_ID RUNTIMED_TEST_BINARY' >&2
  exit 2
fi
canary_image=$1
canary_binary=$(realpath -- "$2")
[[ -f $canary_binary && -x $canary_binary ]]
[[ $(docker image inspect "$canary_image" --format '{{.Id}}') == "$canary_image" ]]
docker run --rm --runtime=runc --network=none --cpus=1 --memory=1g \
  --pids-limit=128 --user=1000 --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --env NVIDIA_VISIBLE_DEVICES=void --env RUNTIMED_CUBE_GUEST=1 \
  --tmpfs /tmp:rw,mode=1777,size=64m \
  --tmpfs /home/sandbox/workspace:rw,uid=1000,gid=1000,mode=0755,size=256m \
  --mount "type=bind,src=$canary_binary,dst=/out/runtimed.test,readonly" \
  --entrypoint /bin/sh "$canary_image" -ec '
    test ! -e /dev/nvidia0 && test ! -e /dev/nvidiactl && test ! -e /dev/nvidia-uvm
    exec /out/runtimed.test -test.v -test.run "^TestProjectGuest"
  '
