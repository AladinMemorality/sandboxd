#!/usr/bin/env bash
# Explicit installation only, after the coordinating operator's cold checkpoint.
# Does NOT start services or remove the persistent enrollment hold.
set -euo pipefail
umask 077
if [ "$#" -ne 4 ]; then
  printf '%s\n' 'usage: install_after_boot.sh OLD_CONFIG MACHINE_ID PREVIOUS_BOOT_ID NEW_PRIVATE_INSTALL_DIR' >&2
  exit 2
fi
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
OLD_CONFIG="$1"
EXPECTED_MACHINE="$2"
PREVIOUS_BOOT="$3"
INSTALL_RECORD="$4"
test "$(id -u)" = 0
test "$(hostname)" = baarcha-cube-worker-01
test ! -e "$INSTALL_RECORD"
# No partial installation is attempted until this read-only preflight passes.
python3 "$SCRIPT_DIR/post_boot.py" before-install --old-config "$OLD_CONFIG" --machine-id "$EXPECTED_MACHINE" --previous-boot-id "$PREVIOUS_BOOT"
mkdir -m 700 -- "$INSTALL_RECORD"
python3 "$SCRIPT_DIR/render_config.py" "$OLD_CONFIG" "$INSTALL_RECORD/config-durable.toml"
python3 - "$INSTALL_RECORD" <<'PY'
import hashlib,json,os,pathlib,stat,sys,tempfile
record=pathlib.Path(sys.argv[1])
root=pathlib.Path('/data/cubelet/persistent-metadata')
config=pathlib.Path('/usr/local/services/cubetoolbox/Cubelet/config/config.toml')
binary=pathlib.Path('/usr/local/services/cubetoolbox/Cubelet/bin/cubelet')
candidate=pathlib.Path('/root/cube-production/durable-native-candidate/cubelet-candidate')
def digest(p):
 with p.open('rb') as f:return hashlib.file_digest(f,'sha256').hexdigest()
def syncdir(p):
 fd=os.open(p,os.O_RDONLY|os.O_DIRECTORY)
 try:os.fsync(fd)
 finally:os.close(fd)
def preserve(src,dst):
 with src.open('rb') as r,dst.open('xb') as w:
  os.fchmod(w.fileno(),0o600)
  while chunk:=r.read(1024*1024):w.write(chunk)
  w.flush();os.fsync(w.fileno())
def replace(src,dst,mode):
 if dst.is_symlink():raise ValueError('refuse symlink installed target')
 fd,name=tempfile.mkstemp(prefix='.durable-install-',dir=dst.parent)
 try:
  with os.fdopen(fd,'wb') as w,src.open('rb') as r:
   os.fchmod(w.fileno(),mode)
   while chunk:=r.read(1024*1024):w.write(chunk)
   w.flush();os.fsync(w.fileno())
  os.replace(name,dst);syncdir(dst.parent)
 finally:
  if os.path.exists(name):os.unlink(name)
preserve(config,record/'config-before.toml');preserve(binary,record/'cubelet-before')
cleanup=pathlib.Path('/data/cubelet/cleanup')
cleanup_before={str(p.relative_to(cleanup)):digest(p) for p in cleanup.rglob('*.db') if p.is_file()}
assert digest(candidate)=='a61a43c531b8e7854dd8ee064db0d1e160d42fcb4d50e7c4b3c98d447e63e75a'
assert not root.exists()
root.mkdir(mode=0o700);syncdir(root);syncdir(root.parent)
# Both replacements occur with management stopped; any failure leaves the hold
# in place and evidence for operator review. There is no unsafe auto-rollback.
replace(candidate,binary,0o755)
replace(record/'config-durable.toml',config,0o600)
assert cleanup_before=={str(p.relative_to(cleanup)):digest(p) for p in cleanup.rglob('*.db') if p.is_file()}
receipt={'installed_sha256':digest(binary),'config_sha256':digest(config),'old_config_sha256':digest(record/'config-before.toml'),'old_binary_sha256':digest(record/'cubelet-before'),'legacy_cleanup_db_sha256':cleanup_before,'services_started':False,'hold_released':False}
with (record/'install-receipt.json').open('x') as f:
 json.dump(receipt,f,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())
syncdir(record)
print(json.dumps({'installed':True,'services_started':False,'hold_released':False}))
PY
