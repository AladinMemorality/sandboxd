#!/usr/bin/env python3
"""Offline phase analysis of fixed-scope coding samples; never contacts a host."""
import argparse
import datetime as dt
import hashlib
import json
import os
import re
from pathlib import Path
import statistics
import metrics as m


def utc_ns(value):
    match = re.fullmatch(r'(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})', value)
    m.need(match is not None, 'timestamps require ISO8601 seconds and an explicit UTC offset')
    parsed = dt.datetime.fromisoformat(match[1] + match[3].replace('Z', '+00:00'))
    epoch = dt.datetime(1970, 1, 1, tzinfo=dt.timezone.utc)
    delta = parsed.astimezone(dt.timezone.utc) - epoch
    fraction = int((match[2] or '').ljust(9, '0'))
    return (delta.days * 86400 + delta.seconds) * 10**9 + fraction


def load_rows(path):
    rows = []
    digest = hashlib.sha256()
    with open(path, 'rb') as source:
        for _ in range(1202):
            line = source.readline(131074)
            if not line:
                break
            m.need(len(line) <= 131073 and line.endswith(b'\n'), 'oversized or partial sample')
            digest.update(line)
            rows.append(json.loads(line))
    m.need(2 <= len(rows) <= 1201, 'sample count outside reviewed bounds')
    validate(rows)
    return rows, digest.hexdigest()


def validate(rows):
    for previous, current in zip(rows, rows[1:]):
        m.need(current['at_unix_ns'] > previous['at_unix_ns'], 'wall clock regressed')
        m.need(current['outer_boottime_ns'] > previous['outer_boottime_ns'], 'outer clock regressed')
        m.need(current['worker']['worker_boottime_ns'] > previous['worker']['worker_boottime_ns'], 'worker clock regressed')
        m.need(current['worker']['index'] == previous['worker']['index'] + 1, 'sample sequence gap')
        for scope in ('outer', 'worker'):
            a, b = previous[scope]['process'], current[scope]['process']
            m.need((a['pid'], a['start_ticks'], a['cgroup']) == (b['pid'], b['start_ticks'], b['cgroup']), 'process generation changed')
        m.need(current['worker']['disk']['inode'] == previous['worker']['disk']['inode'], 'disk generation changed')


def distribution(values):
    return None if not values else {'first': values[0], 'last': values[-1], 'minimum': min(values), 'median': statistics.median(values), 'sampled_peak': max(values), 'samples': len(values)}


def delta_dict(values):
    if len(values) < 2 or any(v is None for v in values):
        return None
    keys = set(values[0])
    m.need(all(set(v) == keys for v in values), 'counter fields changed')
    m.need(all(b[k] >= a[k] for a, b in zip(values, values[1:]) for k in keys), 'counter regressed')
    return {k: values[-1][k] - values[0][k] for k in sorted(keys)}


def cpu(values, clocks, scale):
    if len(values) < 2:
        return None
    seconds = [(b - a) / 1e9 for a, b in zip(clocks, clocks[1:])]
    changes = [(b - a) / scale for a, b in zip(values, values[1:])]
    m.need(all(v > 0 for v in seconds) and all(v >= 0 for v in changes), 'CPU counter/clock regressed')
    span = sum(seconds)
    used = sum(changes)
    return {'cpu_seconds': used, 'sample_span_seconds': span, 'average_percent_one_core': 100 * used / span,
            'interval_peak_percent_one_core': max(100 * v / s for v, s in zip(changes, seconds)),
            'interval_seconds_median': statistics.median(seconds), 'interval_seconds_max': max(seconds), 'intervals': len(seconds)}


def pressure(raw):
    if raw is None:
        return None
    return {parts[0]: {k: float(v) if k.startswith('avg') else int(v) for k, v in (item.split('=') for item in parts[1:])}
            for parts in (line.split() for line in raw.splitlines())}


def pressure_summary(values):
    if not values or any(v is None for v in values):
        return None
    parsed = [pressure(v) for v in values]
    result = {}
    for mode in ('some', 'full'):
        if all(mode in p for p in parsed):
            total = delta_dict([{'total': p[mode]['total']} for p in parsed])
            result[mode] = {'stall_microseconds_delta': total['total'] if total else None,
                            'avg10_percent': distribution([p[mode]['avg10'] for p in parsed])}
    return result


def io_devices(raw):
    if raw is None:
        return None
    result = {}
    for line in raw.splitlines():
        fields = line.split()
        result[fields[0]] = {k: int(v) for k, v in (entry.split('=') for entry in fields[1:])}
    return result


