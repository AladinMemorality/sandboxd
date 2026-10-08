"""Reuse the exact verified NOS recovery artifacts, without contacting B200."""
import hashlib,json,os,pathlib,shutil,sqlite3,subprocess,sys
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
sys.path.insert(0,str(root/'recovery-tools'));import move_project_worker as transport
sid='01M415VT76M0M88GDY2NWWE942'
with sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True) as db:
    db.row_factory=sqlite3.Row
    row=dict(db.execute("select * from cube_recovery where recovery_id='nos-20261008'").fetchone())
    assert row['phase']=='complete' and row['sandbox_id']==sid
    assert db.execute('select runtime_id from runtime_binding where sandbox_id=?',(sid,)).fetchone()[0]==row['new_runtime_id']
    current=[tuple(r) for r in db.execute('select task_id,status,prompt,result_json from task where sandbox_id=? order by task_id',(sid,))]
    assert all(r[1] in ('succeeded','failed','cancelled') for r in current)
paths=json.loads(row['source_artifact_paths_json']);proof=json.loads(row['verification_json'])
assert all(proof[k] for k in ['Authenticated','WorkspaceVerified','HomeVerified','HistoryVerified','ConfigApplied','ApplicationReady'])
assert proof['NewRuntimeID']==row['new_runtime_id'] and proof['OldRuntimeID']==row['old_runtime_id']
with sqlite3.connect('file:'+paths['controller_backup']+'?mode=ro',uri=True) as db:
    assert db.execute('select task_id,status,prompt,result_json from task where sandbox_id=? order by task_id',(sid,)).fetchall()==current
out=root/'recovery-prepared-v2'/sid;out.mkdir(mode=0o700,parents=True,exist_ok=False)
manifest=json.loads(pathlib.Path(paths['home_manifest']).read_text())
receipt={'sandbox_id':sid,'runtime_id':row['new_runtime_id'],'home_manifest':manifest,'task_ids':[r[0] for r in current],'artifacts':{}}
for role in ['workspace','home','history']:
    source=pathlib.Path(paths[role]);assert source.is_file() and not source.is_symlink()
    with source.open('rb') as f:assert hashlib.file_digest(f,'sha256').hexdigest()==proof[role.title()+'SHA256']
    target=out/(role+'.zip');shutil.copyfile(source,target)
    receipt['artifacts'][role]={'sha256':transport.digest(target),'archive_bytes':target.stat().st_size,'local_path':str(target)}
with pathlib.Path(paths['native_backup']).open('rb') as f:receipt['source_archive_sha256']=hashlib.file_digest(f,'sha256').hexdigest()
(out/'export-result.PRIVATE.json').write_text(json.dumps(receipt))
(out/'recovery-chain.json').write_text(json.dumps({'recovery_id':row['recovery_id'],'previous_runtime':row['old_runtime_id'],'verified_runtime':row['new_runtime_id'],'all_artifacts_match_committed_recovery':True,'task_snapshot_unchanged':True,'source_contacted':False}))
subprocess.run([str(root/'artifact-validator'),str(out)],check=True)
print(json.dumps({'prepared':True,'sandbox_id':sid,'verified_recovery_chain':True,'source_contacted':False}))
