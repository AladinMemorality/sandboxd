import unittest
from render_quota import render,UniqueLoader
import yaml

BASE='''# retain comments
host:
  quota:
    mcpu_limit: 28000 # explicit previous limit
    mem_limit: "30Gi"
    mvm_limit: 128
    creation_concurrent_num: 1
    paused_resource_release_ratio: 1.0
private:
  preserved: "unchanged-value"
'''
class QuotaTests(unittest.TestCase):
 def test_changes_exact_fields_preserves_rest(self):
  result=render(BASE);obj=yaml.load(result,Loader=UniqueLoader)
  self.assertEqual(obj['host']['quota']['mcpu_limit'],10000)
  self.assertEqual(obj['host']['quota']['mem_limit'],'10Gi')
  self.assertEqual(obj['host']['quota']['mvm_limit'],128)
  self.assertEqual(obj['private']['preserved'],'unchanged-value')
  self.assertIn('# retain comments',result);self.assertIn('# explicit previous limit',result)
 def test_refuses_unknown_prior_quota_and_duplicate_keys(self):
  for source in (BASE.replace('28000','29000'),BASE+'host: {}\n',BASE.replace('    mem_limit:', '    mcpu_limit: 28000\n    mem_limit:'),BASE+'other:\n  mcpu_limit: 9\n'):
   with self.assertRaises(ValueError):render(source)
if __name__=='__main__':unittest.main()
