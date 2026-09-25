import contextlib
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import socket
import sqlite3
import stat
import subprocess
import sys
import tempfile
import types
import unittest
from unittest import mock

sys.path.insert(0,str(Path(__file__).resolve().parent))
import capture_roles as roles

class RoleTests(unittest.TestCase):
 def setUp(self):
  self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
  self.root=Path(self.temp.name).resolve();self.root.chmod(0o700)
  self.db=sqlite3.connect(':memory:');self.addCleanup(self.db.close)
  self.db.executescript('''CREATE TABLE task(status TEXT);CREATE TABLE cube_recovery(phase TEXT,source_artifact_paths_json TEXT);
  CREATE TABLE cube_admission(state TEXT);CREATE TABLE runtime_migration(phase TEXT);
  CREATE TABLE sandbox(id TEXT,container_id TEXT,workspace_mnt TEXT,runtime_provider TEXT);
  CREATE TABLE snapshot(id TEXT,image_path TEXT,status TEXT);
  INSERT INTO sandbox VALUES('source','full-id','home','docker');
  INSERT INTO snapshot VALUES('snapshot','/library/snapshot','ready');''')
  self.config={'version':1,'inventory':roles.database_inventory(self.db),'role_paths':{r:[] for r in roles.ROLE_NAMES},
   'reviewed_files':{str(roles.PREVIEW_KEY):'0'*64},'controller_env_sha256':'1'*64,'reviewed_caddy_sha256':'2'*64,
   'preview_host':'s-01m2swkj77ve69qxm9zfa33e0b-3000.preview.65.108.225.153.sslip.io',
   'stopped_writer_units':sorted(roles.REQUIRED_WRITERS),'role_byte_budget':1024,'minimum_free_bytes':roles.PAIR_RESERVE+2048,
   'ephemeral_sockets':[]}
  for r in ('controller-config','worker-config','worker-launch'):self.config['role_paths'][r]=['/reviewed/'+r]
  self.config['role_paths']['controller-config'].append(str(roles.PREVIEW_KEY))

 def test_exact_schema_inventory_and_new_source_detection(self):
  original=roles.database_inventory(self.db)
  self.assertEqual(len(original['homes']),1)
  self.db.execute("INSERT INTO sandbox VALUES('new','new-id','new-home','docker')")
  with self.assertRaisesRegex(RuntimeError,'inventory changed'):roles.validate_inventory(roles.database_inventory(self.db),original)

 def test_all_incomplete_work_blocks(self):
  for table,columns,values in [('task','status',"'running'"),('task','status',"'queued'"),('cube_admission','state',"'pending'"),('cube_recovery','phase,source_artifact_paths_json',"'created','{}'"),('runtime_migration','phase',"'imported'")]:
   with self.subTest(table=table,values=values):
    self.db.execute(f'INSERT INTO {table}({columns}) VALUES({values})')
    with self.assertRaises(RuntimeError):roles.database_inventory(self.db)
    self.db.execute(f'DELETE FROM {table}')

 def test_config_cannot_omit_key_writer_or_paired_space(self):
  roles.validate_config(self.config)
  for field,value in [('reviewed_files',{}),('stopped_writer_units',[]),('minimum_free_bytes',roles.PAIR_RESERVE),('preview_host','evil.example')]:
   c=copy.deepcopy(self.config);c[field]=value
   with self.subTest(field=field),self.assertRaises(RuntimeError):roles.validate_config(c)
  c=copy.deepcopy(self.config);c['role_paths']['controller-config']=['/unrelated']
  with self.assertRaisesRegex(RuntimeError,'omitted'):roles.validate_config(c)

 def test_signing_key_changed_is_not_accepted(self):
  key=self.root/'preview.env';key.write_bytes(b'fixture-only-secret');key.chmod(0o600)
  c={'reviewed_files':{str(key):hashlib.sha256(key.read_bytes()).hexdigest()}}
  # Private ownership is a native-root deployment check; patch only that local
  # ownership check so this synthetic content-drift test runs on developer Macs.
  with mock.patch.object(roles,'PREVIEW_KEY',key),mock.patch.object(roles,'private',return_value=key):
   info=key.lstat();fake=types.SimpleNamespace(st_mode=info.st_mode,st_uid=0)
   with mock.patch.object(Path,'lstat',return_value=fake),mock.patch.object(Path,'resolve',return_value=key):
    roles.verify_inputs(c);key.write_bytes(b'changed')
    with self.assertRaisesRegex(RuntimeError,'changed'):roles.verify_inputs(c)

 def test_socket_requires_exact_review_and_regular_sibling_preserved(self):
  home=self.root/'home';home.mkdir();(home/'private\n--filename').write_bytes(b'fixture')
  sock=socket.socket(socket.AF_UNIX);self.addCleanup(sock.close);sock.bind(str(home/'pg.sock'))
  with self.assertRaisesRegex(RuntimeError,'special file'):roles.validate_sources([str(home)])
  info=(home/'pg.sock').lstat();review={'path':str(home/'pg.sock'),'inode':info.st_ino,'device':info.st_dev,'reason':'fixture ephemeral socket'}
  self.assertEqual(roles.validate_sources([str(home)],[review]),[str(home/'pg.sock')])
  bad=dict(review,inode=info.st_ino+1)
  with self.assertRaises(RuntimeError):roles.validate_sources([str(home)],[bad])
  (home/'pg.sock').unlink()
  with self.assertRaisesRegex(RuntimeError,'set changed'):roles.validate_sources([str(home)],[review])

 def test_fifo_and_symlink_source_root_refused(self):
  home=self.root/'home';home.mkdir();os.mkfifo(home/'pipe')
  with self.assertRaisesRegex(RuntimeError,'special file'):roles.validate_sources([str(home)])
  link=self.root/'link';link.symlink_to(home,target_is_directory=True)
  with self.assertRaisesRegex(RuntimeError,'canonical'):roles.validate_sources([str(link)])

 def test_crc_warning_on_stderr_exit_zero_refused(self):
  good=b'Database cluster state:               shut down\n'
  roles.clean_control(types.SimpleNamespace(returncode=0,stdout=good,stderr=b''))
  for p in [types.SimpleNamespace(returncode=0,stdout=good,stderr=b'WARNING: CRC mismatch'),types.SimpleNamespace(returncode=0,stdout=good.replace(b'shut down',b'in production'),stderr=b''),types.SimpleNamespace(returncode=1,stdout=good,stderr=b'')]:
   with self.assertRaises(RuntimeError):roles.clean_control(p)

 def test_route_checks_use_verified_tls_and_refuse_one_open_route(self):
  calls=[]
  def fake(args,**kw):
   calls.append(args)
   return b'200' if args[-1]=='https://baarcha.tn/' else b'503'
  with mock.patch.object(roles,'run',side_effect=fake):roles.route_fence(self.config)
  self.assertEqual(len(calls),6)
  self.assertTrue(all('--resolve' in c and '--insecure' not in c and '-k' not in c for c in calls))
  with mock.patch.object(roles,'run',return_value=b'200'),self.assertRaises(RuntimeError):roles.route_fence(self.config)

 def test_archive_warning_retains_partial_and_never_publishes(self):
  source=self.root/'owner data';source.mkdir();(source/'-name\nfile').write_bytes(b'latest')
  target=self.root/'archive'
  def fake(args,stdout,stderr,**kw):
   self.assertIn('--null',args);self.assertIn('--verbatim-files-from',args);self.assertNotIn('--dereference',args)
   self.assertIn('preexec_fn',kw);stdout.write(b'partial');stderr.write(b'warning');return types.SimpleNamespace(returncode=0)
  with mock.patch.object(roles.subprocess,'run',side_effect=fake),self.assertRaisesRegex(RuntimeError,'warning'):roles.archive([str(source)],target,max_bytes=1024)
  self.assertFalse(target.exists());self.assertTrue((self.root/'archive.INCOMPLETE').exists())
  self.assertEqual((self.root/'archive.inputs.nul').read_bytes(),str(source).lstrip('/').encode()+b'\0')

 def test_archive_cannot_recursively_include_its_output(self):
  with self.assertRaisesRegex(RuntimeError,'output directory'):roles.archive([str(self.root)],self.root/'archive',max_bytes=1024)
  self.assertFalse((self.root/'archive.INCOMPLETE').exists())

 def test_closed_archive_hash_and_no_overwrite(self):
  source=self.root/'data';source.write_bytes(b'fixture')
  target=self.root/'archive'
  def fake(args,stdout,stderr,**kw):stdout.write(b'closed-fixture');return types.SimpleNamespace(returncode=0)
  with mock.patch.object(roles.subprocess,'run',side_effect=fake):result=roles.archive([str(source)],target,max_bytes=1024)
  self.assertEqual(result['sha256'],hashlib.sha256(b'closed-fixture').hexdigest())
  with mock.patch.object(roles.subprocess,'run',side_effect=fake),self.assertRaises(FileExistsError):roles.archive([str(source)],target,max_bytes=1024)
  self.assertEqual(target.read_bytes(),b'closed-fixture')

 def test_space_reserved_for_subsequent_fullpair_and_role_copy(self):
  with mock.patch.object(roles.shutil,'disk_usage',return_value=types.SimpleNamespace(free=self.config['minimum_free_bytes'])):
   self.assertEqual(roles.remaining_budget(self.config,{},self.root),1024)
  with mock.patch.object(roles.shutil,'disk_usage',return_value=types.SimpleNamespace(free=roles.PAIR_RESERVE)):
   with self.assertRaisesRegex(RuntimeError,'reserve'):roles.remaining_budget(self.config,{},self.root)

 def test_subprocess_inherits_backup_locks(self):
  lock=self.root/'lock';fd=os.open(lock,os.O_RDWR|os.O_CREAT,0o600)
  self.addCleanup(os.close,fd)
  with mock.patch.object(roles,'CAPTURE_FDS',(fd,)):
   result=roles.run([sys.executable,'-c','import os,sys;print(os.fstat(int(sys.argv[1])).st_ino)',str(fd)])
  self.assertEqual(int(result),os.fstat(fd).st_ino)

 def test_observation_requires_stopped_exact_sources_and_closed_database(self):
  database=self.root/'state.sqlite'
  with contextlib.closing(sqlite3.connect(database)) as dest:self.db.backup(dest)
  env=['SANDBOXD_ENV_FILE='+str(roles.PREVIEW_KEY)]
  config=dict(self.config,controller_id='controller',controller_image='image',controller_env_sha256=hashlib.sha256(roles.canonical(env)).hexdigest(),docker_homes=[{'sandbox_id':'source','container_id':'full-id','image':'source-image','source':str(self.root/'home')}],reviewed_caddy_sha256=hashlib.sha256(b'{}').hexdigest())
  controller={'Id':'controller','Image':'image','Name':'/src-sandboxd-1','Config':{'Env':env},'State':{'Running':False,'Pid':0},'HostConfig':{'RestartPolicy':{'Name':'no'}}}
  source={'Id':'full-id','Image':'source-image','State':{'Running':False,'Pid':0},'Mounts':[{'Destination':'/home/sandbox','Type':'bind','Source':str(self.root/'home')} ]}
  def command(args,**kw):
   if args[:2]==['docker','inspect']:return json.dumps([controller if x=='controller' else source for x in args[2:]]).encode()
   if args[:2]==['docker','ps']:return b''
   if args[0]=='systemctl':return b'LoadState=loaded\nActiveState=inactive\n'
   raise AssertionError(args)
  with mock.patch.object(roles,'DB',database),mock.patch.object(roles,'verify_inputs'),mock.patch.object(roles.cold_pair,'no_open_users') as users,mock.patch.object(roles,'route_fence'),mock.patch.object(roles,'run',side_effect=command),mock.patch.object(roles.urllib.request,'urlopen',side_effect=lambda *a,**k:contextlib.closing(io.BytesIO(b'{}'))):
   roles.observe(config,True)
   self.assertEqual(users.call_count,1)
   source['State']={'Running':True,'Pid':123}
   with self.assertRaisesRegex(RuntimeError,'writer is still running'):roles.observe(config,True)
   source['State']={'Running':False,'Pid':0};source['Mounts'][0]['Source']='/changed-home'
   with self.assertRaisesRegex(RuntimeError,'home mapping changed'):roles.observe(config,True)
   source['Mounts'][0]['Source']=str(self.root/'home');controller['HostConfig']['RestartPolicy']['Name']='always'
   with self.assertRaisesRegex(RuntimeError,'restart disabled'):roles.observe(config,True)
   controller['HostConfig']['RestartPolicy']['Name']='no';controller['Config']['Env'].append('OTHER=drift')
   with self.assertRaisesRegex(RuntimeError,'environment changed'):roles.observe(config,True)

if __name__=='__main__':unittest.main()
