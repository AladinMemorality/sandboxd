"""Pause the next batch item while applying the reviewed default and budget."""
import contextlib,importlib.util,json,pathlib,subprocess,time
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
barrier=root/'restore-barrier.json';assert not barrier.exists()
gate={'purpose':'reviewed-restore-barrier','allowed_sandboxes':[],'owner':'balanced-default-release'}
b.atomic(barrier,b.encoded(gate))
try:
 deadline=time.monotonic()+1800
 while True:
  stack=contextlib.ExitStack()
  try:stack.enter_context(b.locked())
  except BlockingIOError:
   stack.close();assert time.monotonic()<deadline;time.sleep(2)
  else:stack.close();break
 for script in ['native-memory-45g.py','deploy-balanced-default.py']:
  subprocess.run(['/usr/bin/python3',str(root/script)],check=True,timeout=600)
finally:
 if json.loads(barrier.read_text())==gate:barrier.unlink()
