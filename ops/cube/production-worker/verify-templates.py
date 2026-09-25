#!/usr/bin/env python3
"""Read-only checks of the registered fresh-worker template requests."""
import json
from pathlib import Path
import subprocess

base = Path('/root/cube-production')
if subprocess.check_output(['hostname'], text=True).strip() != 'baarcha-cube-worker-01':
    raise RuntimeError('fresh worker required')
results = json.loads((base / 'registered-templates.json').read_text())
for row in results:
    item = json.loads(subprocess.check_output(['cubemastercli', 'tpl', 'info',
        '--template-id', row['template_id'], '--include-request', '--json'], text=True))
    request = item['create_request']
    assert item['status'] == 'READY'
    assert len(request['containers']) == 1
    container = request['containers'][0]
    assert container['resources'] == {'cpu': '2000m', 'mem': '2048Mi'}
    assert request['cube_network_config'] == {'denyOut': ['0.0.0.0/0']}
    assert any(v.get('volume_source', {}).get('empty_dir', {}).get('size_limit') == '10Gi'
               for v in request['volumes'])
    probe = container['probe']['probe_handler']['http_get']
    assert probe['port'] == 49983 and probe['path'] == '/health'
    row['verified_stored_resources'] = container['resources']
    row['verified_stored_network'] = request['cube_network_config']
    row['verified_stored_writable_layer'] = '10Gi'
    row['template_container_privileged'] = container.get('security_context', {}).get('privileged', False)
    # This guest-VM template flag is not proof of app capabilities. The composed
    # cube-init must drop UID/capabilities; ordinary workload acceptance checks it.
(base / 'verified-templates.json').write_text(json.dumps(results, indent=2) + '\n')
print(json.dumps({'verified': len(results), 'expected': 8, 'all_eight': len(results) == 8}))
