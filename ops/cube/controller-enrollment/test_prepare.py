import copy,importlib.util,json,pathlib,re,sqlite3,unittest
P=pathlib.Path
spec=importlib.util.spec_from_file_location('prepare',P(__file__).with_name('prepare.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
ROOT=P(__file__).resolve().parents[3]
class CandidateTest(unittest.TestCase):
 def setUp(self):
  self.d={'version':1,'reviewed':True,'scope':'single-owned-synthetic-app','app_id':'01ARZ3NDEKTSV4RRFFQ69G5FAV','external_user_id':'103','external_project_id':'owned-fixture','global_rollout':False,'direct_guest_egress':'deny-all','reverse_egress':True,'logical_model_origin':'https://cube-model.baarcha.tn','worker_boot_id':m.BOOT,'cubelet_sha256':'de3bd4c1a4db12c11d58cf7f558589f04ab4b3d736d4e72a947d45b8343bef9b','network_provenance':m.PROVENANCE.copy()}
  self.templates=json.loads((ROOT/'ops/cube/production-worker/templates-2026-09-24.json').read_text())
  self.stop={'controller_id':m.CP,'worker_boot_id':m.BOOT,'api_key':'fixture-private-key','admission':{'max_active':4,'writable_disk_mb':10240,'storage_guard':{'expected_boot_id':m.BOOT},'templates':{r['template_id']:{} for r in self.templates}}}
  self.relays={n:{'image':m.RELAY,'network_mode':'service:sandboxd','read_only':True} for n in ['cube-management-api','cube-management-proxy']}
 def render(self,**changes):
  d=copy.deepcopy(self.d);d.update(changes);return m.render({'services':{'sandboxd':{'environment':{'EXISTING':'keep'}}}}, {'services':{'sandboxd':{'environment':{'SANDBOXD_CUBE_ENABLED':'false'}},'unrelated':{'image':'untouched'}}},d,self.stop,self.templates,self.relays)
 def test_exact_canary_and_preserved_unrelated_state(self):
  before=copy.deepcopy(self.stop);out,active,env=self.render()
  self.assertEqual(env['SANDBOXD_CUBE_APP_IDS'],self.d['app_id']);self.assertEqual(env['SANDBOXD_CUBE_ROLLOUT'],'allowlist');self.assertEqual(env['SANDBOXD_CUBE_EGRESS_ALLOW_DOMAINS'],'');self.assertEqual(env['SANDBOXD_CUBE_APP_HTTP_SERVICES'],'{}')
  self.assertEqual(active['services']['sandboxd']['environment']['SANDBOXD_CUBE_ENABLED'],'true');self.assertEqual(active['services']['unrelated'],{'image':'untouched'});self.assertEqual(out['services']['sandboxd']['environment']['EXISTING'],'keep');self.assertEqual(self.stop,before)
  self.assertTrue(out['services']['sandboxd']['volumes'][0]['read_only']);self.assertFalse(out['services']['sandboxd']['volumes'][0]['bind']['create_host_path'])
 def test_actual_preset_go_ids(self):
  ids=set(re.findall(r'ID: "([a-z-]+)"',(ROOT/'control-plane/internal/preset/preset.go').read_text()))
  self.assertEqual(ids,{r['preset'] for r in self.templates})
  self.templates[0]['preset']='node-postgres-standard'
  with self.assertRaises(ValueError):self.render()
 def test_no_unreviewed_or_global_activation(self):
  for change in [{'reviewed':False},{'global_rollout':True},{'reverse_egress':False},{'direct_guest_egress':'allow-public'},{'app_id':'*'},{'logical_model_origin':'https://other.example'},{'network_provenance':{}},{'external_user_id':''}]:
   with self.subTest(change=change),self.assertRaises(ValueError):self.render(**change)
 def test_no_existing_relay_or_mount_override(self):
  for existing in [{'services':{'cube-management-api':{}}},{'services':{'sandboxd':{'volumes':[{'target':'/run/sandboxd-cube-storage'}]}}}]:
   with self.assertRaises(ValueError):m.render(existing,{},self.d,self.stop,self.templates,self.relays)
 def test_exact_owned_app_no_existing_guest(self):
  db=sqlite3.connect(':memory:');self.addCleanup(db.close)
  db.executescript('CREATE TABLE app(id TEXT,runtime_preset TEXT,external_user_id TEXT,external_project_id TEXT);CREATE TABLE sandbox(app_id TEXT);CREATE TABLE runtime_binding(id TEXT);')
  db.execute('INSERT INTO app VALUES(?,?,?,?)',(self.d['app_id'],'node-postgres','103','owned-fixture'));m.validate_app(db,self.d)
  wrong=dict(self.d,external_user_id='104')
  with self.assertRaises(ValueError):m.validate_app(db,wrong)
  db.execute('INSERT INTO sandbox VALUES(?)',(self.d['app_id'],))
  with self.assertRaises(ValueError):m.validate_app(db,self.d)
if __name__=='__main__':unittest.main()
