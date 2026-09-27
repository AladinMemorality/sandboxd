#!/usr/bin/env bash
# Fresh disks only. Does not install Cube, launch a VM or register a worker.
set -euo pipefail
[[ $# == 4 && $1 =~ ^sha256:[a-f0-9]{64}$ ]] || {
  echo 'usage: prepare-canary.sh TOOL_IMAGE CLOUD_IMAGE NEW_WORKER_DIRECTORY SEED_DIRECTORY' >&2
  exit 2
}
tool_image=$1
cloud_input=$(realpath -- "$2")
worker_directory=$(realpath -- "$3")
seed_directory=$(realpath -- "$4")
[[ $worker_directory == /raid/baarcha-cube-worker-b200-01 ]]
[[ $(stat -c %u "$worker_directory") == "$(id -u)" ]]
[[ $(stat -c %a "$worker_directory") == 700 ]]
[[ -f $cloud_input && -f $seed_directory/user-data && -f $seed_directory/meta-data ]]
for disk in root.qcow2 data.qcow2 seed.img; do
  [[ ! -e $worker_directory/$disk && ! -L $worker_directory/$disk ]]
done
[[ $(docker image inspect "$tool_image" --format '{{.Id}}') == "$tool_image" ]]
docker run --rm --runtime=runc --network=none --cpus=2 --memory=1g \
  --pids-limit=64 --read-only --user "$(id -u):$(id -g)" --cap-drop=ALL \
  --security-opt=no-new-privileges --tmpfs /tmp:rw,mode=1777,size=64m \
  --mount "type=bind,src=$cloud_input,dst=/input/cloud.qcow2,readonly" \
  --mount "type=bind,src=$seed_directory,dst=/seed,readonly" \
  --mount "type=bind,src=$worker_directory,dst=/worker" \
  --entrypoint /bin/sh "$tool_image" -ec '
    umask 077
    echo "612b2c0cc1bc413a6cb8c38fd611794caf0f2b436c50013d8b3794db12ad7354  /input/cloud.qcow2" | sha256sum -c -
    test ! -e /worker/root.qcow2 && test ! -e /worker/data.qcow2 && test ! -e /worker/seed.img
    qemu-img convert -f qcow2 -O qcow2 /input/cloud.qcow2 /worker/root.qcow2
    qemu-img resize /worker/root.qcow2 64G
    qemu-img create -f qcow2 /worker/data.qcow2 256G
    cloud-localds /worker/seed.img /seed/user-data /seed/meta-data
    qemu-img info --output=json /worker/root.qcow2 > /worker/root-info.json
    qemu-img info --output=json /worker/data.qcow2 > /worker/data-info.json
    cp /installed-packages.txt /worker/hypervisor-packages.txt
  '
