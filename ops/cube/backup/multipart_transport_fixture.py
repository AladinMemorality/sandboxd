"""Explicit synthetic transport fixture; never captures the production worker.

The role files below are synthetic inputs, not production recovery artifacts.
This fixed-path one-shot script was executed on 2026-09-25; it refuses reuse.
"""
import contextlib,hashlib,importlib.util,json,os,pathlib,sqlite3,subprocess,time
os.umask(0o077)
P=pathlib.Path('/opt/baarcha-cube/backup-generations/transport-fixture-20260925-01')
assert os.geteuid()==0 and not P.exists()
P.mkdir(mode=0o700);C=P/'synthetic-capture';C.mkdir(mode=0o700)
spec=importlib.util.spec_from_file_location('cold','/opt/baarcha-cube/backup-tools/cold_pair.py');cold=importlib.util.module_from_spec(spec);spec.loader.exec_module(cold)
def run(argv):return subprocess.check_output(argv,stderr=subprocess.PIPE,text=True,timeout=300).strip()
for name in ['root.qcow2','data.qcow2']:
 run(['qemu-img','create','-f','qcow2',str(C/name),'32M'])
 run(['qemu-io','-f','qcow2','-c','write -P 0x71 0 65536',str(C/name)])
with contextlib.closing(sqlite3.connect(C/'controller.sqlite')) as db:
 db.execute('CREATE TABLE synthetic_transport_fixture(note TEXT)');db.execute('INSERT INTO synthetic_transport_fixture VALUES (?)',('SYNTHETIC; no customer or running worker content',));db.commit()
for role in cold.ROLES:(C/role).write_bytes(('SYNTHETIC multipart transport only: '+role+'\n').encode())
# Incompressible owned bytes guarantee more than one actual 128-MiB S3 part.
with (C/'rollback').open('ab') as f:
 for _ in range(129):f.write(os.urandom(1024*1024))
 f.flush();os.fsync(f.fileno())
for f in C.iterdir():f.chmod(0o600)
cold.write_manifest(C,time.time());cold.validate_capture(C)
cold.seal(type('Args',(),{'capture':C,'recipient_key':pathlib.Path('/opt/baarcha-cube/recovery-recipient-20260925/recipient.asc'),'fingerprint':'25017F865BE80AA7E7E8C8925C595C2637931730','output':P/'synthetic.tar.gpg'})())
sealed=json.loads((P/'synthetic.tar.gpg.manifest.json').read_text());assert sealed['file_identity']['bytes']>128*1024*1024
cfg=json.loads(pathlib.Path('/opt/baarcha-bench/cube-paired-backup-review-20260925-02/upload.PREPARED-NOT-EXECUTED.json').read_text())
cfg.update(manifest=str(P/'synthetic.tar.gpg.manifest.json'),output=str(P/'readback'),prefix=cfg['prefix']+'/transport-fixture-20260925',readback_only=False)
for name,val in [('upload.json',cfg),('scope.json',{'synthetic_only':True,'production_worker_captured':False,'services_changed':False,'full_worker_restore_proven':False,'purpose':'Real multipart permissions, complete readback, off-host GPG pipe integration','ciphertext_sha256':sealed['ciphertext_sha256'],'ciphertext_bytes':sealed['file_identity']['bytes'],'capture_manifest_sha256':sealed['capture_manifest_sha256']})]:
 with (P/name).open('x') as f:json.dump(val,f,indent=2);f.flush();os.fsync(f.fileno())
print(json.dumps({'synthetic_ciphertext_ready':True,'bytes':sealed['file_identity']['bytes'],'multipart_parts':2,'production_worker_captured':False}))
