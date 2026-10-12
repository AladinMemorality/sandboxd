#!/usr/bin/env python3
"""One exact failed create, proved rejected by the scheduler before dispatch.

Invoke only while holding the platform project_creation lease. Never generalize
this to arbitrary 500s or empty inventories: those do not prove non-allocation.
"""
import contextlib, fcntl, json, os
from pathlib import Path
import sqlite3, subprocess, time

ROOT=Path(__file__).resolve().parent
APP='01M4S5B9SPDTZCJKHHM6G1WPSE'
REQUEST='6df202da-ac46-4e66-977c-281110af25fa'
KEY='app:'+APP
SSH=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-o','BatchMode=yes',
     '-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts',
     '-p','20222','root@127.0.0.1']
LOCKS=['/opt/baarcha/deploy-release.lock','/opt/sandboxd/deploy-state/deploy.lock',
       '/run/lock/cube-operator-acceptance.lock','/opt/baarcha-bench/cube-workload-operator.lock']

def main():
 os.umask(0o077)
 with contextlib.ExitStack() as stack:
  for path in LOCKS:
   f=stack.enter_context(open(path,'a+'));fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
  raw=subprocess.check_output(SSH+['grep '+REQUEST+' /data/log/CubeMaster/cubemaster-req.log'],timeout=20)
  with (ROOT/'rejection-evidence.PRIVATE.jsonl').open('xb') as out:out.write(raw)
  evidence=[json.loads(line) for line in raw.splitlines()]
  messages=[r.get('LogContent','') for r in evidence]
  assert all(r.get('RequestId')==REQUEST for r in evidence)
  assert any('Filter/disk select_size:0'==m for m in messages)
  failures=[json.loads(m.removeprefix('CreateSandbox_rsp fail:')) for m in messages if m.startswith('CreateSandbox_rsp fail:')]
  assert len(failures)==1 and failures[0]['ret']=={'ret_code':130597,'ret_msg':'no more resource'}
  assert not any('retries:' in m or 'CreateSandbox_rsp:' in m for m in messages)
  creations=[json.loads(m.removeprefix('CreateSandbox:')) for m in messages if m.startswith('CreateSandbox:')]
  assert len(creations)==1 and creations[0]['labels']['sandboxd_app_id']==APP
  operation=creations[0]['labels']['sandboxd_admission_operation']
  sandbox=creations[0]['labels']['sandboxd_id']
  x=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0]
  env=dict(v.split('=',1) for v in x['Config']['Env'])
  check=r'''
import json,os,urllib.request
e=json.loads(os.environ['CHECK_ENV']);app=os.environ['CHECK_APP']
q=urllib.request.Request(e['SANDBOXD_CUBE_API_URL']+'/v2/sandboxes?limit=4096',headers={'X-API-Key':e['SANDBOXD_CUBE_API_KEY']})
rows=json.load(urllib.request.urlopen(q,timeout=20));assert isinstance(rows,list) and len(rows)<4096
assert not any(r.get('metadata',{}).get('sandboxd_app_id')==app for r in rows),'Provider runtime exists'
print(json.dumps({'provider_inventory':len(rows),'target_runtimes':0}))
'''
  subprocess.run(['nsenter','-t',str(x['State']['Pid']),'-n','python3','-c',check],
                 env={**os.environ,'CHECK_ENV':json.dumps(env),'CHECK_APP':APP},check=True,timeout=25)
  with sqlite3.connect('/var/lib/sandboxd/state/sandboxd.db',timeout=10) as db:
   with sqlite3.connect(ROOT/'before-reconcile.PRIVATE.sqlite') as backup:db.backup(backup)
   db.execute('BEGIN IMMEDIATE')
   assert db.execute('SELECT count(*) FROM sandbox WHERE app_id=? OR id=?',(APP,sandbox)).fetchone()[0]==0
   assert db.execute('SELECT count(*) FROM task WHERE sandbox_id=?',(sandbox,)).fetchone()[0]==0
   row=db.execute('SELECT runtime_id,operation,state,charged,worker_id,token FROM cube_admission WHERE admission_key=?',(KEY,)).fetchone()
   assert row==('', 'create','pending',1,'vps',operation),'Allocation changed'
   assert db.execute('SELECT count(*) FROM cube_recovery WHERE app_id=?',(APP,)).fetchone()[0]==0
   now=time.clock_gettime_ns(time.CLOCK_BOOTTIME);boot=Path('/proc/sys/kernel/random/boot_id').read_text().strip()
   policy=db.execute("SELECT last_clock_ns,clock_boot_id FROM cube_storage_policy WHERE worker_id='vps'").fetchone()
   assert policy[1]==boot and now>=policy[0]
   result=db.execute("UPDATE cube_admission SET state='deleted',charged=0 WHERE admission_key=? AND token=? AND state='pending' AND runtime_id='' AND operation='create'",(KEY,operation))
   assert result.rowcount==1
   result=db.execute("UPDATE cube_storage_grant SET released_ns=?,released_boot_id=? WHERE admission_key=? AND worker_id='vps' AND released_ns IS NULL AND token=? AND grant_boot_id=? AND granted_ns<=?",(now,boot,KEY,operation,boot,now))
   assert result.rowcount==1
   db.commit()
  receipt={'app':APP,'reconciled':True,'reason':'confirmed initial disk scheduler rejection','provider_request_id':REQUEST}
  (ROOT/'reconciled.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt))

if __name__=='__main__':main()
