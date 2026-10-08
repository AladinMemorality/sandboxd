"""Canonicalize redundant DOS ZIP flags without changing names, bytes or Unix modes.

Keep original archives and receipts. Go's archive/zip exporter sets the DOS
read-only bit for Unix entries lacking owner write permission; Python does not.
"""
import hashlib,json,os,pathlib,shutil,stat,sys,zipfile
os.umask(0o077)
root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
sys.path.insert(0,str(root/'recovery-tools'))
import move_project_worker as transport
sid=sys.argv[1]
assert len(sid)==26 and sid.isalnum()
source_group=sys.argv[2] if len(sys.argv)>2 else 'recovery-prepared'
assert source_group in ('recovery-prepared','recovery-prepared-v2')
source=root/source_group/sid
target=root/'recovery-prepared-canonical'/sid
target.mkdir(parents=True,exist_ok=False,mode=0o700)
receipt=json.loads((source/'export-result.PRIVATE.json').read_text())
changes={}
for role in ('workspace','home','history'):
    src=source/(role+'.zip');dst=target/(role+'.zip')
    assert transport.digest(src)==receipt['artifacts'][role]['sha256']
    changed=0
    with zipfile.ZipFile(src) as before,zipfile.ZipFile(dst,'w') as after:
        for entry in before.infolist():
            original=entry.external_attr
            mode=original>>16
            assert entry.create_system==3 and stat.S_IFMT(mode) in (stat.S_IFREG,stat.S_IFDIR,stat.S_IFLNK)
            dos=(0x10 if stat.S_ISDIR(mode) else 0)|(1 if not mode&0o200 else 0)
            assert original&0xffff in (0,1,0x10,0x11)
            entry.external_attr=(mode<<16)|dos
            changed+=entry.external_attr!=original
            with before.open(entry) as inp,after.open(entry,'w') as out:
                shutil.copyfileobj(inp,out,1024**2)
    with zipfile.ZipFile(src) as before,zipfile.ZipFile(dst) as after:
        assert before.namelist()==after.namelist()
        for name in before.namelist():
            a,b=before.getinfo(name),after.getinfo(name)
            assert a.external_attr>>16==b.external_attr>>16 and a.file_size==b.file_size
            def checksum(z,n):
                h=hashlib.sha256()
                with z.open(n) as f:
                    while block:=f.read(1024**2):h.update(block)
                return h.digest()
            assert checksum(before,name)==checksum(after,name)
    receipt['artifacts'][role]={'sha256':transport.digest(dst),'archive_bytes':dst.stat().st_size,'local_path':str(dst)}
    changes[role]=changed
(target/'export-result.PRIVATE.json').write_text(json.dumps(receipt))
(target/'canonicalization.json').write_text(json.dumps({'sandbox_id':sid,'source':str(source),'changed_dos_flags':changes,'names_content_unix_modes_verified':True}))
print(json.dumps({'sandbox_id':sid,'changed_dos_flags':changes}))
