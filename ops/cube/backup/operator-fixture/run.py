#!/usr/bin/env python3
"""Prepared source only; root reviews config/code before each operator phase."""
import argparse,hashlib,importlib.util,json,os,pathlib,signal,stat,subprocess,sys,time
P=pathlib.Path

def module(name,path):
 spec=importlib.util.spec_from_file_location(name,path);result=importlib.util.module_from_spec(spec);spec.loader.exec_module(result);return result

def reviewed_helper(raw,uid=0):
 p=P(raw);s=p.lstat()
 if not p.is_absolute() or p.resolve(strict=True)!=p or not stat.S_ISREG(s.st_mode) or s.st_uid!=uid or s.st_nlink!=1 or s.st_mode&0o022:raise ValueError('Unsafe lock helper')
 return p

def main():
 p=argparse.ArgumentParser();p.add_argument('--config',required=True);p.add_argument('--action',choices=['prepare','install','restoreManifest','verify','inspect'],required=True);a=p.parse_args()
 if sys.platform!='linux' or os.getuid()!=0:raise ValueError('Native Linux root required')
 base=P(__file__).resolve().parent
 locks=module('operator_fixture_locks',base.parent/'canonical-fixture/run.py');cfg=json.loads(locks.private(a.config).read_text())
 if locks.sha(base.parent/'canonical-fixture/run.py')!=cfg['canonical_wrapper_sha256']:raise ValueError('Canonical wrapper changed')
 for filename in ('main.mjs','fixture.mjs','home-worker.mjs','run.py'):
  if hashlib.sha256((base/filename).read_bytes()).hexdigest()!=cfg['source_sha256'][filename]:raise ValueError('Reviewed source hash changed')
 helper=P(cfg['enrollment_runner']);reviewed_helper(helper)
 if locks.sha(helper)!=cfg['enrollment_runner_sha256']:raise ValueError('Lock helper changed')
 handles=locks.acquire();nested=None;child=None
 for sig in (signal.SIGINT,signal.SIGTERM,signal.SIGHUP):signal.signal(sig,lambda *_:print('Signal noted; retaining fixture locks until bounded child exits.',file=sys.stderr))
 try:
  nested=module('operator_nested_lock',helper).NestedLock(cfg['worker_boot_id'])
  child=subprocess.Popen(['/opt/baarcha/node22/bin/node','--env-file=/opt/baarcha/landing.env',str(base/'main.mjs'),a.config,a.action],env=dict(os.environ,CUBE_OPERATOR_RECOVERY_PARENT=str(os.getpid())),pass_fds=tuple(handles))
  deadline=time.monotonic()+600
  while child.poll() is None:
   nested.heartbeat()
   if time.monotonic()>deadline:raise RuntimeError('Fixture deadline; inspect retained mutation intent')
   time.sleep(1)
  if child.returncode:raise RuntimeError('Fixture refused')
 finally:
  if child is not None and child.poll() is None:
   child.terminate()
   while child.poll() is None:time.sleep(1)
  if nested is not None:nested.close()
  for fd in handles:os.close(fd)

if __name__=='__main__':
 try:main()
 except Exception:print('Operator recovery phase refused; no automatic repair or cleanup.',file=sys.stderr);sys.exit(1)
