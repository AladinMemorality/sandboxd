#!/usr/bin/env python3
"""Derive a private, current-disk rescue plan from pinned Cubelet metadata.

Pure metadata validation: never mounts, clones, boots or changes a sandbox.
Input metadata contains secrets; only the selected disk/layer identities escape.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re

PIN = '31d911e430fdf8a8879bd062b8e78066c1a8e89d'
ID = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$')

class Invalid(ValueError):
    pass

def require(condition, message):
    if not condition:
        raise Invalid(message)

def absolute(value, root='/data/cubelet'):
    require(isinstance(value, str) and not any(c in value for c in '\x00\n\r:,\\'), 'invalid path')
    p = PurePosixPath(value)
    require(p.is_absolute() and str(p) == value and '..' not in p.parts, 'noncanonical path')
    require(p != PurePosixPath(root) and p.is_relative_to(root), 'path outside approved data tree')
    return value

def relative(value):
    require(isinstance(value, str) and not any(c in value for c in '\x00\n\r:,\\'), 'invalid relative path')
    p = PurePosixPath(value)
    require(not p.is_absolute() and str(p) == value and '..' not in p.parts and value not in ('', '.'), 'noncanonical relative path')
    return value

def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()

def make_plan(box, storage, expected_id, snapshot_metadata=None):
    require(ID.fullmatch(expected_id) is not None, 'invalid expected sandbox ID')
    require(box.get('ID') == expected_id and box.get('sandbox_id') == expected_id, 'CubeBox identity mismatch')
    # cubecli storage ls --raw emits reduced protobuf SandboxStorageInfo,
    # not the internal StorageInfo JSON. Preserve both exact source schemas.
    original_storage=storage
    if 'sandboxID' in storage:
        require(isinstance(storage.get('volumes'),list),'invalid CLI storage volumes')
        normalized={}
        for v in storage['volumes']:
            require(v.get('name') and v['name'] not in normalized,'duplicate CLI storage volume')
            normalized[v['name']]={'Name':v['name'],'FilePath':v.get('file_path'),'VolumeName':v.get('volume_name'),'Kind':v.get('kind'),'Gen':v.get('gen',0)}
        storage={'SandboxID':storage['sandboxID'],'Namespace':storage.get('namespace'),'Volumes':normalized}
    require(storage.get('SandboxID') == expected_id, 'StorageInfo identity mismatch')
    require(storage.get('Namespace') == box.get('namespace') and bool(box.get('namespace')), 'namespace mismatch')
    require(not box.get('user_mark_deleted_time') and not box.get('DeletedTime'), 'deleted sandbox is not recoverable by this tool')
    legacy = box.get('Containers') or {}
    modern = box.get('ContainersMap') or {}
    require(isinstance(legacy, dict) and isinstance(modern, dict), 'invalid container map')
    modern = modern.get('ContainerMap') or {}
    require(isinstance(modern, dict), 'invalid canonical container map')
    require(not legacy or not modern or legacy == modern, 'conflicting container maps')
    containers = modern or legacy
    require(isinstance(containers, dict) and len(containers) == 1, 'only single-container recovery supported')
    cid = box.get('first_container_name')
    require(cid == expected_id and cid in containers, 'main container identity mismatch')
    container = containers[cid]
    require(container.get('ID') == cid and container.get('sandbox_id') == expected_id, 'container identity mismatch')
    config = container.get('config', {})
    mounts = config.get('volume_mounts', [])
    roots = [m for m in mounts if m.get('container_path') == '/']
    require(len(roots) == 1 and roots[0].get('name'), 'exactly one writable root required')
    require(all(m.get('container_path') == '/' or m.get('container_path') in ('/etc/hostname', '/etc/hosts', '/etc/resolv.conf') for m in mounts), 'external data mounts require separate recovery')
    require(not storage.get('hostDirBackendInfos') and not storage.get('pluginVolumeBackendInfos'), 'external storage not supported')
    root = storage.get('Volumes', {}).get(roots[0]['name'])
    require(isinstance(root, dict) and root.get('Name') == roots[0]['name'], 'root volume mismatch')
    require(root.get('Type') in (None,'ext4') and root.get('Kind') in ('volume','snapshot') and root.get('VolumeName'), 'current ext4 CoW root required, never a RAM fallback')
    require(isinstance(root.get('Gen'), int) and root['Gen'] >= 0, 'missing disk generation')
    disk = absolute(root.get('FilePath'))
    if root['Kind'] == 'snapshot':
        # Pinned XFS create_snapshot also names live writable clone generations.
        # Kind alone does not distinguish live RW from a template/pause package.
        live_name = 'sb-' + expected_id + '-rootfs-gen' + str(root['Gen'])
        require(root['VolumeName'] == live_name and PurePosixPath(disk).name == live_name, 'snapshot is not this sandbox current writable generation')
        require(PurePosixPath(disk).is_relative_to('/data/cubelet/storage/xfs/objects/volumes'), 'unsupported writable snapshot backend')
    ri = container.get('cube_rootfs_info', {})
    require(not ri.get('ero_image'), 'unsupported lower filesystem layout')
    require(all(m.get('container_dest') in ('/etc/hostname', '/etc/hosts', '/etc/resolv.conf') for m in ri.get('mounts', [])), 'unexpected external mount')
    resolved = []
    image = None
    layout = 'directory_layers'
    if ri.get('pmem_file'):
        require(not ri.get('overlay_info') and not box.get('virtiofs_config_map'), 'conflicting PMEM lower layout')
        image_config = config.get('image', {})
        image_id = image_config.get('image')
        require(isinstance(image_id, str) and ID.fullmatch(image_id) is not None, 'invalid PMEM image ID')
        require(image_config.get('storage_media') == 'ext4', 'PMEM image medium mismatch')
        references = box.get('ImageReferences', {})
        ref = references.get(image_id, {})
        require(set(references) == {image_id} and ref.get('ID') == image_id and ref.get('Medium') == 1, 'PMEM image reference mismatch')
        expected = '/usr/local/services/cubetoolbox/cubebox_os_image/' + image_id + '/' + image_id + '.ext4'
        require(ri['pmem_file'] == expected, 'PMEM image path mismatch')
        image = {'image_id': image_id, 'file': expected, 'filesystem': 'ext4'}
        layout = 'pmem_ext4'
    else:
        lowers = ri.get('overlay_info', {}).get('virtiofs_lower_dir', [])
        require(isinstance(lowers, list) and 0 < len(lowers) <= 64, 'missing or excessive image layers')
        aliases = {}
        for cfg in box.get('virtiofs_config_map', {}).values():
            backend = cfg.get('backendfs_config', {})
            require(backend.get('shared_dir') == '/data/cubelet/' and backend.get('read_only') is True, 'untrusted image share')
            for path in backend.get('allowed_dirs', []):
                path = absolute(path)
                alias = PurePosixPath(path).name
                require(alias not in aliases or aliases[alias] == path, 'ambiguous image share alias')
                aliases[alias] = path
        resolved = []
        for name in lowers:
            parts = PurePosixPath(relative(name)).parts
            require(len(parts) in (1, 2) and (len(parts) == 1 or parts[1] == 'fs'), 'unsupported image layer alias')
            require(parts[0] in aliases, 'image layer not present in saved share map')
            resolved.append(str(PurePosixPath(aliases[parts[0]]).joinpath(*parts[1:])))
        require(len(resolved) == len(set(resolved)), 'duplicate image layers')
    upper_id = cid
    upper_identity = {'kind':'direct_container','container_id':cid}
    metadata_hashes = {'cubebox':digest(box),'storage':digest(original_storage)}
    template = box.get('LocalRunTemplate') or {}
    snapshot = (template.get('snapshot') or {}).get('snapshot') or {}
    if snapshot:
        template_id = (template.get('distributionReference') or {}).get('templateID')
        require(isinstance(template_id,str) and ID.fullmatch(template_id) is not None, 'invalid saved snapshot template')
        annotations = box.get('Annotations') or {}
        require(snapshot.get('id') == template_id and snapshot.get('media') == 'cubebox' and annotations.get('cube.master.appsnapshot.template.id') == template_id and annotations.get('cube.master.launch.memory.snapshot.id') == template_id, 'saved snapshot template identity conflict')
        expected_path = '/data/cubelet/storage/xfs/snapshots/' + template_id + '/metadata'
        require(snapshot.get('path') == expected_path, 'unsupported template snapshot metadata path')
        require(isinstance(snapshot_metadata,dict), 'exact referenced template metadata required for restored upper identity')
        upper_id = snapshot_metadata.get('app_snapshot_container_id')
        require(upper_id == template_id + '_0', 'unsupported or conflicting original snapshot container identity')
        metadata_hashes['template_snapshot_metadata'] = digest(snapshot_metadata)
        upper_identity = {'kind':'template_snapshot','container_id':upper_id,'template_id':template_id,'metadata_path':expected_path+'/metadata.json','metadata_sha256':digest(snapshot_metadata)}
    elif snapshot_metadata is not None:
        raise Invalid('unreferenced template snapshot metadata')
    return {'format': 1, 'purpose': 'CUBE_CURRENT_DISK_RESCUE', 'upstream_commit': PIN,
            'sandbox_id': expected_id, 'container_id': cid, 'namespace': box['namespace'],
            'source_metadata_sha256': metadata_hashes,
            'current_disk': {k: root[k] for k in ('Name', 'FilePath', 'VolumeName', 'Kind', 'Gen')},
            'expected_filesystem': 'ext4 (must be verified inside rescue VM)', 'upper_subdir': 'disk/' + upper_id + '/upper', 'upper_identity': upper_identity, 'lower_dirs': resolved,
            'lower_layout': layout, 'lower_image': image, 'export_root': 'home/sandbox', 'scope': 'full home including PostgreSQL data and WAL; no external volumes'}

def private_json(path, value):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream, indent=2); stream.write('\n'); stream.flush(); os.fsync(stream.fileno())

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cubebox', required=True, type=Path)
    parser.add_argument('--storage', required=True, type=Path)
    parser.add_argument('--sandbox-id', required=True)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--snapshot-metadata', type=Path)
    args = parser.parse_args()
    try:
        plan = make_plan(json.loads(args.cubebox.read_text()), json.loads(args.storage.read_text()), args.sandbox_id, json.loads(args.snapshot_metadata.read_text()) if args.snapshot_metadata else None)
        private_json(args.output, plan)
    except Invalid as error:
        parser.exit(1, 'Recovery plan rejected: '+str(error)+'\n')
    except (OSError, ValueError, TypeError, KeyError):
        parser.exit(1, 'Recovery plan rejected; verify private metadata and supported layout.\n')

if __name__ == '__main__':
    main()
