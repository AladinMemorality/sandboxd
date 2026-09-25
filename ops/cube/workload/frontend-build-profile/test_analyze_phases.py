import copy
from pathlib import Path
import sys
import unittest
import analyze_phases as a
sys.path.insert(0, str(Path(__file__).resolve().parent.parent / 'coding-profile'))
from test_analyze import rows


def report():
    names = ['copy_started', 'copy_finished', 'frontend_process_start', 'frontend_modules_and_api_ready',
             'warm_api_finished', 'build_started', 'build_finished', 'hmr_started', 'hmr_finished',
             'finished', 'owned_children_stopped']
    return {'version': 2, 'successful': True, 'run': '0123456789abcdef',
            'phases': [{'name': name, 'wall_time_ns': (103 + i * 2) * 10**9,
                        'monotonic_ns': (3 + i * 2) * 10**9} for i, name in enumerate(names)]}


IDENTITY = {'outer': {'pid': 42, 'start_ticks': 10, 'clock_ticks_per_second': 100}}


class PhaseTests(unittest.TestCase):
    def test_no_task_required_exact_markers_and_no_boundary_double_count(self):
        samples = rows()
        for row in samples:
            row['tasks'] = []
        result = a.analyze(samples, report(), IDENTITY)
        self.assertTrue(result['workload_boundaries_covered'])
        phase = next(p for p in result['phases'] if p['name'] == 'build')
        self.assertEqual(phase['phase_start_wall_time_ns'], 113 * 10**9)
        self.assertEqual(phase['last_sample_unix_ns'], 114 * 10**9)
        self.assertEqual(phase['elapsed_monotonic_seconds'], 2)
        self.assertEqual(phase['guest_container']['samples'], 0)
        self.assertIsNone(phase['guest_container']['cpu'])

    def test_short_phase_has_no_invented_resource_delta(self):
        data = report()
        data['phases'][8]['wall_time_ns'] = data['phases'][7]['wall_time_ns'] + 100000
        data['phases'][8]['monotonic_ns'] = data['phases'][7]['monotonic_ns'] + 100000
        result = a.analyze(rows(), data, IDENTITY)
        hmr = next(p for p in result['phases'] if p['name'] == 'hmr')
        self.assertFalse(hmr['available'])
        self.assertEqual(hmr['samples'], 1)

    def test_rejects_duplicate_missing_clock_regression_and_wrong_identity(self):
        for mutate in (lambda r: r['phases'].append(copy.deepcopy(r['phases'][0])),
                       lambda r: r['phases'].pop(),
                       lambda r: r['phases'][1].update(monotonic_ns=0),
                       lambda r: r['phases'][1].update(wall_time_ns=1.2),
                       lambda r: r.update(successful=False)):
            data = report()
            mutate(data)
            with self.assertRaises(ValueError): a.analyze(rows(), data, IDENTITY)
        with self.assertRaises(ValueError):
            a.analyze(rows(), report(), {'outer': {**IDENTITY['outer'], 'start_ticks': 11}})

    def test_partial_observation_is_explicit(self):
        result = a.analyze(rows()[10:20], report(), IDENTITY)
        self.assertFalse(result['workload_boundaries_covered'])
        copy_phase = next(p for p in result['phases'] if p['name'] == 'template_copy')
        self.assertFalse(copy_phase['available'])


if __name__ == '__main__': unittest.main()
