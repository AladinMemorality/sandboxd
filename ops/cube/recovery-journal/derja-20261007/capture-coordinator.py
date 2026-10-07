import contextlib,fcntl,json,os,pathlib,sqlite3,subprocess,time
os.umask(0o077)
r=pathlib.Path('/opt/baarcha/operations/derja-disk-recovery-20261007');name='src-sandboxd-1'
ssh=['ssh','-o','ControlPath='+str(r/'worker.sock'),'-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(r/'worker-known-hosts'),'-i',str(r/'worker-key'),'root@10.254.240.2']
def run(args,**kw):return subprocess.run(args,check=True,**kw)
with contextlib.ExitStack() as stack:
 for path in ['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock','/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']:
  f=stack.enter_context(open(path,'a'));fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
 x=json.loads(subprocess.check_output(['docker','inspect',name]))[0]
 assert x['State']['Running'] and x['HostConfig']['RestartPolicy']['Name']=='unless-stopped'
 c=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)
 assert c.execute("SELECT count(*) FROM task WHERE status IN ('running','queued')").fetchone()[0]==0
 assert c.execute("SELECT count(*) FROM cube_recovery WHERE phase<>'complete'").fetchone()[0]==0
 c.close()
 run(['docker','update','--restart=no',name],stdout=subprocess.DEVNULL)
 try:
  run(['docker','stop','--time','45',name],stdout=subprocess.DEVNULL,timeout=70)
  code='''import pathlib,json,subprocess,time,os
r=pathlib.Path('/data/cube-recovery/derja-20261007');p=json.loads((r/'metadata/plan.json').read_text());sid=p['sandbox_id'];source=pathlib.Path(p['current_disk']['FilePath']);st=source.stat()
assert pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()=='1c920b4d-873c-48a0-8ae3-12d3b990c68d'
assert sid not in subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list'],text=True,stderr=subprocess.DEVNULL)
for proc in pathlib.Path('/proc').glob('[0-9]*'):
 for fd in (proc/'fd').glob('*'):
  try:s=fd.stat()
  except OSError:continue
  assert (s.st_dev,s.st_ino)!=(st.st_dev,st.st_ino),'source still held'
fence={'purpose':'CUBE_CURRENT_DISK_CAPTURE','sandbox_id':sid,'worker_machine_id':'f88701cbc32c43ed87aef23892bd3f9c','previous_boot_id':'f9861e4a-d01b-4237-8cad-0a59fbc497a8','current_boot_id':'1c920b4d-873c-48a0-8ae3-12d3b990c68d','expires_at':int(time.time())+1200,'no_task_verified':True,'management_fenced':True}
os.umask(0o077);(r/'fence.json').write_text(json.dumps(fence))
subprocess.run(['nice','-n','10','python3',str(r/'tools/capture.py'),'--plan',str(r/'metadata/plan.json'),'--fence',str(r/'fence.json'),'--output',str(r/'capture')],check=True,timeout=900)
print('current disk independently captured; original retained')
'''
  run(ssh+['python3 -'],input=code.encode(),timeout=950)
 finally:
  run(['docker','start',name],stdout=subprocess.DEVNULL)
  run(['docker','update','--restart=unless-stopped',name],stdout=subprocess.DEVNULL)
  run(['docker','compose','--project-directory','/opt/sandboxd/src','-f','/opt/sandboxd/src/docker-compose.yml','-f','/opt/sandboxd/deploy-state/runtime-compose.json','-f','/opt/sandboxd/deploy-state/active-images.json','up','-d','--no-deps','--force-recreate','cube-management-api','cube-management-proxy','cube-management-master','cube-management-b200-proxy'],stdout=subprocess.DEVNULL)
 print('controller and management relays restored')
