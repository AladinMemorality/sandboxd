"""Root-only isolated check: two children retain all four maintenance locks."""
import fcntl,importlib.util,json,os,pathlib,subprocess,tempfile
assert os.geteuid()==0
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with tempfile.TemporaryDirectory(prefix='baarcha-inherited-lock-test-') as folder:
 b.LOCKS=tuple(str(pathlib.Path(folder)/str(n)) for n in range(4))
 children=[]
 code=r"""import importlib.util,json,os,sys
s=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(s);s.loader.exec_module(b)
b.LOCKS=tuple(json.loads(os.environ['TEST_LOCK_PATHS']))
with b.locked(json.loads(os.environ['TEST_LOCK_FDS'])):
 print('held-after-exec' if os.environ.get('TEST_EXECUTED') else 'held',flush=True)
 if sys.stdin.readline()=='exec\n':
  for fd in json.loads(os.environ['TEST_LOCK_FDS']):os.set_inheritable(fd,True)
  os.execve('/usr/bin/python3',['/usr/bin/python3','-c',os.environ['TEST_PROGRAM']],{**os.environ,'TEST_EXECUTED':'1'})
"""
 def blocked():
  for path in b.LOCKS:
   with open(path,'r') as f:
    try:fcntl.flock(f,fcntl.LOCK_SH|fcntl.LOCK_NB)
    except BlockingIOError:continue
    raise AssertionError('maintenance ownership was lost')
 try:
  with b.locked() as locks:
   for _ in range(2):
    child=subprocess.Popen(['/usr/bin/python3','-c',code],env={**os.environ,'TEST_PROGRAM':code,'TEST_LOCK_PATHS':json.dumps(b.LOCKS),'TEST_LOCK_FDS':json.dumps(locks)},pass_fds=locks,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    children.append(child);assert child.stdout.readline()==b'held\n'
   children[0].stdin.write(b'exec\n');children[0].stdin.flush();assert children[0].stdout.readline()==b'held-after-exec\n'
   blocked()
  blocked() # Parent exited the context; both children still hold the same OFDs.
  children[0].stdin.close();assert children[0].wait(timeout=10)==0;blocked()
  children[1].stdin.close();assert children[1].wait(timeout=10)==0
  for path in b.LOCKS:
   with open(path,'r') as f:fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
 finally:
  for child in children:
   if child.poll() is None:child.kill();child.wait()
print(json.dumps({'passed':True,'children':2,'locks':4,'parent_exit_did_not_release_child_locks':True,'child_exec_preserved_locks':True}))
