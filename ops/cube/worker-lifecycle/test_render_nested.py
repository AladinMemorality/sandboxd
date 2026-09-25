import copy
import importlib.util
from pathlib import Path
import unittest

spec=importlib.util.spec_from_file_location('render_nested',Path(__file__).with_name('render_nested.py'))
render=importlib.util.module_from_spec(spec);spec.loader.exec_module(render)
class RenderNestedTests(unittest.TestCase):
 def observation(self):
  return {'version':1,'machine_id':'a'*32,'data_uuid':'b'*36,
   'native':{name:{'path':str(render.TOOLBOX/path),'sha256':'c'*64,'pid':123,'start_time':'42','unit_type':'forking' if name=='cubelet' else 'simple'} for name,path in render.NATIVE.items()},
   'artifacts':{'/usr/local/services/cubetoolbox/.one-click.env':'d'*64,'/etc/systemd/system/cube-sandbox-control.target':'e'*64},
   'registry':{'id':'a'*64,'name':'/cube-production-registry','image':'sha256:'+'b'*64,'restart':{'Name':'always','MaximumRetryCount':0},'ports':{'5000/tcp':[{'HostIp':'127.0.0.1','HostPort':'5000'}]},'mounts':[{'Type':'bind','Source':'/data/registry','Destination':'/var/lib/registry','RW':True}]},
   'metadata_paths':[str(render.META/child) for key,child in render.PLUGINS.values()]}
 def test_outputs_remain_unreviewed_and_no_hooks_or_deletions(self):
  manifest,files=render.render(self.observation(),'f'*64)
  self.assertFalse(manifest['reviewed']);self.assertFalse(manifest['durable_metadata_reviewed']);self.assertEqual(len(files),17)
  self.assertEqual(manifest['artifacts'][render.SCRIPT],'f'*64)
  self.assertIn(str(render.TOOLBOX/'CubeOps/bin/cubeops'),manifest['artifacts'])
  self.assertEqual(len(manifest['durable_metadata_paths']),11)
  for destination,data in files.items():
   self.assertTrue(destination.startswith('/etc/systemd/system/'));self.assertIn('Restart=no\n',data);self.assertIn('TimeoutStopSec=infinity\n',data)
   self.assertNotIn('rm ',data);self.assertNotIn('SIGKILL=yes',data);self.assertNotIn('systemctl ',data)
   if 'docker.service.d' not in destination:self.assertIn('stop-component --unit cube-sandbox-',data)
  self.assertNotIn('allow-services',str(files))
 def test_shell_wrapper_or_wrong_installed_binary_cannot_be_enrolled(self):
  for field,value in [('path','/usr/bin/bash'),('unit_type','forking'),('pid',0),('sha256','unknown')]:
   observed=self.observation();observed['native']['cube-api'][field]=value
   with self.assertRaises(RuntimeError):render.render(observed,'f'*64)
 def test_missing_metadata_and_arbitrary_artifacts_refused(self):
  observed=self.observation();observed['metadata_paths'].pop()
  with self.assertRaises(RuntimeError):render.render(observed,'f'*64)
  observed=self.observation();observed['artifacts']['/home/sandbox/executable']='0'*64
  with self.assertRaises(RuntimeError):render.render(observed,'f'*64)
 def test_existing_hold_dropins_are_preserved_and_pinned(self):
  observed=self.observation();path='/etc/systemd/system/cube-sandbox-cubelet.service.d/90-durable-enrollment-hold.conf';observed['artifacts'][path]='1'*64
  manifest,files=render.render(observed,'f'*64)
  self.assertEqual(manifest['artifacts'][path],'1'*64);self.assertNotIn(path,files)

 def test_registry_policy_and_definition_are_pinned(self):
  observed=self.observation();manifest,_=render.render(observed,'f'*64)
  self.assertEqual(manifest['registry'],observed['registry'])
  for key,value in [('restart',{'Name':'unless-stopped','MaximumRetryCount':0}),('ports',{'5000/tcp':[{'HostIp':'0.0.0.0','HostPort':'5000'}]}),('mounts',[]),('id','short')]:
   changed=copy.deepcopy(observed);changed['registry'][key]=value
   with self.assertRaises(RuntimeError):render.render(changed,'f'*64)
