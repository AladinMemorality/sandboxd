#!/usr/bin/env python3
"""One explicitly reviewed coding profile; no automatic task replay or cleanup."""
import hashlib,importlib.util,json,os,pathlib,signal,subprocess,sys,time
P=pathlib.Path

def main():
    if sys.platform!='linux' or os.getuid()!=0:raise ValueError('Linux root required')
    if len(sys.argv) not in (2,3) or len(sys.argv)==3 and sys.argv[2]!='--check':raise ValueError('Unexpected arguments')
    cfg_path=P(sys.argv[1]);s=cfg_path.lstat()
    if cfg_path.resolve()!=cfg_path or s.st_uid!=0 or s.st_mode&0o777!=0o600:raise ValueError('Private reviewed config required')
    cfg=json.loads(cfg_path.read_text())
    wrapper=P(cfg['lock_wrapper']);helper=P(cfg['lock_helper'])
    for p,key in [(wrapper,'lock_wrapper_sha256'),(helper,'lock_helper_sha256'),(P(__file__).with_name('task.mjs'),'task_sha256')]:
        if p.resolve()!=p or hashlib.sha256(p.read_bytes()).hexdigest()!=cfg[key]:raise ValueError('Reviewed source changed')
    def module(name,p):
        spec=importlib.util.spec_from_file_location(name,p);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
    lock=module('coding_lock',wrapper);nested_module=module('coding_nested',helper)
    fds=lock.acquire();nested=None;child=None
    for sig in [signal.SIGINT,signal.SIGTERM,signal.SIGHUP]:signal.signal(sig,lambda *_:None)
    try:
        nested=nested_module.NestedLock(cfg['worker_boot_id'])
        child=subprocess.Popen(['/opt/baarcha/node22/bin/node','--env-file=/opt/baarcha/landing.env',str(P(__file__).with_name('task.mjs')),str(cfg_path),*sys.argv[2:]],env=dict(os.environ,CUBE_CODING_PROFILE_LOCKED_PARENT=str(os.getpid())))
        until=time.monotonic()+750
        while child.poll() is None:
            nested.heartbeat()
            if time.monotonic()>until:raise RuntimeError('Coordinator deadline; preserve task journal')
            time.sleep(1)
        if child.returncode:raise RuntimeError('Profile task refused or failed')
    finally:
        if child is not None and child.poll() is None:
            child.terminate()
            # Keep fencing until this exact mutation-capable child has exited.
            child.wait()
        if nested is not None:nested.close()
        for fd in fds:os.close(fd)

if __name__=='__main__':
    try:main()
    except Exception:
        print('Coding profile refused or failed; retain private journal. No automatic replay.',file=sys.stderr);sys.exit(1)
