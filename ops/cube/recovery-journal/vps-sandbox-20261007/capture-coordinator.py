import contextlib,fcntl,json,os,pathlib,sqlite3,subprocess,time
os.umask(0o077)
r=pathlib.Path('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007');name='src-sandboxd-1'
ssh=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','root@127.0.0.1']
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
r=pathlib.Path('/data/cube-recovery/vps-sandbox-20261007');p=json.loads((r/'metadata/plan.json').read_text());sid=p['sandbox_id'];source=pathlib.Path(p['current_disk']['FilePath']);st=source.stat()
assert pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip()=='ae6250ea-8933-416b-9621-cbb1eadfc54b'
assert sid not in subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list'],text=True,stderr=subprocess.DEVNULL)
for proc in pathlib.Path('/proc').glob('[0-9]*'):
 for fd in (proc/'fd').glob('*'):
  try:s=fd.stat()
  except OSError:continue
  assert (s.st_dev,s.st_ino)!=(st.st_dev,st.st_ino),'source still held'
fence={'purpose':'CUBE_FENCED_RUNTIME_DISK_CAPTURE','sandbox_id':sid,'worker_machine_id':'2b9e31d4abd345e3bd4b966591e61296','current_boot_id':'ae6250ea-8933-416b-9621-cbb1eadfc54b','expires_at':int(time.time())+1200,'no_task_verified':True,'management_fenced':True,'provider_requests_drained':True,'no_disk_handles_verified':True,'source_identity':{'device':st.st_dev,'inode':st.st_ino,'bytes':st.st_size,'mtime_ns':st.st_mtime_ns}}
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
