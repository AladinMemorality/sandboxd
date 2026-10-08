"""Restore representative data-bearing homes from VPS archives, network disabled."""
import json,os,subprocess,time
from pathlib import Path
os.umask(0o077)
root=Path('/opt/baarcha/operations/vps-50-profiles-20261008')
backup=Path('/var/backups/baarcha-sandboxes/20261008T1440Z/source-backup-v2')
audit=json.loads((root/'recovery-audit.PRIVATE.json').read_bytes())['results']
selected=[r for r in audit if r['databases'] or r['upload_files']]
results=[]
image=json.loads(subprocess.check_output(['docker','image','inspect','node:22-bookworm-slim']))[0]['Id']
for row in selected:
    sid=row['sandbox_id'];volume='baarcha-data-restore-20261008-'+sid.lower()
    archive=backup/row['source_worker']/sid/'home.tar.gz'
    exists=subprocess.run(['docker','volume','inspect',volume],capture_output=True)
    assert exists.returncode!=0
    subprocess.run(['docker','volume','create','--label','purpose=vps-data-restore-check',volume],check=True,stdout=subprocess.DEVNULL)
    common=['docker','run','--rm','--network=none','--cpus=1','--memory=1g','--pids-limit=128',
            '--cap-drop=ALL','--security-opt=no-new-privileges','--read-only','--tmpfs','/tmp:rw,nosuid,nodev,size=64m',
            '-v',volume+':/restore','-w','/restore']
    result={'sandbox_id':sid,'workers_contacted':[],'production_binding_changed':False}
    try:
        subprocess.run(common+['-v',str(archive)+':/backup/home.tar.gz:ro',image,'sh','-ec',
                              'tar -xzf /backup/home.tar.gz --no-same-owner -C /restore'],check=True,capture_output=True,timeout=300)
        checked=subprocess.run(common+['-v',str(root/'restore-data-check.cjs')+':/checks/check.cjs:ro',image,
                                      'node','--experimental-sqlite','/checks/check.cjs'],check=True,capture_output=True,timeout=300)
        details=json.loads(checked.stdout)
        assert details['files']==row['files'] and details['bytes']==row['expanded_bytes']
        assert details['gitFiles']==row['git_files'] and details['uploads']==row['upload_files'] and details['localEnv']==row['configuration_files']
        result.update(status='passed',details=details)
    except Exception as e:
        result.update(status='failed',error_type=type(e).__name__)
        if isinstance(e,subprocess.CalledProcessError):
            (root/('restore-data-'+sid+'.PRIVATE.log')).write_bytes(e.stderr or b'')
    finally:
        subprocess.run(['docker','volume','rm',volume],check=True,stdout=subprocess.DEVNULL)
    results.append(result)
    (root/'restore-data-results.json').write_text(json.dumps({'at':time.time(),'results':results},indent=2))
    print(json.dumps({'sandbox_id':sid,'status':result['status']}),flush=True)
