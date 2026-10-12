import fs from 'node:fs';
import { execFileSync } from 'node:child_process';
import { sql, closePg } from '/opt/baarcha/app/landing/src/lib/pg.ts';
import { creationStore } from '/opt/baarcha/app/landing/src/lib/projects/creation-store.ts';
import { resumeCreation } from '/opt/baarcha/app/landing/src/lib/projects/creation-service.ts';
import { withSandboxAccountMutation } from '/opt/baarcha/app/landing/src/lib/projects/mutation-lock.ts';

const projectId='01M4S5B9SPDTZCJKHHM6G1WPSE';
const root='/opt/baarcha/operations/scheduler-budget-20261013';
let claimed;
try {
  const before=await creationStore.forProject(1,projectId);
  if (!before || !['provisioning','project'].includes(before.phase) || before.state.sandbox || before.state.task) throw Error('Creation state changed');
  claimed=await creationStore.claim(1,before.request_id);
  if (!claimed || !['provisioning','project'].includes(claimed.phase)) throw Error('Creation is busy or changed');
  if (!fs.existsSync(root+'/reconciled.json')) {
    fs.writeFileSync(root+'/creation-before.PRIVATE.json',JSON.stringify(claimed,null,2),{mode:0o600,flag:'wx'});
    execFileSync('python3',[root+'/reconcile-car-wash.py'],{stdio:'inherit',timeout:60000});
  }
  const proof=JSON.parse(fs.readFileSync(root+'/reconciled.json','utf8'));
  if (!proof.reconciled || proof.app!==projectId || proof.provider_request_id!=='6df202da-ac46-4e66-977c-281110af25fa') throw Error('Missing reconciliation receipt');
  execFileSync('python3',['-c',`import sqlite3
c=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)
assert c.execute("SELECT state,charged,runtime_id FROM cube_admission WHERE admission_key=?",('app:${projectId}',)).fetchone()==('deleted',0,'')
assert c.execute('SELECT count(*) FROM sandbox WHERE app_id=?',('${projectId}',)).fetchone()[0]==0
`],{stdio:'inherit'});
  await withSandboxAccountMutation(1,async()=>{
    const db=sql();
    await db.begin(async tx=>{
      const rows=await tx`SELECT * FROM sandbox_start_reservation WHERE owner_id=1 AND target=${'project:'+projectId} FOR UPDATE`;
      if (rows.length!==1 || rows[0].reservation_id!=='f91d8ff4-c50c-4f54-822b-6172d04623de' || rows[0].sandbox_id || rows[0].operation!=='unknown') throw Error('Allowance reservation changed');
      fs.writeFileSync(root+'/allowance-before.PRIVATE.json',JSON.stringify(rows[0],null,2),{mode:0o600,flag:'wx'});
      await tx`DELETE FROM sandbox_start_reservation WHERE owner_id=1 AND target=${'project:'+projectId} AND reservation_id=${rows[0].reservation_id}`;
    });
  });
  claimed.phase='project';
  await creationStore.save(claimed);
  await creationStore.release(claimed);claimed=null;
  const state=await resumeCreation(1,null,projectId);
  const receipt={projectId,sandboxId:state?.sandbox?.id,taskId:state?.task?.id,status:state?.task?.status};
  fs.writeFileSync(root+'/resumed.json',JSON.stringify(receipt,null,2),{mode:0o600,flag:'wx'});
  console.log(JSON.stringify(receipt));
} finally {
  if (claimed) await creationStore.release(claimed);
  await closePg();
}
