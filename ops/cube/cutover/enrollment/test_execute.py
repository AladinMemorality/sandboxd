import importlib.util,json,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
from types import SimpleNamespace
s=importlib.util.spec_from_file_location('runner',str(Path(__file__).with_name('execute.py')))
r=importlib.util.module_from_spec(s);s.loader.exec_module(r)
class Checks(unittest.TestCase):
 def test_model_paths_not_caught_by_scoped_project_fence(self):
  scopes=[[[{'host':['baarcha.tn']}],[{'path':['/api/projects','/api/projects/*','/api/bridge','/api/tools/call']}]],[[{'host':['*.preview.baarcha.tn']}]]]
  self.assertTrue(r.model_routes_unfenced(scopes))
 def test_broad_main_host_fence_refused(self):
  self.assertFalse(r.model_routes_unfenced([[[{'host':['baarcha.tn']}]]]))
 def test_model_specific_fence_refused(self):
  self.assertFalse(r.model_routes_unfenced([[[{'host':['baarcha.tn'],'path':['/api/v1/*']}]]]))
 def test_unknown_matcher_refused(self):
  with self.assertRaises(RuntimeError):r.model_routes_unfenced([[[{'expression':'arbitrary'}]]])
 def test_nested_503_scopes_preserved(self):
  v={'routes':[{'match':[{'host':['baarcha.tn']}],'handle':[{'handler':'subroute','routes':[{'match':[{'path':['/api/projects*']}],'handle':[{'handler':'static_response','status_code':503}]}]}]}]}
  self.assertEqual(r.caddy_fence_scopes(v),[[[{'host':['baarcha.tn']}],[{'path':['/api/projects*']}]]])
 def test_known_terminal_provider_groups_accepted(self):
  with patch.object(r.subprocess,'run',return_value=SimpleNamespace(returncode=0,stdout=json.dumps(r.JOB_GROUPS).encode())):
   self.assertEqual(r.provider_jobs()['active_jobs'],0)
 def test_nonterminal_or_missing_provider_groups_refused(self):
  for change in ['RUNNING','BUILT','PENDING','missing']:
   v=json.loads(json.dumps(r.JOB_GROUPS))
   if change=='missing':v.pop('t_component_import_job')
   else:v['t_cube_template_image_job'][change]=1
   with patch.object(r.subprocess,'run',return_value=SimpleNamespace(returncode=0,stdout=json.dumps(v).encode())):
    with self.assertRaises(RuntimeError):r.provider_jobs()
 def test_provider_query_failure_not_zero(self):
  with patch.object(r.subprocess,'run',return_value=SimpleNamespace(returncode=1,stdout=b'')):
   with self.assertRaises(RuntimeError):r.provider_jobs()
 def test_early_failure_no_unrequested_restore(self):
  t=r.Enrollment.__new__(r.Enrollment);t.job=Path('/tmp');t.phase='preflight';t.changed=False;t.worker_touched=False;t.event=lambda *a:None
  t.restore_traffic=lambda:self.fail('unrequested restore')
  t.failed(RuntimeError('fixture'))
 def test_failure_retains_until_explicit_manual_release(self):
  with tempfile.TemporaryDirectory() as tmp:
   t=r.Enrollment.__new__(r.Enrollment);t.job=Path(tmp);t.phase='manual-stop-intent';t.changed=True;t.worker_touched=True
   events=[];t.event=lambda *a:events.append(a);t.advance=lambda *a:events.append(a)
   t.restore_traffic=lambda:self.fail('must not automatically restore after worker mutation')
   (t.job/'recovery-command-01.json').write_text(json.dumps({'job':str(t.job),'action':'restore-before-worker'}))
   (t.job/'recovery-command-02.json').write_text(json.dumps({'job':str(t.job),'action':'release-after-manual-recovery','operator_accepts_remaining_fence':True}))
   with patch.object(r,'private',side_effect=lambda p,*a:p):t.failed(RuntimeError('fixture'))
   self.assertTrue(any(x[0].startswith('recovery-refused-') for x in events))
   self.assertEqual(events[-1][0],'released-to-manual-recovery')
 def test_no_resize_unless_explicit(self):
  t=r.Enrollment.__new__(r.Enrollment);t.resize=False
  with patch.object(r,'run',side_effect=AssertionError('unexpected mutation')):t.resize_disk()
 def test_exact_reviewed_boot_hold_accepted(self):
  info=SimpleNamespace(st_uid=0,st_gid=0,st_mode=0o100600)
  with patch.object(r,'private',side_effect=lambda p:p),patch.object(r,'sha',side_effect=lambda p:r.HOLD_SHA if p==r.HOLD else r.ALLOW_SHA),patch.object(Path,'stat',return_value=info):
   result=r.reviewed_hold({'UnitFileState':'disabled','DropInPaths':str(r.HOLD)})
  self.assertEqual(result['dropin']['sha256'],r.HOLD_SHA)
  self.assertEqual(result['allow']['path'],str(r.ALLOW))
 def test_missing_or_extra_boot_hold_refused(self):
  for paths in ['',str(r.HOLD)+' /etc/systemd/system/other.conf']:
   with self.assertRaises(RuntimeError):r.reviewed_hold({'UnitFileState':'disabled','DropInPaths':paths})
 def test_enabled_boot_unit_still_refused(self):
  with self.assertRaises(RuntimeError):r.reviewed_hold({'UnitFileState':'enabled','DropInPaths':str(r.HOLD)})
 def test_changed_hold_bytes_refused(self):
  with patch.object(r,'private',side_effect=lambda p:p),patch.object(r,'sha',return_value='0'*64):
   with self.assertRaises(RuntimeError):r.reviewed_hold({'UnitFileState':'disabled','DropInPaths':str(r.HOLD)})
 def test_unsafe_hold_owner_or_mode_refused(self):
  with patch.object(r,'private',side_effect=RuntimeError('unsafe private path')):
   with self.assertRaises(RuntimeError):r.reviewed_hold({'UnitFileState':'disabled','DropInPaths':str(r.HOLD)})
if __name__=='__main__':unittest.main()
