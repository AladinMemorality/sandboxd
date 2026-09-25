import copy
import json
from pathlib import Path
import tempfile
import subprocess
import sys
import unittest
import analyze as a


def rows():
    result = []
    for i in range(26):
        group = {'memory.current': str(300+i), 'memory.stat': f'anon 100\nfile {150+i}\nshmem 20\nkernel 30\nfile_dirty 0\nfile_writeback 0\nslab_reclaimable 0\n',
                 'memory.swap.current': str(i), 'memory.events': f'oom {int(i>=15)}\noom_kill 0\nhigh 0\n',
                 'cpu.stat': f'usage_usec {i*100000}\nnr_throttled {int(i>=12)}\nthrottled_usec {i*10}\n',
                 'cpu.pressure': f'some avg10=0.01 avg60=0.00 avg300=0.00 total={i*10}\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n',
                 'memory.pressure': 'some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n',
                 'io.stat': f'8:0 rbytes={i*10} wbytes={i*20}\n9:0 rbytes={i*10} wbytes={i*20}\n'}
        memory = dict.fromkeys(('MemAvailable','Cached','SReclaimable','Shmem','SwapFree'), 1000-i)
        process = {'pid':42,'start_ticks':10,'cgroup':'0::/example','user_ticks':i*10,'system_ticks':0,
                   'io': {'read_bytes':i*10,'write_bytes':i*20},'memory_bytes':{'Rss':200+i,'Pss':150+i,'Pss_Anon':100,'Pss_File':50+i,'Swap':i}}
        worker = {'index':i,'worker_boottime_ns':i*10**9,'worker_collection_ms':5,'process':process,'cgroup':group,
                  'worker_memory':memory,'disk':{'inode':1,'allocated_bytes':50+i,'logical_bytes':1000}}
        if i%5==0:
            worker['guest_stats']={'memory.usage_in_bytes':100+i,'memory.limit_in_bytes':2**31,'cpuacct.usage':{0:0,5:10**9,10:2*10**9,15:12*10**9,20:13*10**9,25:14*10**9}[i]}
        result.append({'at_unix_ns':(100+i)*10**9,'outer_boottime_ns':i*10**9,'worker':worker,
                       'outer':{'process':copy.deepcopy(process),'cgroup':copy.deepcopy(group),'host_memory':memory},
                       'outer_collection_ms':20,'tasks':[{'task_id':'owned','status':'running' if i<20 else 'succeeded'}]})
    return result


