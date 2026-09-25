#!/usr/bin/env python3
"""Prepare a copied four-slot PG/reload fixture and package; never execute it."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil


def replace_once(source, before, after):
    if source.count(before) != 1:
        raise ValueError('reviewed source changed; inspect before preparing')
    return source.replace(before, after)


def bounded_fixture(source):
    source = replace_once(source, 'MaxActive: 12, CPUCount: 2, MemoryMB: 2048',
                          'MaxActive: 4, CPUCount: 2, MemoryMB: 2048')
    return replace_once(source, 'report := map[string]any{',
                        'report := map[string]any{"admission_max_active": 4, ')


def complete_inventory(source):
    return replace_once(source, "    return counts == ['0']",
                        "    nodes = re.findall(r'^\\s*NODES_SCANNED\\s+1/1\\s*$', value, re.MULTILINE)\n"
                        "    return counts == ['0'] and len(nodes) == 1")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--snapshot', required=True, type=Path)
    parser.add_argument('--package', required=True, type=Path)
    args = parser.parse_args()
    source = Path(__file__).resolve().parents[1]
    repo = source.parents[3]
    snapshot = args.snapshot.resolve(strict=True)
    if snapshot == repo / 'control-plane' or not (snapshot / 'go.mod').is_file():
        raise ValueError('separate complete control-plane snapshot required')
    # Validate both sources before any output or snapshot mutation.
    fixtures = {name: bounded_fixture((source / name).read_text()) for name in
                ('postgres_acceptance_test.go', 'reload_acceptance_test.go')}
    preflight = complete_inventory((source / 'api_preflight.py').read_text())
    targets = {name: snapshot / 'internal/api' / ('operator_four_' + name) for name in fixtures}
    if any(path.exists() for path in targets.values()):
        raise ValueError('fixture copy already exists')
    args.package.mkdir(mode=0o700)  # never overwrite an earlier result
    for name, text in fixtures.items():
        with targets[name].open('x') as stream:
            stream.write(text)
    for filename in ('run-api.sh',):
        shutil.copy2(source / filename, args.package / filename)
    (args.package / 'api_preflight.py').write_text(preflight)
    shutil.copy2(repo / 'scripts/vite-reload-regression.mjs', args.package / 'reload-regression.mjs')
    (args.package / 'disposable-api-stage').write_text('DISPOSABLE_CUBE_API_ACCEPTANCE_ONLY\n')
    manifest = {'purpose': 'PREPARED_FOUR_SLOT_API_FIXTURE', 'admission_max_active': 4,
                'guest_cpu': 2, 'guest_memory_mib': 2048, 'live_execution': False,
                'source_files': {name: hashlib.sha256((source / name).read_bytes()).hexdigest()
                                 for name in fixtures},
                'copied_fixture_files': {targets[name].name: hashlib.sha256(text.encode()).hexdigest()
                                         for name, text in fixtures.items()},
                'preflight_sha256': hashlib.sha256(preflight.encode()).hexdigest()}
    (args.package / 'preparation.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print('Prepared four-slot fixture copies; compile internal/api next. No guest operations.')


if __name__ == '__main__':
    main()
