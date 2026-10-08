"""Inventory VPS-local restore inputs without contacting either worker."""
import collections, hashlib, json, os, tarfile, time
from pathlib import Path
os.umask(0o077)
root=Path('/var/backups/baarcha-sandboxes/20261008T1440Z')
source=root/'source-backup-v2'
bindings=json.loads((root/'bindings.json').read_bytes())
results=[]
for binding in bindings:
    sid=binding['sandbox_id']; directory=source/binding['worker_id']/sid
    archive=directory/'home.tar.gz'; verified=json.loads((directory/'verified.json').read_bytes())
    h=hashlib.sha256()
    with archive.open('rb') as file:
        for chunk in iter(lambda:file.read(4*1024**2),b''):h.update(chunk)
    assert h.hexdigest()==verified['sha256'] and archive.stat().st_size==verified['bytes']
    row={'sandbox_id':sid,'source_worker':binding['worker_id'],'archive_bytes':archive.stat().st_size,
         'sha256':h.hexdigest(),'files':0,'expanded_bytes':0,'databases':0,'git_files':0,
         'upload_files':0,'configuration_files':0,'manifests':[],'lockfiles':[]}
    with tarfile.open(archive,mode='r|gz') as tar:
        for item in tar:
            if not item.isfile():continue
            row['files']+=1;row['expanded_bytes']+=item.size
            name=item.name.removeprefix('./');base=name.rsplit('/',1)[-1]
            row['databases']+=int(base.endswith(('.db','.sqlite','.sqlite3','-wal')))
            row['git_files']+=int('/.git/' in '/'+name)
            row['upload_files']+=int('/uploads/' in '/'+name)
            row['configuration_files']+=int(base=='.env' or base.startswith('.env.'))
            if base in ('package-lock.json','pnpm-lock.yaml','yarn.lock','bun.lock','bun.lockb','poetry.lock','uv.lock','requirements.txt'):row['lockfiles'].append(name)
            if base=='package.json' and '/node_modules/' not in '/'+name and item.size<1024**2:
                try:
                    value=json.load(tar.extractfile(item))
                    row['manifests'].append({'path':name,'scripts':sorted(value.get('scripts',{})),
                         'frameworks':sorted(set(value.get('dependencies',{}))&{'next','vite','react','express','fastify','astro','vue','svelte'}),
                         'package_manager':value.get('packageManager')})
                except (ValueError,TypeError):row['manifests'].append({'path':name,'invalid_json':True})
    metadata=source/binding['worker_id']/'runtime-metadata/cubebox'/(binding['runtime_id']+'.json')
    assert metadata.is_file();row['runtime_configuration_present']=True
    results.append(row)
summary={'sandboxes':len(results),'verified_archives':len(results),'archive_bytes':sum(r['archive_bytes'] for r in results),
         'expanded_bytes':sum(r['expanded_bytes'] for r in results),'with_databases':sum(r['databases']>0 for r in results),
         'with_uploads':sum(r['upload_files']>0 for r in results),'with_git_history':sum(r['git_files']>0 for r in results),
         'with_local_env':sum(r['configuration_files']>0 for r in results),'with_package_manifests':sum(bool(r['manifests']) for r in results),
         'workers_contacted':[],'at':time.time(),'full_application_restore':False}
out=Path('/opt/baarcha/operations/vps-50-profiles-20261008')
(out/'recovery-audit.PRIVATE.json').write_text(json.dumps({'summary':summary,'results':results}))
(out/'recovery-audit-summary.json').write_text(json.dumps(summary,indent=2))
print(json.dumps(summary),flush=True)