class AnalysisTest(unittest.TestCase):
    def test_explicit_phase_guest_native_cadence_and_no_double_count(self):
        report=a.analyze(rows(),106*10**9,120*10**9,'owned',100,110*10**9,115*10**9)
        self.assertEqual([p['name'] for p in report['phases']],['idle_before_task','actual_task_including_build','build_subset_of_task','after_task'])
        task=report['phases'][1];g=task['guest_container']
        self.assertEqual(g['samples'],3)
        self.assertEqual(g['memory_usage_bytes']['median'],115)
        self.assertEqual(g['cpu']['sample_span_seconds'],10)
        self.assertEqual(g['cpu']['cpu_seconds'],11)
        self.assertAlmostEqual(g['cpu']['average_percent_one_core'],110)
        self.assertEqual(g['cpu']['interval_peak_percent_one_core'],200)
        self.assertEqual(g['cpu']['interval_seconds_median'],5)
        self.assertEqual(task['disk']['allocated_net_delta_bytes'],14)
        self.assertEqual(task['guest_vm']['process_cpu']['average_percent_one_core'],10)
        self.assertEqual(task['guest_vm']['memory_events_delta']['oom'],1)
        self.assertEqual(task['guest_vm']['cpu_stat_delta']['nr_throttled'],1)
        self.assertEqual(task['guest_vm']['cpu_pressure']['some']['stall_microseconds_delta'],140)
        self.assertEqual(task['guest_vm']['cgroup_block_io']['per_device_delta']['8:0']['wbytes'],280)
        self.assertFalse(task['guest_vm']['cgroup_block_io']['summed_across_devices'])
        self.assertEqual(task['guest_vm']['cgroup_memory_components_bytes']['file']['sampled_peak'],170)
        self.assertIn('build_subset_of_task',a.markdown(report))

    def test_sparse_guest_samples_not_interpolated_or_assumed_zero(self):
        report=a.analyze(rows(),106*10**9,109*10**9,'owned',100)
        g=report['phases'][1]['guest_container']
        self.assertEqual(g['samples'],0);self.assertIsNone(g['cpu']);self.assertIsNone(g['memory_usage_bytes'])
        self.assertFalse(g['cache_available'])
        self.assertIn('unavailable',a.markdown(report))

    def test_phase_coverage_reports_missing_edges(self):
        report=a.analyze(rows(),106500000000,109500000000,'owned',100)
        task=report['phases'][1]
        self.assertEqual(task['start_unobserved_seconds'],.5)
        self.assertEqual(task['end_unobserved_seconds'],.5)

    def test_refuses_clock_generation_and_counter_regression(self):
        for mutate in (lambda r:r[2].update(at_unix_ns=r[1]['at_unix_ns']),
                       lambda r:r[2]['worker']['process'].update(start_ticks=99),
                       lambda r:r[2]['worker'].update(index=9),
                       lambda r:r[2]['worker']['disk'].update(inode=2),
                       lambda r:r[15]['worker']['guest_stats'].update(**{'cpuacct.usage':0}),
                       lambda r:r[15]['worker']['cgroup'].update(**{'memory.events':'oom 0\noom_kill 0\nhigh 0\n'})):
            r=rows();mutate(r)
            # Last case needs a prior positive event before zero to exercise regression.
            if r[15]['worker']['cgroup']['memory.events'].startswith('oom 0'):
                r[14]['worker']['cgroup']['memory.events']='oom 1\noom_kill 0\nhigh 0\n'
            with self.assertRaises(ValueError):a.analyze(r,100*10**9,125*10**9,'owned',100)

    def test_explicit_task_build_and_timezone_required(self):
        self.assertEqual(a.utc_ns('1970-01-01T01:00:00+01:00'),0)
        self.assertEqual(a.utc_ns('1970-01-01T00:00:00.123456Z'),123456000)
        with self.assertRaises(ValueError):a.utc_ns('2026-09-25T20:54:51')
        for args in [('missing',100,None,None),('owned',0,None,None),('owned',100,99*10**9,101*10**9),('owned',100,None,101*10**9)]:
            with self.assertRaises(ValueError):a.analyze(rows(),100*10**9,125*10**9,*args)

    def test_cli_private_artifacts_and_exact_input_hashes(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);raw=root/'samples.jsonl';identity=root/'identity.json';out=root/'report'
            raw.write_text(''.join(json.dumps(r)+'\n' for r in rows()))
            identity.write_text(json.dumps({'outer':{'pid':42,'start_ticks':10,'clock_ticks_per_second':100}}))
            command=[sys.executable,str(Path(a.__file__)),str(raw),'--identity',str(identity),'--task-id','owned',
                     '--task-start','1970-01-01T00:01:46Z','--task-end','1970-01-01T00:02:00Z','--output',str(out)]
            subprocess.run(command,check=True,capture_output=True)
            report=json.loads((out/'analysis.json').read_text())
            self.assertEqual(report['input_sha256']['samples'],a.hashlib.sha256(raw.read_bytes()).hexdigest())
            self.assertEqual((out/'analysis.json').stat().st_mode & 0o777,0o600)
            self.assertEqual(out.stat().st_mode & 0o777,0o700)
            self.assertIn('actual_task_including_build',(out/'table.md').read_text())
            self.assertNotEqual(subprocess.run(command,capture_output=True).returncode,0)

    def test_raw_bounds_hash_partial_and_absent_io(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'rows.jsonl';p.write_text(''.join(json.dumps(r)+'\n' for r in rows()))
            loaded,digest=a.load_rows(p);self.assertEqual(len(loaded),26);self.assertEqual(len(digest),64)
            p.write_bytes(p.read_bytes()[:-1])
            with self.assertRaises(ValueError):a.load_rows(p)
        r=rows()
        for row in r:row['worker']['cgroup']['io.stat']=None
        self.assertIsNone(a.analyze(r,100*10**9,125*10**9,'owned',100)['phases'][0]['guest_vm']['cgroup_block_io'])


if __name__=='__main__':unittest.main()
