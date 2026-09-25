import copy
import importlib.util
import json
from pathlib import Path
import unittest
s=importlib.util.spec_from_file_location('prepare',Path(__file__).with_name('prepare_enrollment.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
class PrepareTest(unittest.TestCase):
 def setUp(self):
  root=Path(__file__).parent
  self.pins=json.loads((root/'enrollment/pins.NON-AUTHORIZING.json').read_text())
  self.templates=json.loads((root/'template-capacity-verified-2026-09-25.json').read_text())
  self.boot={'version':1,'verified':True,'outer_boot_id':self.pins['outer_boot_id'],'worker_machine_id':self.pins['worker_machine_id'],'worker_boot_id':'11111111-1111-1111-1111-111111111111','data_uuid':self.pins['inner_fs_uuid'],'evidence_sha256':'a'*64,'qemu_pid':789,'qemu_start_time':'987','controller_id':'c'*64}
  self.admission={'max_active':4,'cpu_count':2,'memory_mb':2048,'templates':{t['template_id']:{'cpu_count':2,'memory_mb':2048} for t in self.templates['templates']}}
  self.stop={'admission':copy.deepcopy(self.admission),'api_url':'http://127.0.0.1:20300','worker_machine_id':self.pins['worker_machine_id'],'data_uuid':self.pins['inner_fs_uuid'],'worker_boot_id':'old','api_key':'synthetic-private-key','database':'/existing/state.db','migrations':'/old/migrations','qemu_pid':123,'qemu_start_time':'456'}
 def render(self):return m.render(self.pins,self.boot,self.admission,self.stop,self.templates)
 def test_placeholder_cannot_be_installed_or_used_as_boot_evidence(self):
  with self.assertRaises(m.observe.Invalid):m.observe.validate_config(self.pins)
  self.boot['worker_boot_id']=m.UNVERIFIED
  with self.assertRaises(m.observe.Invalid):self.render()
 def test_render_preserves_private_fields_and_matches_both_boot_pins(self):
  old=copy.deepcopy(self.stop);g,a,s=self.render();self.assertEqual(old,self.stop);self.assertEqual(s['api_key'],'synthetic-private-key');self.assertEqual(s['qemu_pid'],789);self.assertEqual(s['qemu_start_time'],'987');self.assertEqual(s['controller_id'],'c'*64);self.assertEqual(g['expected_boot_id'],s['worker_boot_id']);self.assertEqual(a['writable_disk_mb'],10240);self.assertEqual(s['migrations'],m.MIGRATIONS_TARGET);self.assertEqual(s['admission'],a)
 def test_identity_and_template_drift_refuse(self):
  for field in ('outer_boot_id','worker_machine_id','data_uuid'):
   old=self.boot[field];self.boot[field]='wrong'
   with self.assertRaises(m.observe.Invalid):self.render()
   self.boot[field]=old
  self.templates['templates'][0]['rootfs_writable_mb']=20480
  with self.assertRaises(m.observe.Invalid):self.render()
 def test_legacy_twelve_slots_and_observer_replacement_refuse(self):
  self.admission['max_active']=12
  with self.assertRaises(m.observe.Invalid):self.render()
  self.admission['max_active']=4;g,_,_=self.render();g['observer_id']='0'*32;self.admission['storage_guard']=g
  with self.assertRaises(m.observe.Invalid):self.render()
 def test_mount_is_directory_readonly_and_does_not_enable_cube(self):
  _,a,_=self.render();out=m.compose_override(a);self.assertIn('read_only: true',out);self.assertIn('create_host_path: false',out);self.assertNotIn('CUBE_ENABLED',out);self.assertNotIn('synthetic-private-key',out)
 def test_receipt_must_have_actual_proof_reference(self):
  self.boot['verified']=False
  with self.assertRaises(m.observe.Invalid):self.render()
if __name__=='__main__':unittest.main()
