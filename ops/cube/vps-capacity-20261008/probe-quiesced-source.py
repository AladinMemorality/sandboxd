"""Read a retained source's control/process state under scoped maintenance."""
import importlib.util,json,pathlib,shlex,subprocess,sys
from maintenance_account import account_maintenance
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');sid='01M1XFEHGCQ2HV5NNJBXNQK3WE';runtime='19886838497240a5a507b8ac4d3fe35e'
spec=importlib.util.spec_from_file_location('boot','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
spec=importlib.util.spec_from_file_location('copy','/opt/baarcha-bench/cube-fleet-20260927/copy-fleet.py');copy=importlib.util.module_from_spec(spec);spec.loader.exec_module(copy)
out=root/'source-control-probe';out.mkdir(mode=0o700)
guest="""import pathlib,os,json,time,urllib.request
p=pathlib.Path;live=[]
for d in p('/proc').iterdir():
 if not d.name.isdigit():continue
 try:
  if os.readlink(d/'exe').removesuffix(' (deleted)')=='/usr/local/bin/runtimed':live.append(d)
 except OSError:pass
assert len(live)==1
env=dict(v.split('=',1) for v in (live[0]/'environ').read_bytes().decode().split('\\0') if '=' in v)
req=urllib.request.Request('http://127.0.0.1:3031/status',headers={'Authorization':'Bearer '+env['RUNTIMED_HTTP_TOKEN']})
with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=5) as r:status=json.load(r)
assert not status.get('active_task')
files=[{'name':f.name,'bytes':f.stat().st_size,'age':time.time()-f.stat().st_mtime} for f in p('/home/sandbox/.runtimed').glob('.private-workspace-v2-*')]
tcp=[line for line in p('/proc/net/tcp').read_text().splitlines()[1:] if line.split()[1].endswith(':0BD7')]
print('RUNTIME_RECEIPT='+json.dumps({'active_task':False,'quiesced_marker':p('/home/sandbox/.runtimed/workspace-quiesced').exists(),'export_files':files,'control_connections':tcp}))
"""
with b.locked(),account_maintenance([sid],out):
    try:
        code,_=copy.api('POST','/v1/sandboxes/'+sid+'/start');assert code==200
        inner="import sys,json;sys.path.insert(0,'/opt/baarcha-vps-process-recovery-a583d45');import worker;print(json.dumps(worker.execute("+repr(runtime)+","+repr(guest)+",'probe',b'')))"
        ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p','20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
        p=subprocess.run(ssh+['python3 -'],input=inner.encode(),capture_output=True,timeout=45)
        b.atomic(out/'guest.PRIVATE.stdout',p.stdout);b.atomic(out/'guest.PRIVATE.stderr',p.stderr)
        assert p.returncode==0,'Read-only guest probe failed'
        result=json.loads(p.stdout);b.atomic(out/'result.json',b.encoded(result));print(json.dumps(result))
    finally:
        code,_=copy.api('POST','/v1/sandboxes/'+sid+'/stop');assert code==200
