"""Verify only the fixed synthetic multipart fixture; no VM or service actions."""
import contextlib,hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,time
P=pathlib.Path('/opt/baarcha-cube/backup-generations/transport-fixture-20260925-01');R=P.with_name(P.name+'-restored');D=P.with_name(P.name+'-plaintext')
spec=importlib.util.spec_from_file_location('cold','/opt/baarcha-cube/backup-tools/cold_pair.py');cold=importlib.util.module_from_spec(spec);spec.loader.exec_module(cold)
manifest=cold.validate_capture(R);original=cold.validate_capture(P/'synthetic-capture');assert manifest==original
for name in ['root.qcow2','data.qcow2']:
 subprocess.run(['qemu-img','compare','-f','qcow2','-F','qcow2',str(P/'synthetic-capture'/name),str(R/name)],check=True,capture_output=True,timeout=30)
with contextlib.closing(sqlite3.connect('file:'+str(R/'controller.sqlite')+'?mode=ro',uri=True)) as db:assert db.execute('SELECT note FROM synthetic_transport_fixture').fetchone()[0]=='SYNTHETIC; no customer or running worker content'
phases=[json.loads(f.read_text()) for f in sorted((P/'readback').glob('phase-*.json'))]
parts=[p for p in phases if p['phase']=='part-acknowledged'];assert len(parts)==2 and [p['part'] for p in parts]==[1,2]
assert phases[-1]['phase']=='readback-verified' and any(p['phase']=='completed' for p in phases)
seal=json.loads((P/'synthetic.tar.gpg.manifest.json').read_text());copy=json.loads((P/'readback/copy-evidence.json').read_text());transport=json.loads((D/'transport.json').read_text())
assert copy['full_object_get_hash_verified'] and seal['ciphertext_sha256']==copy['ciphertext_sha256']==transport['ciphertext_sha256'] and transport['gpg_exit_zero_verified_by_offhost_operator']
report={'scope':'SYNTHETIC multipart and off-host GPG transport integration; no production worker capture or application boot','verified_at':time.time(),'recipient_fingerprint':seal['recipient_fingerprint'],'ciphertext_bytes':copy['bytes'],'ciphertext_sha256':copy['ciphertext_sha256'],'plaintext_bytes':transport['bytes'],'actual_multipart_parts':2,'part_limit_bytes':128*1024**2,'multipart_complete_acknowledged':True,'full_object_get_hash_verified':True,'offhost_gpg_exit_zero':True,'private_key_uploaded':False,'local_full_archive_stored':False,'strict_restore_all_file_hashes_match':True,'qcow_pair_compare_passed':True,'sqlite_fixture_row_preserved':True,'production_worker_captured':False,'application_restore_verified':False,'services_changed':False,'object_retained':True,'artifacts_sha256':{n:cold.digest(f) for n,f in [('seal',P/'synthetic.tar.gpg.manifest.json'),('readback',P/'readback/copy-evidence.json'),('transport',D/'transport.json'),('capture',P/'synthetic-capture/manifest.json')]}}
with os.fdopen(os.open(P/'verified-result.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600),'w') as f:json.dump(report,f,indent=2);f.flush();os.fsync(f.fileno())
print(json.dumps(report))
