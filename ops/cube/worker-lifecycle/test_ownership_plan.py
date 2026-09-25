import hashlib
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec=importlib.util.spec_from_file_location('ownership_plan',Path(__file__).with_name('ownership_plan.py'))
plan=importlib.util.module_from_spec(spec);spec.loader.exec_module(plan)

class OwnershipPlanTests(unittest.TestCase):
 def test_dynamic_quota_configuration_is_in_fixed_ownership_scope(self):
  self.assertIn('Cubelet/dynamicconf/conf.yaml',plan.FIXED)

 def test_explicit_control_scope_excludes_customer_and_runtime_data(self):
  for path in ['/data/cubelet','/home/sandbox','/var/lib/docker/volumes','/usr/local/services/other','/usr/local/services/cubetoolbox-other']:
   self.assertFalse(plan.in_scope(Path(path)))
  for path in [str(plan.BASE/'CubeOps/bin/cubeops'),'/usr/local/services','/etc/systemd/system/docker.service.d']:
   self.assertTrue(plan.in_scope(Path(path)))
 def test_hash_inventory_does_not_change_content_mode_or_ownership(self):
  with tempfile.TemporaryDirectory() as temp,mock.patch.object(plan,'in_scope',return_value=True):
   path=Path(temp).resolve()/'input';path.write_bytes(b'fixture-control-data');path.chmod(0o640);before=path.stat()
   row=plan.entry(path);after=path.stat()
   self.assertEqual(row['sha256'],hashlib.sha256(b'fixture-control-data').hexdigest())
   self.assertEqual((before.st_uid,before.st_gid,before.st_mode,before.st_ino,before.st_ctime_ns),(after.st_uid,after.st_gid,after.st_mode,after.st_ino,after.st_ctime_ns))
   self.assertEqual(row['desired_uid'],0);self.assertEqual(row['desired_gid'],0)
 def test_links_and_group_writable_inputs_refused(self):
  with tempfile.TemporaryDirectory() as temp,mock.patch.object(plan,'in_scope',return_value=True):
   root=Path(temp).resolve();path=root/'input';path.write_text('fixture');path.chmod(0o640)
   link=root/'symlink';link.symlink_to(path)
   with self.assertRaises(RuntimeError):plan.entry(link)
   os.link(path,root/'hardlink')
   with self.assertRaises(RuntimeError):plan.entry(path)
   (root/'hardlink').unlink();path.chmod(0o660)
   with self.assertRaises(RuntimeError):plan.entry(path)
 def test_directories_record_ownership_without_content_traversal(self):
  with tempfile.TemporaryDirectory() as temp,mock.patch.object(plan,'in_scope',return_value=True):
   path=Path(temp).resolve();row=plan.entry(path)
   self.assertEqual(row['kind'],'directory');self.assertIsNone(row['sha256'])
