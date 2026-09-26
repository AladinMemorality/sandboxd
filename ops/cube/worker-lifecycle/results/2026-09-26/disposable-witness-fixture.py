import hashlib,importlib.util,json,os,pathlib,subprocess,time
ROOT=pathlib.Path('/opt/baarcha-bench/cube-wait-witness-20260926-01')
os.umask(0o077)
spec=importlib.util.spec_from_file_location('external_clean',ROOT/'external_clean.py');x=importlib.util.module_from_spec(spec);spec.loader.exec_module(x)
parent_code=r'''
import json,os,pathlib,subprocess,sys,time
out=pathlib.Path(sys.argv[1]);child=subprocess.Popen(['/usr/bin/sleep','3'])
start=lambda pid:pathlib.Path('/proc/'+str(pid)+'/stat').read_text().rsplit(')',1)[1].split()[19]
value={'supervisor_pid':os.getpid(),'supervisor_start_time':start(os.getpid()),'qemu_pid':child.pid,'qemu_start_time':start(child.pid),'outer_boot_id':pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()}
with out.open('x') as file:json.dump(value,file);file.flush();os.fsync(file.fileno())
while child.poll() is None:time.sleep(.05)
raise SystemExit(1 if child.returncode==0 else 2)
'''
rows=[]
for mode in ('normal-exit','detach-while-alive'):
 d=ROOT/mode;d.mkdir(mode=0o700);identity_path=d/'identity.json'
 parent=subprocess.Popen(['/usr/bin/python3','-c',parent_code,str(identity_path)],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 witness=None
 try:
  deadline=time.monotonic()+5
  while not identity_path.exists():
   assert parent.poll() is None and time.monotonic()<deadline;time.sleep(.02)
  identity=json.loads(identity_path.read_bytes());assert identity['supervisor_pid']==parent.pid
  witness=x.WaitWitness(identity,d);witness.attach()
  raw=(d/'wait4.trace').read_bytes();ready=x.parse_wait4(x.complete_trace(raw),parent.pid,identity['qemu_pid']);assert ready['polls'] and not ready['exits']
  if mode=='detach-while-alive':
   witness.close();assert parent.poll() is None;x.WaitWitness(identity,d).same()
   row={'mode':mode,'attached_ready':True,'only_owned_tracer_detached':True,'both_original_processes_alive_after_detach':True}
  else:
   assert parent.wait(timeout=6)==1
   assert witness.process.wait(timeout=5)==0
   raw=(d/'wait4.trace').read_bytes();parsed=x.parse_wait4(raw,parent.pid,identity['qemu_pid']);assert len(parsed['exits'])==1
   row={'mode':mode,'attached_ready':True,'exact_child_exit0_observed':True,'parent_actual_exit1_preserved':True,'trace_sha256':hashlib.sha256(raw).hexdigest()}
  assert parent.wait(timeout=6)==1
  assert not pathlib.Path('/proc/'+str(identity['qemu_pid'])).exists()
  row['cleanup_natural_exit_verified']=True;rows.append(row)
 finally:
  if witness:witness.close()
  parent.wait(timeout=10)
result={'version':1,'scope':'Owned disposable Python parent and sleep child only; no VM, worker, services or network changes','real_guest_os_shutdown_tested':False,'external_clean_receipt_created':False,'source_sha256':hashlib.sha256((ROOT/'external_clean.py').read_bytes()).hexdigest(),'cases':rows}
(ROOT/'result.json').write_text(json.dumps(result,sort_keys=True,indent=2)+'\n');print(json.dumps(result))
