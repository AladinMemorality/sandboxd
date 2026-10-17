#!/usr/bin/env python3
"""Produce a reviewed Compose overlay; never modify live configuration.

Input: JSON object containing the three SANDBOXD_CUBE_* environment strings,
plus a release JSON with `replacements` keyed by react-pro/marketplace. Each
receipt requires previous_template, template_id, cpu_count, memory_mb,
writable_disk_mb and ready_nodes from actual Cube registration/canary checks.
Existing template entries remain usable by existing sandboxes and rollback.
"""
import copy
import json
from pathlib import Path
import re
import sys


def plan(environment, release):
    names = ['SANDBOXD_CUBE_TEMPLATES', 'SANDBOXD_CUBE_ADMISSION', 'SANDBOXD_CUBE_FLEET']
    values = {key: json.loads(environment[key]) for key in names}
    defaults, admission, fleet = (values[key] for key in names)
    replacements = release['replacements']
    if not replacements or set(replacements) - {'react-pro', 'marketplace'}:
        raise ValueError('Only reviewed React frontend defaults can be replaced')
    for preset, receipt in replacements.items():
        old, new = defaults[preset], receipt['template_id']
        if receipt['previous_template'] != old or old == new or not re.fullmatch(r'tpl-[a-z0-9]+', new):
            raise ValueError('Template baseline changed or invalid replacement')
        if receipt.get('canary_passed') is not True:
            raise ValueError('A fresh Cube canary must pass before cutover')
        spec = {'cpu_count': receipt['cpu_count'], 'memory_mb': receipt['memory_mb']}
        if spec != admission['templates'][old]:
            raise ValueError('Resource changes require separate admission review')
        for worker in fleet['workers']:
            if not worker.get('draining') and worker['admission']['node_id'] not in receipt['ready_nodes']:
                raise ValueError('Replacement is not READY on an active worker')
        for contract in [admission, *[w['admission'] for w in fleet['workers']]]:
            if new in contract['templates']:
                raise ValueError('Replacement already exists; reconcile before retry')
            previous = contract['templates'].get(old)
            if previous is None or previous != spec:
                raise ValueError('Worker lacks matching reviewed template resources')
            contract['templates'][new] = copy.deepcopy(previous)
            budget = contract.get('resource_budget')
            if budget:
                profile = budget['profiles'][old]
                if profile['writable_disk_mb'] != receipt['writable_disk_mb']:
                    raise ValueError('Writable disk differs from admitted size')
                budget['profiles'][new] = copy.deepcopy(profile)
        defaults[preset] = new
    return {'services': {'sandboxd': {'environment': {key: json.dumps(values[key], separators=(',', ':')) for key in names}}}}


def budget_contracts(environment, candidate, rows):
    """Extend the durable contract using the same verified overlay, with no resize.

    Match the deployed Go struct/map encoding exactly; reject unfamiliar schemas
    instead of silently changing a durable capacity contract.
    """
    def canonical(value):
        assert set(value) == {'budget', 'templates'}
        budget = value['budget']
        assert set(budget) == {'cpu_millis', 'memory_mb', 'runtime_slots', 'build_slots', 'profiles'}
        ordered = {key: budget[key] for key in ['cpu_millis', 'memory_mb', 'runtime_slots', 'build_slots']}
        ordered['profiles'] = {}
        for key, profile in sorted(budget['profiles'].items()):
            assert set(profile) == {'cpu_millis', 'writable_disk_mb', 'kind'}
            ordered['profiles'][key] = {field: profile[field] for field in ['cpu_millis', 'writable_disk_mb', 'kind']}
        templates = {}
        for key, resource in sorted(value['templates'].items()):
            assert set(resource) == {'cpu_count', 'memory_mb'}
            templates[key] = {field: resource[field] for field in ['cpu_count', 'memory_mb']}
        return json.dumps({'budget': ordered, 'templates': templates}, separators=(',', ':'))
    before = {worker['id']: worker['admission'] for worker in json.loads(environment['SANDBOXD_CUBE_FLEET'])['workers']}
    after = {worker['id']: worker['admission'] for worker in json.loads(candidate['SANDBOXD_CUBE_FLEET'])['workers']}
    assert set(rows) == {key for key, value in before.items() if value.get('resource_budget')}
    result = {}
    for worker, raw in rows.items():
        old = {'budget': before[worker]['resource_budget'], 'templates': before[worker]['templates']}
        assert canonical(old) == raw, 'Durable budget baseline or encoding changed'
        result[worker] = canonical({'budget': after[worker]['resource_budget'], 'templates': after[worker]['templates']})
    return result


if __name__ == '__main__':
    if len(sys.argv) != 4:
        raise SystemExit('usage: plan.py environment.json verified-release.json overlay.json')
    result = plan(json.loads(Path(sys.argv[1]).read_text()), json.loads(Path(sys.argv[2]).read_text()))
    with Path(sys.argv[3]).open('x') as out:
        json.dump(result, out, indent=2)
