import importlib.util,json,tempfile,unittest,subprocess,sys,os,hashlib
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
 def test_existing_certificate_preview_host_is_exactly_pinned(self):
  host='s-'+'a'*26+'-3000.preview.65.108.225.153.sslip.io'
  value=SimpleNamespace(read_text=lambda:json.dumps({'host':host}))
  with patch.object(r,'private',return_value=value),patch.object(r,'PREVIEW_HOST_SHA',hashlib.sha256(host.encode()).hexdigest()):
   self.assertEqual(r.reviewed_preview_host(),host)
 def test_preview_host_query_port_or_unreviewed_app_refused(self):
  for host in ['s-'+'a'*26+'-3000.preview.65.108.225.153.sslip.io?token=x','s-'+'a'*26+'-3000.preview.65.108.225.153.sslip.io:443','s-'+'a'*26+'-3000.preview.65.108.225.153.sslip.io']:
   with patch.object(r,'private',return_value=SimpleNamespace(read_text=lambda:json.dumps({'host':host}))):
    with self.assertRaises(RuntimeError):r.reviewed_preview_host()
 def test_preview_request_preserves_tls_verification(self):
  host='reviewed.fixture'
  with patch.object(r,'reviewed_preview_host',return_value=host),patch.object(r,'run',return_value=b'503') as called:
   self.assertEqual(r.route_status('/',True),503)
  args=called.call_args.args[0]
  self.assertIn('--resolve',args);self.assertIn(host+':443:127.0.0.1',args)
  self.assertNotIn('-k',args);self.assertNotIn('--insecure',args)
class PrerequisiteTests(unittest.TestCase):
 def test_missing_install_parent_refused_before_drain(self):
  with patch.object(Path,'exists',return_value=False):
   with self.assertRaisesRegex(RuntimeError,'installation directory absent'):r.install_parent_preflight()
 def test_canonical_root_install_parents_required(self):
  info=SimpleNamespace(st_mode=0o40755,st_uid=0,st_gid=0)
  with patch.object(Path,'exists',return_value=True),patch.object(Path,'resolve',lambda p:p),patch.object(Path,'stat',return_value=info):
   r.install_parent_preflight()
  for wrong in [SimpleNamespace(st_mode=0o40700,st_uid=0,st_gid=0),SimpleNamespace(st_mode=0o40755,st_uid=1000,st_gid=0),SimpleNamespace(st_mode=0o40755,st_uid=0,st_gid=1000),SimpleNamespace(st_mode=0o100755,st_uid=0,st_gid=0)]:
   with patch.object(Path,'exists',return_value=True),patch.object(Path,'resolve',lambda p:p),patch.object(Path,'stat',return_value=wrong):
    with self.assertRaises(RuntimeError):r.install_parent_preflight()
 def test_old_observer_never_loaded(self):
  with patch.object(r,'sha',return_value='0'*64),patch.object(r,'load_module') as loaded:
   with self.assertRaisesRegex(RuntimeError,'deterministic-close'):r.reviewed_observer()
   loaded.assert_not_called()
 def test_reviewed_observer_hash_matches_repository_fix(self):
  source=Path(__file__).parent.parent/'drain_observe.py'
  self.assertEqual(hashlib.sha256(source.read_bytes()).hexdigest(),r.OBSERVER_SHA)
  with patch.object(r,'sha',return_value=r.OBSERVER_SHA),patch.object(r,'load_module',return_value='reviewed'):
   self.assertEqual(r.reviewed_observer(),'reviewed')
class NestedLockTests(unittest.TestCase):
 def setUp(self):
  self.temp=tempfile.TemporaryDirectory(dir='/private/tmp' if Path('/private/tmp').is_dir() else None)
  self.path=Path(self.temp.name).resolve()/'acceptance.lock';self.path.touch(mode=0o600)
  self.real_popen=subprocess.Popen
  self.code=r.NESTED_LOCK_CODE.replace("'/run/lock/cube-operator-acceptance.lock'",repr(str(self.path))).replace('s.st_uid==0','s.st_uid==os.geteuid()').replace("pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()",repr('fixture-boot'))
  self.patch=patch.object(r.subprocess,'Popen',side_effect=lambda args,**kwargs:self.real_popen([sys.executable,'-u','-c',self.code],**kwargs));self.patch.start()
 def tearDown(self):self.patch.stop();self.temp.cleanup()
 def test_actual_flock_contention_heartbeat_and_release(self):
  holder=r.NestedLock('fixture-boot')
  try:
   holder.heartbeat()
   with self.assertRaises(RuntimeError):r.NestedLock('fixture-boot')
   holder.heartbeat()
  finally:holder.close()
  next_holder=r.NestedLock('fixture-boot');next_holder.heartbeat();next_holder.close()
 def test_different_boot_refused_and_lock_released(self):
  with self.assertRaises(RuntimeError):r.NestedLock('wrong-boot')
  holder=r.NestedLock('fixture-boot');holder.close()
 def test_unexpected_holder_eof_blocks_further_actions(self):
  holder=r.NestedLock('fixture-boot');holder.process.stdin.close();holder.process.wait(timeout=5)
  with self.assertRaises(RuntimeError):holder.heartbeat()
  self.assertEqual(holder.process.returncode,0);holder.close()
 def test_known_poweroff_close_never_kills_ssh(self):
  holder=r.NestedLock('fixture-boot')
  with patch.object(holder.process,'kill',side_effect=AssertionError('no kill')),patch.object(holder.process,'terminate',side_effect=AssertionError('no terminate')):
   holder.close(poweroff_confirmed=True)
  self.assertEqual(holder.process.returncode,0)
 def test_missing_new_boot_lock_created_and_inode_preserved(self):
  self.path.unlink()
  holder=r.NestedLock('fixture-boot');holder.heartbeat()
  inode=self.path.stat().st_ino
  self.assertEqual(self.path.stat().st_mode&0o777,0o600);holder.close()
  next_holder=r.NestedLock('fixture-boot');next_holder.close()
  self.assertEqual(self.path.stat().st_ino,inode)
 def test_symlink_lock_refused_without_modifying_target(self):
  target=self.path.with_name('target');target.write_bytes(b'retained');target.chmod(0o600)
  self.path.unlink();self.path.symlink_to(target)
  with self.assertRaises(RuntimeError):r.NestedLock('fixture-boot')
  self.assertEqual(target.read_bytes(),b'retained');self.assertTrue(self.path.is_symlink())
 def test_wrong_mode_lock_refused_without_chmod(self):
  self.path.chmod(0o644)
  with self.assertRaises(RuntimeError):r.NestedLock('fixture-boot')
  self.assertEqual(self.path.stat().st_mode&0o777,0o644)
 def test_wrong_descriptor_owner_refused(self):
  # The fixture reports a foreign owner through fstat; production owner policy
  # remains exact-root. No chown privilege or real worker path is needed.
  self.code=self.code.replace('s=os.fstat(fd);', 's=os.fstat(fd);import types;s=types.SimpleNamespace(st_mode=s.st_mode,st_uid=os.geteuid()+1,st_dev=s.st_dev,st_ino=s.st_ino);')
  with self.assertRaises(RuntimeError):r.NestedLock('fixture-boot')
if __name__=='__main__':unittest.main()
