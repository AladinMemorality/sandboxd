#!/usr/bin/env python3
"""One reviewed binary/configuration upgrade. Default is a read-only check."""
import argparse,contextlib,fcntl,hashlib,json,os,pathlib,sqlite3,stat,subprocess
P=pathlib.Path
BASE=P('/opt/baarcha-bench/cube-storage-coordinators-20260925')
REVIEW=P('/opt/baarcha-bench/cube-storage-enrollment-reviewed-20260925-01')
TARGET=P('/opt/baarcha-cube/coordinator-storage-guard-20260925')
BACKUP=REVIEW/'coordinator-upgrade-01'
CP='54f9d073a7c2739b55481d5bbbb80f22847cb1e1d3225fe5af9590f2f48de5f3'
OLD={'/usr/local/libexec/baarcha-cube-worker-stop':'ab55a5dda0bfceb6053a1870c7c73413ce2902e53e4c5ea2d2e0f903d20c469f','/usr/local/libexec/baarcha-cube-worker-start':'e3752688e978540c6dbcee2c7d8786fb24e3d409efaed8a2d880d1bc6c85cfb9','/etc/baarcha-cube/worker-stop.json':'d7f080d7a698eced83af3ad99d2f4c037b94c3eb02cb7bab4f63949986115f6d'}
UNTOUCHED={'/usr/local/libexec/baarcha-cube-worker-lifecycle.py':'94398ad7ebd42af9288c705924e2bbf5c4214561582afeef54d55f32714dbffd','/etc/baarcha-cube/lifecycle.json':'8ba3f512b8c3af5edc4924a02f23da7057d6080c04a87fdd6d63d46ef9fc9e60','/etc/baarcha-cube/worker-start.json':'8b45e751cc8d4b0b6b33a05f9de5b8bdeb2de84f6e3dc439729a51255676bce4'}
def need(v,m):
 if not v:raise RuntimeError(m)
def sha(p):return hashlib.sha256(P(p).read_bytes()).hexdigest()
def safe(p):
 for x in [p,*p.parents]:
  s=x.lstat();sticky=x==P('/run/lock') and stat.S_ISDIR(s.st_mode) and s.st_mode&0o7777==0o1777
  need(not stat.S_ISLNK(s.st_mode) and s.st_uid==0 and (not s.st_mode&0o022 or sticky),'unsafe operator path')
