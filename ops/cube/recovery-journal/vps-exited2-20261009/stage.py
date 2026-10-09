import json,os,pathlib,subprocess,shlex,sqlite3,hashlib,importlib.util,datetime
P=pathlib.Path;os.umask(0o077);r=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=r/'exited-recovery-02';out.mkdir(mode=0o700)
spec=importlib.util.spec_from_file_location('b','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','-oBatchMode=yes','root@127.0.0.1']
def save(p,v):p.write_text(json.dumps(v))
with b.locked():
 x=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];e=dict(v.split('=',1) for v in x['Config']['Env']);fleet=json.loads(e['SANDBOXD_CUBE_FLEET']);w=next(w for w in fleet['workers'] if w['id']=='vps');assert all(v['draining'] for v in fleet['workers'] if v['id']!='vps') and int(e.get('SANDBOXD_CUBE_TASK_CONCURRENCY','0'))==0
 cfg=json.loads(P('/opt/baarcha/operations/vps-sandbox-disk-recovery-20261007/operator-config.private.json').read_text());cfg.update(ProtectedCIDRs=e['SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS'],ProtectedDomains=e.get('SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS',''),Policy=w['admission'],ProxyURL='http://127.0.0.1:20080',MasterURL='http://10.254.240.1:18089',Migrations=str(r/'resume-retry-release-d5b07bb/migrations'));cfg['Provider'].update(APIURL='http://127.0.0.1:20300',APIKey=e['SANDBOXD_CUBE_API_KEY'])
 save(out/'operator-config.PRIVATE.json',cfg)
 save(out/'operator-identity.json',{'controller_id':x['Id'],'image':x['Image']})
 stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');inner='/data/baarcha-source-generations/'+stamp
 scope={'worker':'vps','bindings':[{'sandbox_id':'01M2QDV0PGEJ1AAKE2MJXGK4P8','runtime_id':'5373f2de0dac4906987503f741534ccb','worker_id':'vps'}]}
 code='import pathlib,json\nr=pathlib.Path('+repr(inner)+');r.mkdir(mode=0o700);(r/"scope.json").write_text('+repr(json.dumps(scope))+')'
 subprocess.run(ssh+['python3 -c '+shlex.quote(code)],check=True,capture_output=True)
 capture=subprocess.check_output(ssh+['python3 /root/baarcha-source-backup-tools/capture.py '+inner],stderr=subprocess.PIPE,timeout=180);save(out/'native-capture.json',json.loads(capture));save(out/'native-location.json',{'path':inner,'worker':'vps'})
 code='''import pathlib,json,hashlib,subprocess
r=pathlib.Path(INNER);rows=json.loads((r/'resolved-coverage.json').read_text());assert len(rows)==1 and rows[0]['mode']=='current_disk' and not rows[0]['changed_required']
files=rows[0]['required_files'];disk=next(p for p in files if '/sb-5373f2de0dac4906987503f741534ccb-rootfs-gen' in p)
source=pathlib.Path('/'+disk);copy=r/'tree'/disk;st=source.stat()
for proc in pathlib.Path('/proc').glob('[0-9]*'):
 for fd in (proc/'fd').glob('*'):
  try:s=fd.stat()
  except OSError:continue
  assert (s.st_dev,s.st_ino)!=(st.st_dev,st.st_ino),'source disk still open'
assert '5373f2de0dac4906987503f741534ccb' not in subprocess.check_output(['ctr','--address','/data/cubelet/cubelet.sock','--namespace','default','tasks','list']).decode()
def sha(p):
 with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
h=sha(copy);assert h==sha(source)
print(json.dumps({'worker':'vps','source':str(source),'backup':str(copy),'sha256':h,'bytes':st.st_size,'source_identity':[st.st_dev,st.st_ino,st.st_size,st.st_mtime_ns],'no_live_handles':True}))
'''.replace('INNER',repr(inner))
 evidence=json.loads(subprocess.check_output(ssh+['python3 -c '+shlex.quote(code)],stderr=subprocess.PIPE,timeout=180));save(out/'native-backup.json',evidence)
 with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
  with sqlite3.connect(out/'preflight.db') as dest:db.backup(dest)
 print(json.dumps({'staged':True,'native_backup_bytes':evidence['bytes'],'source_execution_absent':True}))