def block_io(values):
    if any(v is None for v in values) or len(values) < 2:
        return None
    parsed = [io_devices(v) for v in values]
    devices = set.intersection(*(set(p) for p in parsed))
    return {'per_device_delta': {d: delta_dict([p[d] for p in parsed]) for d in sorted(devices)},
            'devices_changed': any(set(p) != set(parsed[0]) for p in parsed),
            'summed_across_devices': False}


def scope_summary(rows, scope, ticks):
    items = [r[scope] for r in rows]
    clocks = [r['outer_boottime_ns'] if scope == 'outer' else r['worker']['worker_boottime_ns'] for r in rows]
    processes = [v['process'] for v in items]
    groups = [v['cgroup'] for v in items]
    memory = [m.counters(v['memory.stat']) if v['memory.stat'] is not None else {} for v in groups]
    counters = lambda name: [m.counters(v[name]) if v[name] is not None else None for v in groups]
    return {
        'process_memory_bytes': {key: distribution([p['memory_bytes'][key] for p in processes if key in p['memory_bytes']]) for key in ('Rss', 'Pss', 'Pss_Anon', 'Pss_File', 'Pss_Shmem', 'Swap', 'SwapPss')},
        'cgroup_memory_bytes': distribution([int(v['memory.current']) for v in groups]),
        'cgroup_memory_components_bytes': {key: distribution([v[key] for v in memory if key in v]) for key in ('anon', 'file', 'shmem', 'kernel', 'file_dirty', 'file_writeback', 'slab_reclaimable')},
        'cgroup_swap_current_bytes': distribution([int(v['memory.swap.current']) for v in groups if v['memory.swap.current'] is not None]),
        'memory_events_delta': delta_dict(counters('memory.events')),
        'cpu_stat_delta': delta_dict(counters('cpu.stat')),
        'process_cpu': cpu([p['user_ticks'] + p['system_ticks'] for p in processes], clocks, ticks),
        'process_io_delta': delta_dict([p['io'] for p in processes]),
        'cgroup_block_io': block_io([v['io.stat'] for v in groups]),
        'cpu_pressure': pressure_summary([v['cpu.pressure'] for v in groups]),
        'memory_pressure': pressure_summary([v['memory.pressure'] for v in groups]),
    }


def phase(rows, name, start, end, ticks):
    chosen = [r for r in rows if start <= r['at_unix_ns'] <= end]
    result = {'name': name, 'requested_start_unix_ns': start, 'requested_end_unix_ns': end, 'samples': len(chosen)}
    if len(chosen) < 2:
        return {**result, 'available': False, 'reason': 'fewer than two in-window samples; no interpolation'}
    result.update(available=True, first_sample_unix_ns=chosen[0]['at_unix_ns'], last_sample_unix_ns=chosen[-1]['at_unix_ns'],
                  start_unobserved_seconds=(chosen[0]['at_unix_ns'] - start) / 1e9,
                  end_unobserved_seconds=(end - chosen[-1]['at_unix_ns']) / 1e9)
    result['guest_vm'] = scope_summary(chosen, 'worker', ticks)
    result['whole_worker'] = scope_summary(chosen, 'outer', ticks)
    guest = [r['worker'] for r in chosen if 'guest_stats' in r['worker']]
    result['guest_container'] = {
        'samples': len(guest), 'memory_usage_bytes': distribution([g['guest_stats']['memory.usage_in_bytes'] for g in guest]),
        'assigned_limit_bytes': distribution([g['guest_stats']['memory.limit_in_bytes'] for g in guest]),
        'cache_available': False,
        'cpu': cpu([g['guest_stats']['cpuacct.usage'] for g in guest], [g['worker_boottime_ns'] for g in guest], 1e9),
    }
    disks = [r['worker']['disk'] for r in chosen]
    result['disk'] = {'allocated_bytes': distribution([d['allocated_bytes'] for d in disks]),
                      'allocated_net_delta_bytes': disks[-1]['allocated_bytes'] - disks[0]['allocated_bytes'],
                      'logical_bytes': disks[0]['logical_bytes'], 'unique_physical_known': False}
    for scope, item, key in (('worker_kernel_memory_bytes', 'worker', 'worker_memory'), ('outer_kernel_memory_bytes', 'outer', 'host_memory')):
        result[scope] = {name: distribution([r[item][key][name] for r in chosen]) for name in ('MemAvailable', 'Cached', 'SReclaimable', 'Shmem', 'SwapFree')}
    result['observer_ms'] = {'outer': distribution([r['outer_collection_ms'] for r in chosen]), 'worker': distribution([r['worker']['worker_collection_ms'] for r in chosen])}
    return result


