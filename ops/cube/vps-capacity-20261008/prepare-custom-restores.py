"""Prepare the ten reviewed nonstandard recipes without starting their apps."""
import json,os,pathlib,subprocess
os.umask(0o077);root=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
profiles={
 '01M28XBR5WVEB9B3XWX1CN4NEB':'standard',
 '01M2JXMTMTBA5XXYDDJY2T51C9':'standard',
 '01M3KTSNB58S2GNXE9AA39NBKY':'standard',
 '01M3VXR9TQ688ZTE9ZEK0M9W37':'standard',
 '01M2QMWHFV91J7PV5HG3TRTZRK':'large',
 '01M37NA3YHW3WDPTJ63DD3FFGG':'large',
 '01M3M4SZAAA3K9Z6049GGP1YQZ':'large',
 '01M3MHXVCC4P23MH0EQ2YXWHA2':'large',
 '01M3C9C0V0MQYNFTMCYS7CCNVC':'large',
 '01M1HJ4EXF1GS6GE3BS9G3ANF3':'standard',
}
scope=json.loads((root/'vite-batch-03/scope.json').read_text())
assert set(profiles)=={x['sandbox_id'] for x in scope['skipped']}
assert all(x['reason']=='different startup recipe' for x in scope['skipped'])
for sid in profiles:
    prepared=root/'recovery-prepared-canonical'/sid
    if not prepared.exists():
        source=next(root/g/sid for g in ['recovery-prepared-v2','recovery-prepared'] if (root/g/sid/'export-result.PRIVATE.json').exists())
        subprocess.run(['/usr/bin/python3',str(root/'canonicalize-recovery-zip.py'),sid,source.parent.name],check=True,timeout=300)
    subprocess.run([str(root/'artifact-validator'),str(prepared)],check=True,timeout=180)
(root/'custom-restore-plan.json').write_text(json.dumps({'profiles':profiles,'source_contacted':False,'model_calls':False}))
