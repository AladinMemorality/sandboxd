import pathlib,subprocess,os
r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007');rescue='/var/lib/cube-rescue/vps-sandbox-20261007'
ssh=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/rescue-operator-20260925/known_hosts','-i','/opt/baarcha-cube/rescue-operator-20260925/operator-key','-p','20223','root@127.0.0.1']
subprocess.run(ssh+['umask 077; mkdir -p '+rescue+'/source '+rescue+'/input'],check=True)
for n in ['rescue_export.py','capture.py','plan.py']:
 subprocess.run(ssh+['umask 077; cat > '+rescue+'/source/'+n],input=(r/n).read_bytes(),check=True)
script='''#!/bin/bash
set -euo pipefail
stage=/var/lib/cube-rescue/vps-sandbox-20261007
[[ $(hostname) == baarcha-cube-rescue && $(id -u) == 0 ]]
[[ -f "$stage/input/rescue-input.json" && ! -e "$stage/exit-status" ]]
for iface in /sys/class/net/*; do [[ ${iface##*/} == lo || ${iface##*/} == enp0s2 ]]; done
restore() { ip link set dev enp0s2 up; }
trap restore EXIT HUP INT TERM
ip link set dev enp0s2 down
set +e
timeout --signal=INT --kill-after=30s 900s python3 "$stage/source/rescue_export.py" --input "$stage/input" --work "$stage/export" > "$stage/export.log" 2>&1
status=$?
printf '%s\\n' "$status" > "$stage/exit-status"
exit "$status"
'''
subprocess.run(ssh+['umask 077; cat > '+rescue+'/run.sh'],input=script.encode(),check=True)
print('network-isolated rescue exporter staged')
