#!/usr/bin/env python3
"""Offline resource analysis using exact guest phase markers, without a model task."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'coding-profile'))
import analyze as metrics

PAIRS = (
    ('template_copy', 'copy_started', 'copy_finished'),
    ('new_frontend_process_to_ready', 'frontend_process_start', 'frontend_modules_and_api_ready'),
    ('warm_api', 'frontend_modules_and_api_ready', 'warm_api_finished'),
    ('build', 'build_started', 'build_finished'),
    ('post_build_api_tail', 'build_finished', 'hmr_started'),
    ('hmr', 'hmr_started', 'hmr_finished'),
    ('child_cleanup', 'finished', 'owned_children_stopped'),
)


def analyze(rows, report, identity):
    metrics.validate(rows)
    need = metrics.m.need
    need(report.get('version') == 2 and report.get('successful') is True,
         'requires a successful version-2 guest report, not a task interval')
    markers = report['phases']
    names = [p['name'] for p in markers]
    need(len(set(names)) == len(names), 'duplicate phase marker')
    for p in markers:
        need(type(p['wall_time_ns']) is int and type(p['monotonic_ns']) is int,
             'phase timestamps must be original integer nanoseconds')
    for a, b in zip(markers, markers[1:]):
        need(b['wall_time_ns'] > a['wall_time_ns'] and b['monotonic_ns'] > a['monotonic_ns'],
             'phase clock regressed')
    by_name = {p['name']: p for p in markers}
    required = {name for _, start, end in PAIRS for name in (start, end)}
    need(required <= set(names), 'missing phase marker')
    outer = identity['outer']
    need(rows[0]['outer']['process']['pid'] == outer['pid'] and
         rows[0]['outer']['process']['start_ticks'] == outer['start_ticks'],
         'sampler identity mismatch')
    ticks = outer['clock_ticks_per_second']
    need(type(ticks) is int and ticks > 0, 'invalid tick frequency')
    first, last = by_name['copy_started'], by_name['owned_children_stopped']
    phases = []
    if rows[0]['at_unix_ns'] < first['wall_time_ns']:
        phases.append(metrics.phase(rows, 'before_workload_includes_supervisor_restart',
                                    rows[0]['at_unix_ns'], first['wall_time_ns'] - 1, ticks))
    for name, start, end in PAIRS:
        a, b = by_name[start], by_name[end]
        # Half-open intervals ensure a boundary sample cannot count in two phases.
        item = metrics.phase(rows, name, a['wall_time_ns'], b['wall_time_ns'] - 1, ticks)
        item['phase_start_wall_time_ns'] = a['wall_time_ns']
        item['phase_end_wall_time_ns'] = b['wall_time_ns']
        item['elapsed_monotonic_seconds'] = (b['monotonic_ns'] - a['monotonic_ns']) / 1e9
        item['wall_minus_monotonic_seconds'] = ((b['wall_time_ns'] - a['wall_time_ns']) -
                                                (b['monotonic_ns'] - a['monotonic_ns'])) / 1e9
        phases.append(item)
    # This overlapping envelope reports overall peaks; never sum it with its phases.
    envelope = metrics.phase(rows, 'whole_workload_overlaps_phases', first['wall_time_ns'], last['wall_time_ns'], ticks)
    if last['wall_time_ns'] < rows[-1]['at_unix_ns']:
        phases.append(metrics.phase(rows, 'after_workload_includes_restore_and_other_activity',
                                    last['wall_time_ns'] + 1, rows[-1]['at_unix_ns'], ticks))
    return {
        'schema': 1, 'run': report['run'], 'model_task_executed': False,
        'phase_source': 'original guest-report.json integer wall_time_ns; not JS-reserialized journal',
        'sampler_first_unix_ns': rows[0]['at_unix_ns'], 'sampler_last_unix_ns': rows[-1]['at_unix_ns'],
        'workload_boundaries_covered': rows[0]['at_unix_ns'] <= first['wall_time_ns'] and rows[-1]['at_unix_ns'] >= last['wall_time_ns'],
        'phases': phases, 'whole_workload': envelope,
        'limitations': [
            'Nested guest VM and whole-worker scopes overlap and must not be summed.',
            'Only in-window samples are used; no interpolation or task-time inference.',
            'Short phases with fewer than two samples have no CPU/counter delta; absent guest samples are not zero.',
            'Sampler receipt timestamps include transport delay; guest phase and host wall clocks are not independently synchronized by this analysis.',
            'Guest container metrics target five-second cadence; sampled peaks can miss shorter spikes.',
            'Guest cache is unavailable; VM cgroup file includes cache and shmem.',
            'Before/after intervals include manifest reloads and unrelated subsequent activity, not pure idle baselines.',
            'The 64-request API probe overlaps build then continues afterward; its aggregate p95 is not a build-only p95.',
            'Child max RSS is a cumulative Linux RUSAGE_CHILDREN high-water value, not total guest memory.',
            'Prepared dependencies and retained page cache were used; no cold-VM, browser-pixel, production-speed, or AI-success claim.',
        ],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--samples', type=Path, required=True)
    parser.add_argument('--identity', type=Path, required=True)
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--sampler-complete', action='store_true', help='Only after independently checking sampler terminal success')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    rows, digest = metrics.load_rows(args.samples)
    report = json.loads(metrics.m.text(args.report, 131072))
    identity = json.loads(metrics.m.text(args.identity, 8192))
    result = analyze(rows, report, identity)
    result['sampler_complete_verified_by_operator'] = args.sampler_complete
    result['input_sha256'] = {'samples': digest, 'identity': hashlib.sha256(args.identity.read_bytes()).hexdigest(),
                              'guest_report': hashlib.sha256(args.report.read_bytes()).hexdigest()}
    os.umask(0o077)
    args.output.mkdir(mode=0o700)
    (args.output / 'analysis.json').write_text(json.dumps(result, indent=2) + '\n')
    table = metrics.markdown(result).replace('No build interval was supplied.',
        'Build uses the explicit guest build_started/build_finished markers; short phases can lack resource samples.')
    (args.output / 'table.md').write_text(table)


if __name__ == '__main__':
    main()
