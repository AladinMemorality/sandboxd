#!/usr/bin/env python3
"""Prepare the Cube controller's three existing Compose layers. Never deploys."""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re

SERVICE = 'sandboxd'  # Retained network/service identity; the executable is replaced.
DATA = '/var/lib/sandboxd'
DATA_EXPR = '${SANDBOXD_DATA_DIR:-/var/lib/sandboxd}'
SOCKET = '/run/baarcha-motion-studio'
OBSERVER = '/run/sandboxd-cube-storage'
BINARY = '/usr/local/bin/cube-controller'
MIGRATIONS = '/usr/local/share/cube-controller/migrations'
WRITABLE = (DATA + '/state', DATA + '/agent-auth', DATA + '/library', DATA + '/log')


def need(ok, message):
    if not ok:
        raise RuntimeError(message)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'))


def bind(source, target=None, readonly=True):
    return {'type': 'bind', 'source': source, 'target': target or source,
            'read_only': readonly, 'bind': {'create_host_path': False}}


def mount(value):
    if isinstance(value, dict):
        return copy.deepcopy(value)
    need(isinstance(value, str), 'Unsupported mount representation')
    # Do not split the colons in ${VAR:-default} Compose interpolation.
    fields, current, depth = [], '', 0
    for char in value:
        if char == '{':
            depth += 1
        elif char == '}':
            depth -= 1
        if char == ':' and depth == 0:
            fields.append(current)
            current = ''
        else:
            current += char
    fields.append(current)
    need(depth == 0 and len(fields) in (2, 3), 'Ambiguous mount')
    need(len(fields) == 2 or fields[2] in ('ro', 'rw'), 'Unreviewed mount options')
    source, target = fields[:2]
    need(source.startswith(('/', '${')) and target.startswith(('/', '${')), 'Named or anonymous mount requires review')
    return bind(source, target, len(fields) == 3 and fields[2] == 'ro')


def reviewed_image(image):
    need(re.fullmatch(r'(?:[a-z0-9][a-z0-9./:_-]*@)?sha256:[0-9a-f]{64}', image),
         'Immutable reviewed Cube controller image required')


def prepare_layers(base, runtime, active, image, motion_app):
    reviewed_image(image)
    need(re.fullmatch(r'[0-9A-HJKMNP-TV-Z]{26}', motion_app), 'Exact Motion app ULID required')
    result = [copy.deepcopy(v) for v in (base, runtime, active)]
    for layer in result:
        service = layer.get('services', {}).get(SERVICE)
        need(isinstance(service, dict), 'Existing controller service missing from Compose layer')
        service.pop('build', None)
        service.update(image=image, pull_policy='never', entrypoint=[BINARY], command=[])
        volumes = []
        for original in service.get('volumes', []):
            value = mount(original)
            source, target = value.get('source'), value.get('target')
            if source == '/var/run/docker.sock' or target == '/var/run/docker.sock':
                need(source == target and value['type'] == 'bind', 'Unexpected Docker socket alias')
                continue
            if target in (DATA, DATA_EXPR):
                need(source == target and value['type'] == 'bind', 'Unexpected controller data mapping')
                value = bind(source, target, True)
            volumes.append(value)
        if 'volumes' in service:
            service['volumes'] = volumes
    # Keep Compose service name, network aliases, published ports and management
    # relay namespace references stable. Replace the actual image/entrypoint.
    service = result[2]['services'][SERVICE]
    environment = service.setdefault('environment', {})
    need(isinstance(environment, dict), 'Normalized environment mapping required')
    environment.update(SANDBOXD_CUBE_ENABLED='true', SANDBOXD_CUBE_ROLLOUT='global', SANDBOXD_CUBE_APP_IDS='',
                       SANDBOXD_MIGRATIONS=MIGRATIONS, CUBE_CONTROLLER_ADDR='0.0.0.0:9000',
                       CUBE_RETAINED_HISTORY_ROOT=DATA + '/workspaces', SANDBOXD_CUBE_MOTION_STUDIO_APP_ID=motion_app)
    service['volumes'] = [bind(DATA), *[bind(path, readonly=False) for path in WRITABLE],
                          bind(OBSERVER), bind(SOCKET)]
    service['group_add'] = ['980']
    labels = service.setdefault('labels', {})
    need(isinstance(labels, dict), 'Normalized service labels required')
    labels['baarcha.controller'] = 'cube'
    return result


