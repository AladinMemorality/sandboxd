import pathlib,subprocess,json,re
r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007')
ssh=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=5','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/rescue-operator-20260925/known_hosts','-i','/opt/baarcha-cube/rescue-operator-20260925/operator-key','-p','20223','root@127.0.0.1']
remote='/var/lib/cube-rescue/vps-sandbox-20261007'
# Diagnose only the already disposable rescue clone, inside the networkless VM.
code='#!/bin/bash\nset -eu\nstage=/var/lib/cube-rescue/vps-sandbox-20261007\ntrap \'ip link set dev enp0s2 up\' EXIT HUP INT TERM\nip link set dev enp0s2 down\nset +e\nblkid -p -o value -s TYPE "$stage/export/journal-replayed.ext4" > "$stage/diagnostic-type" 2>/dev/null\nprintf \'%s\\n\' "$?" > "$stage/diagnostic-blkid-status"\ntimeout 300 e2fsck -fn "$stage/export/journal-replayed.ext4" > "$stage/diagnostic-fsck.log" 2>&1\nprintf \'%s\\n\' "$?" > "$stage/diagnostic-fsck-status"\n'
subprocess.run(ssh+['umask 077; cat > '+remote+'/diagnose.sh'],input=code.encode(),check=True)
subprocess.run(ssh+['systemd-run --unit=vps-sandbox-rescue-diagnose-20261007 --property=RuntimeMaxSec=330 /bin/bash '+remote+'/diagnose.sh'],check=True)
