"""Accept completed live cycles using timestamped Shim and actual VMM evidence."""
import contextlib,importlib.util,json,pathlib,re,sqlite3,subprocess,time
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');out=root/'full-pause-canary-01'
sid='01M2AR44CA9AW2FEFA44KVEAGQ';rid='e3a26535a2874c77a799e45f40158cd1'
spec=importlib.util.spec_from_file_location('b','/usr/local/libexec/baarcha-cube-boot-transition.py');b=importlib.util.module_from_spec(spec);spec.loader.exec_module(b)
with b.locked():
 assert not (out/'complete.json').exists();failed=json.loads((out/'failed.json').read_text());assert failed['type']=='AssertionError' and failed['reason']==''
 began=json.loads((root/'full-pause-followthrough-02/stage.json').read_text())['at'];end=failed['at'];progress=json.loads((out/'progress.json').read_text())
 assert [r['cycle'] for r in progress]==[1,2,3] and all(r['modules']==23 and r['memory']['oom_kill']==0 for r in progress)
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
  assert db.execute('select s.status,r.runtime_id,a.state,a.charged from sandbox s join runtime_binding r on r.sandbox_id=s.id join cube_admission a on a.runtime_id=r.runtime_id where s.id=?',(sid,)).fetchall()==[('stopped',rid,'released',0)]
 code='''import pathlib,json,datetime,re
rid=RID;began=BEGAN;end=END;pauses=[];restores=[]
for p in pathlib.Path('/data/log/CubeShim').glob('*.log'):
 for line in p.open():
  if rid not in line:continue
  try:v=json.loads(line)
  except ValueError:continue
  if v.get('InstanceId')!=rid or 'Timestamp' not in v or 'LogContent' not in v:continue
  at=datetime.datetime.fromisoformat(v['Timestamp'].replace('Z','+00:00')).timestamp()
  if not began<=at<=end:continue
  msg=v['LogContent']
  if msg.startswith('pause to snapshot: destination='):
   assert msg.endswith('snapshot_type=full');pauses.append(re.search(r'pause-snapshots/(snap-[a-z0-9]+)',msg).group(1))
  if msg.startswith('snapshot restore init using snapshot_base:'):
   restores.append(re.search(r'pause-snapshots/(snap-[a-z0-9]+)',msg).group(1))
assert len(pauses)==4 and len(set(pauses))==4
assert restores[1:]==pauses[:3] and len(restores)==4
lines=[l for l in pathlib.Path('/data/log/CubeVmm/vmm.log').read_text().splitlines() if l.startswith(rid+' --- ')]
verified=[]
for snap in pauses:
 idx=[i for i,l in enumerate(lines) if snap in l and 'VmPauseToSnapshot(SnapshotConfig' in l];assert len(idx)==1
 i=idx[0];assert 'snapshot_type: Full,' in lines[i]
 following=[]
 for l in lines[i+1:]:
  if 'API request event: VmRestore(' in l or 'API request event: VmPauseToSnapshot(' in l:break
  following.append(l)
 assert any('Saving full guest memory to snapshot image file.' in l for l in following)
 assert not any('PagemapAnon snapshot' in l for l in following)
 verified.append(snap)
print(json.dumps({'passed':True,'full_pause_records':len(pauses),'snapshots':verified,'restores_of_new_full_snapshots':3,'timestamp_start':began,'timestamp_end':end}))
'''.replace('RID',repr(rid)).replace('BEGAN',repr(began)).replace('END',repr(end))
 ssh=['ssh','-i','/opt/baarcha-cube/worker-01/operator-key','-p20222','-oUserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts','root@127.0.0.1']
 evidence=json.loads(subprocess.check_output(ssh+['python3 -'],input=code.encode(),timeout=30));assert evidence['passed']
 b.atomic(out/'evidence-reconciliation.json',b.encoded(evidence))
 result={'passed':True,'sandbox_id':sid,'runtime_id':rid,'cycles':progress,'full_pause_records':4,'capture_verified_by':'Shim timestamps and VMM full-memory dump records','original_log_check_failure_retained':True,'model_calls':False,'b200_contacted':False,'at':time.time()}
 b.atomic(out/'complete.json',b.encoded(result));print(json.dumps(result))