def validate_resolved(value, image, motion_app):
    reviewed_image(image)
    services = value['services']
    service = services[SERVICE]
    need(service['image'] == image and service.get('entrypoint') == [BINARY]
         and not service.get('command') and 'build' not in service and service.get('pull_policy') == 'never',
         'Rendered service can launch an unreviewed executable/image')
    need(not service.get('privileged') and not service.get('devices') and service.get('pid') != 'host'
         and service.get('network_mode') != 'host', 'Controller host privileges are not permitted')
    need(not service.get('cap_add'), 'Unreviewed controller capabilities')
    environment = service['environment']
    expected = dict(SANDBOXD_CUBE_ENABLED='true', SANDBOXD_CUBE_ROLLOUT='global', SANDBOXD_CUBE_APP_IDS='',
                    SANDBOXD_MIGRATIONS=MIGRATIONS, CUBE_CONTROLLER_ADDR='0.0.0.0:9000',
                    CUBE_RETAINED_HISTORY_ROOT=DATA + '/workspaces', SANDBOXD_CUBE_MOTION_STUDIO_APP_ID=motion_app)
    need(all(environment.get(k) == v for k, v in expected.items()), 'Rendered Cube controller configuration differs')
    need(environment.get('SANDBOXD_DATA_DIR') == DATA and environment.get('SANDBOXD_API_AUTH_DISABLED') == 'false',
         'Retained canonical data and authenticated API required')
    need(environment.get('SANDBOXD_CUBE_API_URL') == 'http://127.0.0.1:20300'
         and environment.get('SANDBOXD_CUBE_PROXY_URL') == 'http://127.0.0.1:20080', 'Management relay contract changed')
    need(environment.get('SANDBOXD_CUBE_REVERSE_EGRESS') == 'true'
         and environment.get('SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED') == 'true', 'Reviewed Cube network policy missing')
    expected_mounts = {DATA: True, OBSERVER: True, SOCKET: True, **{path: False for path in WRITABLE}}
    mounts = service.get('volumes', [])
    need(len(mounts) == len(expected_mounts), 'Unexpected or missing controller mount')
    for value in mounts:
        target = value['target']
        need(target in expected_mounts and value.get('type') == 'bind' and value.get('source') == target
             and bool(value.get('read_only', False)) == expected_mounts[target]
             # Compose's normalized JSON omits this false-valued field.
             and value.get('bind', {}).get('create_host_path', False) is False, 'Unsafe controller mount')
    need({v['target'] for v in mounts} == set(expected_mounts), 'Duplicate controller mount')
    need('980' in [str(v) for v in service.get('group_add', [])], 'Motion socket group missing')
    for name in ('cube-management-api', 'cube-management-proxy'):
        relay = services[name]
        need(relay.get('network_mode') == 'service:' + SERVICE and not relay.get('ports'), 'Management relay namespace changed')
    return {'version': 1, 'prepared_only': True, 'controller_binary': BINARY,
            'controller_image': image, 'global_cube_creation': True, 'docker_socket_mounted': False,
            'retained_history_read_only': True, 'legacy_service_alias_only': SERVICE,
            'production_deployed': False, 'customer_projects_migrated': 0}


def write(path, value):
    fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(canonical(value) + '\n')
        stream.flush()
        os.fsync(stream.fileno())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True)
    parser.add_argument('--runtime', required=True)
    parser.add_argument('--active', required=True)
    parser.add_argument('--image', required=True)
    parser.add_argument('--motion-app', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    import yaml  # Native operator environment, safe parsing; no custom tags.
    paths = [Path(v) for v in (args.base, args.runtime, args.active)]
    sources = [p.read_bytes() for p in paths]
    layers = prepare_layers(*(yaml.safe_load(v) for v in sources), args.image, args.motion_app)
    output = Path(args.output)
    need(output.is_absolute() and output.parent.resolve() == output.parent and not output.exists(), 'New canonical private output required')
    output.mkdir(mode=0o700)
    for name, value in zip(('base.json', 'runtime.json', 'active.json'), layers):
        write(output / name, value)
    write(output / 'prepared.json', {'version': 1, 'source_sha256': {str(p): hashlib.sha256(v).hexdigest() for p, v in zip(paths, sources)},
                                    'image': args.image, 'motion_app': args.motion_app, 'production_deployed': False,
                                    'requires': 'Render all actual Compose layers, validate resolved mounts/environment and reconcile a fully migrated fleet under the existing maintenance fence.'})
    print(canonical({'prepared_only': True, 'production_deployed': False, 'output': str(output)}))


if __name__ == '__main__':
    main()
