"""Refresh the fleet backup if recovery changed provider identities or task history."""
import contextlib,hashlib,json,pathlib,sqlite3,subprocess,time
P=pathlib.Path;root=P('/opt/baarcha/operations/vps-50-profiles-20261008');backup_root=P('/var/backups/baarcha-vps-source')
latest=json.loads((backup_root/'latest.json').read_text());assert latest['verified']
saved=backup_root/latest['generation'];scope=json.loads((saved/'scope.json').read_text())
def signature(path):
 with contextlib.closing(sqlite3.connect('file:'+str(path)+'?mode=ro',uri=True)) as db:
  mapping=db.execute('select sandbox_id,runtime_id,config_revision from runtime_binding order by sandbox_id').fetchall()
  h=hashlib.sha256()
  for row in db.execute('select task_id,sandbox_id,status,prompt,result_json from task order by task_id'):h.update(json.dumps(row,separators=(',',':')).encode())
  return mapping,h.hexdigest()
changed=signature('/var/lib/sandboxd/state/sandboxd.db')!=signature(saved/'controller.PRIVATE.sqlite')
if changed or time.time()-latest['completed_at']>=6*3600:
 subprocess.run(['/usr/bin/python3','/usr/local/libexec/baarcha-vps-source-backup/backup.py'],check=True,timeout=7*3600)
 latest=json.loads((backup_root/'latest.json').read_text());assert latest['verified']
result={'verified':True,'refreshed':changed or latest['generation']!=saved.name,'generation':latest['generation'],'sandboxes':latest['sandboxes'],'at':time.time()}
(root/'source-backup-final-refresh.json').write_text(json.dumps(result));print(json.dumps(result))
