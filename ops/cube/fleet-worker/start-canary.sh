#!/usr/bin/env bash
# Starts only the new isolated operator VM. No customer enrollment or GPU access.
set -euo pipefail
[[ ( $# == 2 || $# == 3 ) && $1 =~ ^sha256:[a-f0-9]{64}$ ]] || {
  echo 'usage: start-canary.sh TOOL_IMAGE PREPARED_WORKER_DIRECTORY [pilot|capacity-50|capacity-100]' >&2
  exit 2
}
case ${3:-pilot} in
  pilot) worker_cpus=8; worker_memory_mb=32768; worker_limit=36g ;;
  capacity-50) worker_cpus=112; worker_memory_mb=163840; worker_limit=168g ;;
  capacity-100) worker_cpus=224; worker_memory_mb=229376; worker_limit=240g ;;
  *) echo 'unknown worker profile' >&2; exit 2 ;;
esac
tool_image=$1
worker_directory=$(realpath -- "$2")
[[ $worker_directory == /raid/baarcha-cube-worker-b200-01 ]]
[[ $(stat -c %u "$worker_directory") == "$(id -u)" ]]
[[ $(stat -c %a "$worker_directory") == 700 ]]
[[ $(docker image inspect "$tool_image" --format '{{.Id}}') == "$tool_image" ]]
for disk in root.qcow2 data.qcow2 seed.img; do
  [[ -f $worker_directory/$disk && ! -L $worker_directory/$disk ]]
done
[[ ! -e $worker_directory/container-id && ! -e $worker_directory/qmp.sock ]]
worker_kvm_group=$(stat -c %g /dev/kvm)
[[ $worker_kvm_group =~ ^[0-9]+$ ]]
umask 077
set -o noclobber
docker run --detach --name baarcha-cube-worker-b200-01 --restart=no \
  --runtime=runc --network=bridge --cpus="$worker_cpus" --memory="$worker_limit" --memory-swap="$worker_limit" \
  --pids-limit=512 --read-only --user "$(id -u):$(id -g)" \
  --group-add "$worker_kvm_group" --cap-drop=ALL --security-opt=no-new-privileges \
  --device=/dev/kvm --env NVIDIA_VISIBLE_DEVICES=void --stop-timeout=180 \
  --tmpfs /tmp:rw,mode=1777,size=64m --tmpfs /run:rw,mode=0755,size=16m \
  --mount "type=bind,src=$worker_directory,dst=/worker" \
  --publish 127.0.0.1:24222:24222/tcp "$tool_image" \
  -nodefaults -no-user-config -enable-kvm -machine q35,accel=kvm -cpu host \
  -name baarcha-cube-worker-b200-01 -m "$worker_memory_mb" -smp "$worker_cpus" -device virtio-rng-pci \
  -drive file=/worker/root.qcow2,if=none,id=rootdisk,format=qcow2 \
  -device virtio-blk-pci,drive=rootdisk,serial=baarcha-b200-root,bootindex=1 \
  -drive file=/worker/data.qcow2,if=none,id=datadisk,format=qcow2 \
  -device virtio-blk-pci,drive=datadisk,serial=baarcha-b200-data \
  -drive file=/worker/seed.img,if=virtio,format=raw,readonly=on \
  -nic user,net=10.0.3.0/24,dhcpstart=10.0.3.15,model=virtio-net-pci,hostfwd=tcp:0.0.0.0:24222-10.0.3.15:22 \
  -display none -serial file:/worker/serial.log \
  -qmp unix:/worker/qmp.sock,server=on,wait=off > "$worker_directory/container-id"
echo 'Started candidate worker VM. Verify console and host identity before SSH.'
