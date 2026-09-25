import unittest
import tomllib
from render_config import render,ROOT,PLUGINS
class ConfigTests(unittest.TestCase):
 def test_changes_only_exact_metadata_fields_preserving_private_and_data_config(self):
  text='''version=2
state="/data/cubelet/state"
root="/data/cubelet/root"
[plugins."io.cubelet.internal.v1.storage"]
data_path="/data/cubelet/root"
secret="private-value-kept"
[plugins."io.containerd.metadata.v1.bolt"]
root_path="/data/cubelet/state"
no_sync=true
[plugins."io.containerd.snapshotter.v1.overlayfs"]
root_path="/data/cubelet/state"
data_path="/data/cubelet/root"
'''
  out=tomllib.loads(render(text));self.assertFalse(out['plugins']['io.containerd.metadata.v1.bolt']['no_sync'])
  self.assertEqual(out['plugins']['io.cubelet.internal.v1.storage']['secret'],'private-value-kept')
  self.assertEqual(out['plugins']['io.containerd.snapshotter.v1.overlayfs']['data_path'],'/data/cubelet/root')
  for p,(k,d) in PLUGINS.items():self.assertEqual(out['plugins'][p][k],ROOT+'/'+d)
 def test_refuses_unknown_or_already_migrated_state(self):
  for text in ('state="/tmp/other"','state="/data/cubelet/state"\ndurable_metadata_root="/previous"'):
   with self.assertRaises(ValueError):render(text)
if __name__=='__main__':unittest.main()
