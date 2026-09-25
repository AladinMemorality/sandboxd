import copy
import unittest
from post_boot import check_config,check_open_roots,check_template,ROOT,PLUGINS
from render_config import render

class PostBootTests(unittest.TestCase):
 def test_rejects_startup_config_overwrite_and_sync_change(self):
  old='state="/data/cubelet/state"\nroot="/data/cubelet/root"\n'
  check_config(render(old),old)
  for config in (old,render(old).replace('no_sync = false','no_sync = true'),render(old)+'\nextra="unreviewed"'):
   with self.assertRaises(ValueError):check_config(config,old)
 def test_requires_actual_persistent_fds_not_only_config(self):
  paths=[ROOT+'/'+sub+'/meta.db' for _,sub in PLUGINS.values() if sub!='netfile']
  self.assertEqual(check_open_roots(paths)['netfile'],0)
  for variant in (paths[:-1],paths+['/data/cubelet/state/cubebox/meta.db'],paths+[paths[0]+' (deleted)']):
   with self.assertRaises(ValueError):check_open_roots(variant)
  check_open_roots(paths+['/data/cubelet/state/io.containerd.mount-manager.v1.bolt/mounts.db'])
 def test_template_checks_do_not_replace_missing_or_weaken_resources(self):
  item={'status':'READY','create_request':{'containers':[{'resources':{'cpu':'2000m','mem':'2048Mi'},'probe':{'probe_handler':{'http_get':{'port':49983,'path':'/health'}}}}],'cube_network_config':{'denyOut':['0.0.0.0/0']},'volumes':[{'volume_source':{'empty_dir':{'size_limit':'10Gi'}}}]}}
  row={'template_id':'tpl-'+'a'*24};check_template(item,row)
  bad=copy.deepcopy(item);bad['create_request']['containers'][0]['resources']['mem']='1024Mi'
  with self.assertRaises(ValueError):check_template(bad,row)
  bad=copy.deepcopy(item);bad['status']='BUILDING'
  with self.assertRaises(ValueError):check_template(bad,row)
if __name__=='__main__':unittest.main()
