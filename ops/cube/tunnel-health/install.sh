#!/bin/bash
set -euo pipefail
worker=${1:?worker required}
case "$worker" in vps|b200-01) ;; *) exit 1;; esac
source_dir=$(cd -- "$(dirname -- "$0")" && pwd)
# Refuse deployment unless the dedicated public route reaches this gateway.
python3 "$source_dir/health.py" --worker "$worker" --check
install -m 0755 "$source_dir/health.py" /opt/baarcha-preview/tunnel-health.py
cat > /etc/systemd/system/baarcha-preview-tunnel-health.service <<EOF
[Unit]
Description=Check public Baarcha preview tunnel and recover confirmed disconnects
After=network-online.target
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /opt/baarcha-preview/tunnel-health.py --worker $worker
StateDirectory=baarcha-preview-tunnel-health
StateDirectoryMode=0700
TimeoutStartSec=55
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
CapabilityBoundingSet=
EOF
cat > /etc/systemd/system/baarcha-preview-tunnel-health.timer <<'EOF'
[Unit]
Description=Check the public preview tunnel every 30 seconds
[Timer]
OnBootSec=60
OnUnitActiveSec=30
AccuracySec=1
RandomizedDelaySec=3
Unit=baarcha-preview-tunnel-health.service
[Install]
WantedBy=timers.target
EOF
systemd-analyze verify /etc/systemd/system/baarcha-preview-tunnel-health.{service,timer}
systemctl daemon-reload
systemctl enable --now baarcha-preview-tunnel-health.timer
systemctl start baarcha-preview-tunnel-health.service
