"""Exact transitional Motion configuration; keeps the current controller image.

This is not the final Cube-only controller profile. It permits the already
accepted Motion guest while the two remaining Docker game projects migrate.
"""
import copy
import json

APP = '01M3CKN983PFRGMD711PCEPDFD'
SANDBOX = '01M3CKN99ZF90BEEA4DS66YAQV'
TEMPLATE = 'tpl-c0c9813b42db46898f7ddd9f'
SOCKET = '/run/baarcha-motion-studio'
MOUNT = {'type': 'bind', 'source': SOCKET, 'target': SOCKET, 'read_only': True, 'bind': {'create_host_path': False}}
IMAGE = 'sha256:f343341e0d3cf2c926ecd0a7ba8e293a5bca5f8dab6f9281ebcb1ff58044361b'


def need(ok, why):
    if not ok: raise RuntimeError(why)


def encoded(value): return json.dumps(value, sort_keys=True, separators=(',', ':'))


def admission(value):
    need(value.get('max_active') == 4 and value.get('cpu_count') == 2 and value.get('memory_mb') == 2048
         and value.get('writable_disk_mb') == 10240 and value.get('storage_guard'), 'Reviewed admission contract required')
    need(TEMPLATE not in value['templates'], 'Motion admission already changed; review actual state')
    result = copy.deepcopy(value)
    result['templates'][TEMPLATE] = {'cpu_count': 2, 'memory_mb': 2048}
    return result


def environment(value):
    need(not value.get('SANDBOXD_CUBE_MOTION_STUDIO_APP_ID'), 'Unexpected existing Motion selection')
    result = copy.deepcopy(value)
    result['SANDBOXD_CUBE_MOTION_STUDIO_APP_ID'] = APP
    result['SANDBOXD_CUBE_ADMISSION'] = encoded(admission(json.loads(value['SANDBOXD_CUBE_ADMISSION'])))
    return result


def prepare(runtime, active, stop):
    result = [copy.deepcopy(v) for v in (runtime, active, stop)]
    need(runtime['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION'] ==
         active['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION'], 'Compose admission layers differ')
    before = json.loads(runtime['services']['sandboxd']['environment']['SANDBOXD_CUBE_ADMISSION'])
    need(before == stop['admission'], 'Worker stop and controller admission differ')
    for layer in result[:2]:
        service = layer['services']['sandboxd']
        service['environment'] = environment(service['environment'])
    selected = result[1]['services']['sandboxd']
    need(not selected.get('volumes'), 'Review unexpected active-layer mounts')
    selected['volumes'] = [copy.deepcopy(MOUNT)]
    selected['image'] = IMAGE
    result[2]['admission'] = admission(before)
    return result


def validate_rendered(before, after):
    expected = copy.deepcopy(before)
    service = expected['services']['sandboxd']
    service['environment'] = environment(service['environment'])
    service['image'] = IMAGE
    mounts = service.setdefault('volumes', [])
    need(all(v.get('target') != SOCKET for v in mounts), 'Existing socket mount requires review')
    # Compose omits false create_host_path values when rendering JSON.
    new_mount = copy.deepcopy(MOUNT); new_mount['bind'] = {}
    mounts.append(new_mount)
    def normalize(value):
        value = copy.deepcopy(value)
        for selected in value['services'].values():
            if 'volumes' in selected:
                for mount in selected['volumes']:
                    if mount.get('bind', {}).get('create_host_path') is False:
                        del mount['bind']['create_host_path']
                    if not mount.get('bind'): mount.pop('bind', None)
                selected['volumes'].sort(key=lambda m: m['target'])
        return value
    need(normalize(after) == normalize(expected), 'Rendered configuration changes more than the exact Motion adapter')


def verify_mounts(old, actual):
    def normalize(mounts):
        return sorted((v['Type'], v['Source'], v['Destination'], v['RW']) for v in mounts)
    expected = list(old) + [{'Type': 'bind', 'Source': SOCKET, 'Destination': SOCKET, 'RW': False}]
    need(normalize(actual) == normalize(expected), 'Controller mount scope changed')
