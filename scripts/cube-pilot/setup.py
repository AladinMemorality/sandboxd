#!/usr/bin/env python3
"""Run ONLY in the disposable Cube benchmark VM, never the production host."""
import json, pathlib, secrets, subprocess
root=pathlib.Path('/root/cube-pilot')
assert root.is_dir() and pathlib.Path('/root/bench-ready').exists(), 'not the isolated test VM'
config_path=root/'test-secrets.json'
if not config_path.exists():
 config_path.write_text(json.dumps({k:secrets.token_hex(32) for k in ['cube_key','api_a','api_b','preview_secret']}))
 config_path.chmod(0o600)
config=json.loads(config_path.read_text())
dropin=pathlib.Path('/etc/systemd/system/cube-sandbox-cube-api.service.d')
dropin.mkdir(exist_ok=True)
(dropin/'pilot.conf').write_text('[Service]\nEnvironment=CUBE_API_KEY='+config['cube_key']+'\n')
(dropin/'pilot.conf').chmod(0o600)
subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','restart','cube-sandbox-cube-api'],check=True)
(root/'host-only-canary').write_text('must never be visible inside a guest')
print('isolated API authentication configured; secret values remain on the VM')
