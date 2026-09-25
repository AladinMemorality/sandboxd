import importlib.util
from pathlib import Path
import unittest
s=importlib.util.spec_from_file_location('observe',Path(__file__).with_name('observe.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
class ObserverTest(unittest.TestCase):
 def setUp(self):
  self.c={'outer_boot_id':'66666666-6666-6666-6666-666666666666','observation_path':'/run/sandboxd-cube-storage/observation.json','observer_id':'1'*32,'worker_machine_id':'2'*32,'expected_boot_id':'33333333-3333-3333-3333-333333333333','inner_fs_uuid':'44444444-4444-4444-4444-444444444444','outer_fs_uuid':'55555555-5555-5555-5555-555555555555'}
  self.inner={'worker_machine_id':self.c['worker_machine_id'],'worker_boot_id':self.c['expected_boot_id'],'inner_fs_uuid':self.c['inner_fs_uuid'],'inner_free_bytes':100<<30}
  self.outer={'outer_fs_uuid':self.c['outer_fs_uuid'],'outer_free_bytes':200<<30}
 def run_probe(self,old=None,times=(100_000_000_000,101_000_000_000)):
  it=iter(times);return m.make_observation(self.c,old,lambda:self.inner,lambda:self.outer,lambda:next(it),lambda:self.c["outer_boot_id"])
 def test_measurement_start_precedes_probes(self):
  o=self.run_probe();self.assertEqual(o['started_boottime_ns'],100_000_000_000);self.assertEqual(o['generation'],1)
  old={'observer_id':o['observer_id'],'generation':o['generation'],'started_ns':o['started_boottime_ns'],'outer_boot_id':o['outer_boot_id']};self.assertEqual(self.run_probe(old,(102_000_000_000,103_000_000_000))['generation'],2)
 def test_boot_and_filesystem_fail_closed(self):
  for target,key in [(self.inner,'worker_boot_id'),(self.inner,'inner_fs_uuid'),(self.inner,'worker_machine_id'),(self.outer,'outer_fs_uuid')]:
   original=target[key];target[key]='wrong'
   with self.assertRaises(m.Invalid):self.run_probe()
   target[key]=original
 def test_clock_and_sequence_fail_closed(self):
  for times in [(100,99),(100,20_000_000_101)]:
   with self.assertRaises(m.Invalid):self.run_probe(times=times)
  for old in [{'observer_id':'2'*32,'generation':1,'started_ns':1},{'observer_id':'1'*32,'generation':1,'started_ns':100_000_000_000},{'observer_id':'1'*32,'generation':True,'started_ns':1}]:
   with self.assertRaises(m.Invalid):self.run_probe({**old,"outer_boot_id":self.c["outer_boot_id"]})
 def test_probe_failure_never_becomes_zero_or_fresh(self):
  def fail():raise OSError('offline')
  with self.assertRaises(OSError):m.make_observation(self.c,None,fail,lambda:self.outer,lambda:100,lambda:self.c["outer_boot_id"])
 def test_root_only_fixed_output_and_strict_json(self):
  self.c['observation_path']='/tmp/user.json'
  with self.assertRaises(m.Invalid):self.run_probe()
  with self.assertRaises(m.Invalid):m.strict_json('{"x":1,"x":2}')
 def test_corrupt_sequence_is_not_reinterpreted_as_fresh(self):
  for old in [{'generation':1}, {'observer_id':self.c['observer_id'],'generation':0,'started_ns':1,'outer_boot_id':self.c['outer_boot_id']}]:
   with self.assertRaises(m.Invalid):self.run_probe(old)
 def test_low_space_is_truthfully_published(self):
  self.inner['inner_free_bytes']=0;self.assertEqual(self.run_probe()['inner_free_bytes'],0)
 def test_unreviewed_outer_boot_cannot_publish(self):
  with self.assertRaises(m.Invalid):m.make_observation(self.c,None,lambda:self.inner,lambda:self.outer,lambda:100,lambda:'77777777-7777-7777-7777-777777777777')
 def test_reviewed_boot_reset_preserves_generation(self):
  old={'observer_id':self.c['observer_id'],'generation':82,'started_ns':999_000_000_000,'outer_boot_id':'77777777-7777-7777-7777-777777777777'}
  o=self.run_probe(old);self.assertEqual(o['generation'],83);self.assertEqual(o['outer_boot_id'],self.c['outer_boot_id'])
if __name__=='__main__':unittest.main()
