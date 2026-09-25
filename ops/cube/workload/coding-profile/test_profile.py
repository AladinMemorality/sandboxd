import copy,io,json,os,sys,types,unittest
from unittest.mock import patch
import metrics as m
import sample
class MetricsTest(unittest.TestCase):
 def test_proc_stat_comm_spaces_and_generation(self):
  fields=['S']+['0']*30
  for index,value in {7:4,9:2,11:100,12:50,17:3,19:456,20:4096,21:2}.items():fields[index]=str(value)
  got=m.proc_stat('12 (name with ) spaces) '+' '.join(fields))
  self.assertEqual(got['start_ticks'],456);self.assertEqual(got['user_ticks'],100);self.assertEqual(got['minor_faults'],4)
  with self.assertRaises(ValueError):m.proc_stat('12 (x) S')
 def guest(self):return 'ID TIMESTAMP\n'+m.PROVIDER+' now\nMETRIC VALUE\nmemory.usage_in_bytes 123\nmemory.limit_in_bytes 2147483648\nmemory.stat.cache 0\ncpuacct.usage 5000000000\n'
 def test_guest_scope_missing_values_and_cache_default(self):
  self.assertEqual(m.parse_guest_stats(self.guest())['memory.usage_in_bytes'],123)
  self.assertIsNone(m.parse_guest_stats(self.guest())['memory.stat.cache'])
  for raw in ['',self.guest().replace(m.PROVIDER,'other'),self.guest().replace('2147483648','4294967296'),self.guest().replace('memory.usage_in_bytes 123\n',''),self.guest()+'cpuacct.usage 6\n']:
   with self.assertRaises(ValueError):m.parse_guest_stats(raw)
 def test_cpu_and_io_deltas_do_not_confuse_percentage_or_reuse(self):
  process={'pid':1,'start_ticks':10,'user_ticks':100,'system_ticks':50,'io':{'read_bytes':100,'write_bytes':20}}
  before={'outer':{'process':copy.deepcopy(process)},'worker':{'process':copy.deepcopy(process)}}
  after=copy.deepcopy(before)
  for scope in after:after[scope]['process']['user_ticks']+=200;after[scope]['process']['io']['write_bytes']+=40
  result=sample.cpu_delta(before,after,2,100)
  self.assertEqual(result['worker_process_cpu_percent_one_core'],100)
  self.assertEqual(result['worker_write_bytes_delta'],40)
  after['worker']['process']['start_ticks']=11
  with self.assertRaises(ValueError):sample.cpu_delta(before,after,2,100)
 def test_stream_framing_keeps_prefetched_lines(self):
  r,w=os.pipe();os.write(w,b'{"a":1}\n{"b":2}\n');os.close(w)
  with os.fdopen(r,'rb',buffering=0) as f:
   proc=types.SimpleNamespace(stdout=f)
   self.assertEqual(sample.read_line(proc),{'a':1});self.assertEqual(sample.read_line(proc),{'b':2})
   with self.assertRaises(ValueError):sample.read_line(proc,.01)
 def test_worker_scope_refuses_arbitrary_duration_before_commands(self):
  for duration in [0,9,1201]:
   with patch.object(m,'worker_identity',side_effect=AssertionError('must not observe')):
    with self.assertRaises(ValueError):m.worker_stream(duration)
 def test_no_guest_exec_or_secret_proc_read(self):
  # Validate the actual fixed-command behavior used for supported guest stats.
  identity={'worker_boot_id':'boot','pid':1,'start_ticks':1,'cgroup':'/cube_sandbox/sandbox/4','disk_path':'/exact','disk_inode':2,'disk_device':3,'disk_logical_bytes':10}
  stat=types.SimpleNamespace(st_ino=2,st_dev=3,st_size=10,st_blocks=1,st_mtime_ns=0)
  with patch.object(m.time,'CLOCK_BOOTTIME',0,create=True),patch.object(m,'boot',return_value='boot'),patch.object(m,'process',return_value={'cgroup':'0::/cube_sandbox/sandbox/4'}),patch.object(m.P,'stat',return_value=stat),patch.object(m.P,'resolve',return_value=m.P('/exact')),patch.object(m,'cgroup',return_value={}),patch.object(m,'host_memory',return_value={}),patch.object(m,'run',return_value=self.guest()) as run:
   row=m.worker_sample(identity,True)
   self.assertEqual(row['guest_stats']['memory.usage_in_bytes'],123)
   self.assertEqual(run.call_args.args[0],['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','metrics',m.PROVIDER])
if __name__=='__main__':unittest.main()
