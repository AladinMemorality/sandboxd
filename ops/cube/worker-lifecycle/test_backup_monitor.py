import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest import mock

spec=importlib.util.spec_from_file_location('backup_monitor',Path(__file__).with_name('backup_monitor.py'))
monitor=importlib.util.module_from_spec(spec);spec.loader.exec_module(monitor)
class BackupMonitorTests(unittest.TestCase):
 def setUp(self):
  self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup);self.root=Path(self.temp.name).resolve();self.archive=self.root/'backup.gpg';self.archive.write_bytes(b'encrypted-fixture');self.archive.chmod(0o600)
  for name in ['copy-proof','restore-proof']:(self.root/name).write_bytes(name.encode())
  info=self.archive.stat();self.info=info
  self.manifest={'version':1,'kind':'encrypted-cold-cube-pair','captured_at':900,'sealed_at':910,'ciphertext_path':str(self.archive),'ciphertext_sha256':'a'*64,'capture_manifest_sha256':'b'*64,'recipient_fingerprint':'A'*40,'file_identity':{'device':info.st_dev,'inode':info.st_ino,'bytes':info.st_size,'mtime_ns':info.st_mtime_ns,'ctime_ns':info.st_ctime_ns}}
  self.documents={'manifest':self.manifest}
  self.policy={'version':1,'max_backup_age_seconds':200,'max_restore_age_seconds':200,'recipient_fingerprint':'A'*40,'encrypted_manifest':'manifest','offhost_receipt':'copy','restore_receipt':'restore','offhost_evidence':str(self.root/'copy-proof'),'restore_evidence':str(self.root/'restore-proof')}
  for name in ['copy','restore']:
   self.documents[name]={'version':1,'manifest_sha256':self.digest('manifest'),'ciphertext_sha256':'a'*64,'evidence_sha256':self.digest(str(self.root/(name+'-proof'))),'verified_at':950,'kind':'offhost-copy' if name=='copy' else 'application-restore'}
  self.documents['copy'].update({'verified_bytes':info.st_size,'offhost':True,'destination_id':'fixture-independent-storage'})
  self.documents['restore'].update({key:True for key in ['decryption_verified','disk_integrity_verified','bindings_verified','application_data_verified','task_history_verified','database_data_verified']})
 def digest(self,path):
  raw=json.dumps(self.documents[path],sort_keys=True).encode() if path in self.documents else Path(path).read_bytes()
  return hashlib.sha256(raw).hexdigest()
 def check(self,now=1000):
  original=Path.stat
  def stat(path,*args,**kwargs):
   value=original(path,*args,**kwargs)
   if path==self.archive:return types.SimpleNamespace(st_dev=value.st_dev,st_ino=value.st_ino,st_size=value.st_size,st_mtime_ns=value.st_mtime_ns,st_ctime_ns=value.st_ctime_ns,st_uid=0,st_mode=value.st_mode)
   return value
  with mock.patch.object(Path,'stat',stat):return monitor.check(self.policy,lambda path:self.documents[path],self.digest,now)
 def test_current_exact_backup_and_restore_receipts(self):self.assertTrue(self.check()['healthy'])
 def test_stale_future_and_false_restore_refused(self):
  for now in [800,1200]:
   with self.assertRaises(RuntimeError):self.check(now)
  self.documents['restore']['task_history_verified']=False
  with self.assertRaises(RuntimeError):self.check()
 def test_unrelated_manifest_or_ciphertext_receipt_refused(self):
  for key in ['manifest_sha256','ciphertext_sha256','evidence_sha256']:
   previous=self.documents['copy'][key];self.documents['copy'][key]='0'*64
   with self.assertRaises(RuntimeError):self.check()
   self.documents['copy'][key]=previous
 def test_ciphertext_change_and_missing_external_evidence_refused(self):
  self.archive.write_bytes(b'changed')
  with self.assertRaises(RuntimeError):self.check()
 def test_absent_restore_or_local_only_copy_refused(self):
  self.documents['copy']['offhost']=False
  with self.assertRaises(RuntimeError):self.check()
 def test_modified_recipient_refused(self):
  self.policy['recipient_fingerprint']='B'*40
  with self.assertRaises(RuntimeError):self.check()

 def test_old_capture_resealed_now_with_fresh_copy_and_restore_is_stale(self):
  self.manifest['captured_at']=100
  self.manifest['sealed_at']=990
  for name in ['copy','restore']:
   self.documents[name]['manifest_sha256']=self.digest('manifest')
   self.documents[name]['verified_at']=995
  with self.assertRaises(RuntimeError):self.check(1000)
 def test_legacy_encryption_time_without_capture_time_is_unproven(self):
  del self.manifest['captured_at'];self.manifest['created_at']=999
  with self.assertRaises(RuntimeError):self.check()