def syncdir(p):
 fd=os.open(p,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
 try:os.fsync(fd)
 finally:os.close(fd)
def write(p,raw,mode):
 fd=os.open(p,os.O_CREAT|os.O_EXCL|os.O_WRONLY|os.O_NOFOLLOW,mode)
 with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
 syncdir(p.parent)
def replace(p,raw,mode):
 tmp=p.with_name('.'+p.name+'.storage34-pending')
 write(tmp,raw,mode);os.replace(tmp,p);syncdir(p.parent)
def main():
 parser=argparse.ArgumentParser();parser.add_argument('--install',action='store_true');args=parser.parse_args()
 os.umask(0o077);need(os.geteuid()==0,'root required')
 with contextlib.ExitStack() as locks:
  for name in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
   p=P(name);safe(p);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);locks.callback(os.close,fd);fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  need(not TARGET.exists() and not TARGET.is_symlink() and not BACKUP.exists(),'new upgrade and backup directories required')
  safe(TARGET.parent);safe(BACKUP.parent)
  for path,digest in {**OLD,**UNTOUCHED}.items():safe(P(path));need(sha(path)==digest,'installed artifact drift')
  need(not P('/var/lib/sandboxd/state/sandboxd.db.worker-stop.json').exists(),'stop operation in progress')
  cp=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];env=dict(x.split('=',1) for x in cp['Config']['Env'] if '=' in x)
  need(cp['Id']==CP and cp['State']['Running'] and env.get('SANDBOXD_CUBE_ENABLED')=='false' and env.get('SANDBOXD_CUBE_REVERSE_EGRESS')=='false','controller changed or Cube activated')
  need(P('/proc/3026420/stat').read_text().rsplit(')',1)[1].split()[19]=='483403245','QEMU changed')
  for proc in P('/proc').iterdir():
   if proc.name.isdigit():
    try:exe=proc.joinpath('exe').readlink()
    except FileNotFoundError:continue
    need(str(exe) not in OLD,'coordinator is currently executing')
  with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True,timeout=2)) as db:
   for table in ['cube_storage_policy','cube_storage_grant','runtime_binding','cube_admission']:need(db.execute('SELECT count(*) FROM '+table).fetchone()[0]==0,'canonical Cube allocation/enrollment exists')
   need(db.execute('SELECT count(*) FROM migration WHERE id=34').fetchone()[0]==1,'deployed schema34 missing')
  manifest=REVIEW/'enrollment/coordinator-build-and-migrations-2026-09-25.json';safe(manifest);need(sha(manifest)=='55c642cd516b968c35535ff9d405984b808dae18fc924f5150e51d623c679947','reviewed build manifest mismatch');m=json.loads(manifest.read_text())
  need(sha(BASE/'source.tar.gz')==m['source_archive_sha256']=='7fbc6ce9b660d91c4dc19155e703e5c926d0acbfa1f8869544a0d13b51bfd4c2','build source mismatch')
  paths={P(dst):(BASE/spec['source'],spec['sha256'],0o700) for dst,spec in m['binaries'].items()}
  need(set(map(str,paths))==set(OLD)-{'/etc/baarcha-cube/worker-stop.json'},'unexpected binary destinations')
  cfg=REVIEW/'reviewed-configs-after-runtime34/worker-stop.json'
  paths[P('/etc/baarcha-cube/worker-stop.json')]=(cfg,'94ca892e95c7ae03ae103d2936efadd83d76f87a4bfc5c627e194ccf6c8a6a2d',0o600)
  for src,digest,_ in paths.values():safe(src);need(sha(src)==digest,'candidate hash mismatch')
  c=json.loads(cfg.read_text());old=json.loads(P('/etc/baarcha-cube/worker-stop.json').read_text())
  need(c['controller_id']==CP and c['migrations']==str(TARGET/'migrations') and c['api_key']==old['api_key'],'candidate configuration identity mismatch')
  mig=BASE/'control-plane/migrations';expected=m['migration_sha256'];need(len(expected)==34 and {p.name for p in mig.iterdir()}==set(expected),'exact migration set required')
  safe(mig);migration_bytes={}
  # Build source tar retained macOS UID501 on SQL files; admit only captured,
  # bounded regular-file bytes matching the root-pinned 34-file hash manifest.
  for name,digest in expected.items():
   fd=os.open(mig/name,os.O_RDONLY|os.O_NOFOLLOW)
   with os.fdopen(fd,'rb') as f:
    st=os.fstat(f.fileno());need(stat.S_ISREG(st.st_mode) and st.st_size<1024*1024,'invalid SQL source')
    raw=f.read(1024*1024)
   need(hashlib.sha256(raw).hexdigest()==digest,'migration hash mismatch');migration_bytes[name]=raw
  for src,_,_ in list(paths.values())[:2]:subprocess.run([str(src),'--help'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5)
  if not args.install:print('Checked exact coordinator34 upgrade; no changes');return
  BACKUP.mkdir(mode=0o700)
  for path in OLD:write(BACKUP/P(path).name,P(path).read_bytes(),0o600)
  write(BACKUP/'before.json',(json.dumps({'old_sha256':OLD,'untouched_sha256':UNTOUCHED,'controller_id':CP,'canonical_guard_unenrolled':True},indent=2)+'\n').encode(),0o600)
  TARGET.mkdir(mode=0o700);(TARGET/'migrations').mkdir(mode=0o700)
  for name in sorted(expected):write(TARGET/'migrations'/name,migration_bytes[name],0o400)
  write(TARGET/'build-manifest.json',manifest.read_bytes(),0o400)
  for dst,(src,digest,mode) in paths.items():replace(dst,src.read_bytes(),mode);need(sha(dst)==digest,'installed hash mismatch')
  for path,digest in UNTOUCHED.items():need(sha(path)==digest,'unrelated lifecycle artifact changed')
  report={'version':1,'installed':True,'new_sha256':{str(p):sha(p) for p in paths},'migration_sha256':expected,'backup':str(BACKUP),'no_coordinator_invocation':True,'no_service_restart':True,'canonical_guard_enrolled':False,'controller_id':CP}
  write(BACKUP/'installed.json',(json.dumps(report,indent=2)+'\n').encode(),0o600)
  print(json.dumps({'receipt':str(BACKUP/'installed.json'),'sha256':sha(BACKUP/'installed.json')}))
if __name__=='__main__':main()