def analyze(rows, start, end, task_id, ticks, build_start=None, build_end=None):
    validate(rows)
    m.need(start < end and ticks > 0, 'invalid task interval/tick rate')
    m.need((build_start is None) == (build_end is None), 'both build boundaries required')
    if build_start is not None:
        m.need(start <= build_start < build_end <= end, 'build must be inside actual task interval')
    observed = [t for r in rows for t in r['tasks'] if t['task_id'] == task_id]
    m.need(bool(observed), 'task not present in scoped telemetry')
    phases = []
    if rows[0]['at_unix_ns'] < start:
        phases.append(phase(rows, 'idle_before_task', rows[0]['at_unix_ns'], start - 1, ticks))
    phases.append(phase(rows, 'actual_task', start, end, ticks))
    if build_start is not None:
        phases.append(phase(rows, 'build_subset_of_task', build_start, build_end, ticks))
    if end < rows[-1]['at_unix_ns']:
        phases.append(phase(rows, 'after_task', end + 1, rows[-1]['at_unix_ns'], ticks))
    return {'schema': 1, 'sandbox_id': m.SANDBOX, 'provider_id': m.PROVIDER, 'task_id': task_id,
            'task_states_observed': list(dict.fromkeys(t['status'] for t in observed)), 'phases': phases,
            'limitations': ['Nested scopes overlap and must not be summed.', 'Build is an explicitly timed subset of the task, not inferred or subtracted.',
                            'Only in-window samples and intervals are used; boundaries are not interpolated.',
                            'Timestamps are sample receipt times, with recorded collection/transport delay; guest metrics target 5 seconds.',
                            'Sampled peaks can miss shorter spikes; interval CPU peaks are averages over the reported interval.',
                            'Cgroup file includes cache/shmem; guest container cache is unavailable. Warm-cache state is retained.',
                            'Whole-worker counters include services, other activity and observer overhead.',
                            'Allocated backing-file blocks may be reflink-shared; block-device counters are not summed across stacked devices.']}


def markdown(result):
    lines = ['| Phase | Guest memory median / peak MiB | Guest VM PSS peak MiB | VM cgroup file peak MiB | Guest CPU seconds / average / interval peak | RW net MiB |',
             '| --- | ---: | ---: | ---: | ---: | ---: |']
    for p in result['phases']:
        if not p['available']:
            lines.append('| ' + p['name'] + ' | unavailable | — | — | — | — |')
            continue
        g = p['guest_container']; mem = g['memory_usage_bytes']; c = g['cpu']; file = p['guest_vm']['cgroup_memory_components_bytes']['file']
        memory = f"{mem['median']/2**20:.2f} / {mem['sampled_peak']/2**20:.2f}" if mem else 'unavailable'
        cpu_text = f"{c['cpu_seconds']:.3f}s / {c['average_percent_one_core']:.2f}% / {c['interval_peak_percent_one_core']:.2f}%" if c else 'unavailable'
        lines.append(f"| {p['name']} | {memory} | {p['guest_vm']['process_memory_bytes']['Pss']['sampled_peak']/2**20:.2f} | {file['sampled_peak']/2**20:.2f} | {cpu_text} | {p['disk']['allocated_net_delta_bytes']/2**20:.2f} |")
    build_note = 'The build interval overlaps the task.' if any(p['name'] == 'build_subset_of_task' for p in result['phases']) else 'No build interval was supplied.'
    return '\n'.join(lines) + '\n\nCPU percentages refer to one core. ' + build_note + ' Nested scopes must not be summed. See JSON for actual sample coverage, RSS/cgroup/cache, I/O, pressure, OOM/swap/throttle and collection costs.\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('samples', type=Path)
    parser.add_argument('--identity', required=True, type=Path)
    parser.add_argument('--task-id', required=True)
    parser.add_argument('--task-start', required=True)
    parser.add_argument('--task-end', required=True)
    parser.add_argument('--build-start')
    parser.add_argument('--build-end')
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    rows, digest = load_rows(args.samples)
    identity = json.loads(m.text(args.identity, 8192))
    m.need(rows[0]['outer']['process']['pid'] == identity['outer']['pid'] and rows[0]['outer']['process']['start_ticks'] == identity['outer']['start_ticks'], 'sampler identity mismatch')
    result = analyze(rows, utc_ns(args.task_start), utc_ns(args.task_end), args.task_id, identity['outer']['clock_ticks_per_second'],
                     utc_ns(args.build_start) if args.build_start else None, utc_ns(args.build_end) if args.build_end else None)
    result['input_sha256'] = {'samples': digest, 'identity': hashlib.sha256(args.identity.read_bytes()).hexdigest()}
    os.umask(0o077)
    args.output.mkdir(mode=0o700)
    (args.output / 'analysis.json').write_text(json.dumps(result, indent=2) + '\n')
    (args.output / 'table.md').write_text(markdown(result))


if __name__ == '__main__':
    main()
