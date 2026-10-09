"""Read-only readiness/writeback sample during the paced archive transfer."""
import json,os,pathlib,time,urllib.request
os.umask(0o077);root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'transfer-readiness-watch-01';out.mkdir(mode=0o700)
rows=[]
for i in range(60):
 begin=time.monotonic();row={'at':time.time()}
 try:
  with urllib.request.urlopen('http://127.0.0.1:9090/readyz',timeout=5) as response:row['ready']=response.status==200 and response.read().strip()==b'ready'
 except Exception as error:row.update(ready=False,error=type(error).__name__)
 row['seconds']=time.monotonic()-begin
 memory={l.split(':',1)[0]:int(l.split()[1])*1024 for l in pathlib.Path('/proc/meminfo').read_text().splitlines()}
 row.update(dirty_bytes=memory['Dirty'],writeback_bytes=memory['Writeback']);rows.append(row)
 (out/'samples.json').write_text(json.dumps(rows))
 if i<59:time.sleep(5)
result={'passed':all(r['ready'] for r in rows),'checks':len(rows),'failed_checks':sum(not r['ready'] for r in rows),'max_seconds':max(r['seconds'] for r in rows),'max_dirty_bytes':max(r['dirty_bytes'] for r in rows),'max_writeback_bytes':max(r['writeback_bytes'] for r in rows),'duration_seconds':rows[-1]['at']-rows[0]['at'],'model_calls':False,'b200_contacted':False}
(out/'result.json').write_text(json.dumps(result));print(json.dumps(result),flush=True)
