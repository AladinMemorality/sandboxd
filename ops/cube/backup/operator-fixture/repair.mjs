// Single reviewed fixture repair. Not a generic installer retry mechanism.
import assert from 'node:assert/strict';
import {candidateManifest,confirmEffective,digest,WORKER} from './fixture.mjs';
export const REPAIR_RUN='8bb03c01d5527213';
export function correctedArgument(original,run){
 assert.equal(run,REPAIR_RUN,'Only the reviewed failed fixture');
 const before=`    command: node .operator-recovery/${run}/home-worker.mjs`;
 const lines=original.split('\n');assert.equal(lines.filter(x=>x===before).length,1,'Exactly the missing argument command required');
 return lines.map(x=>x===before?x+' '+run:x).join('\n');
}
export async function repairArgument(config,journal,io,guard){
 assert.equal(config.run,REPAIR_RUN);assert(!journal.pending&&!journal.installed&&journal.prepared&&!journal.argument_repair,'Exact failed installation required');
 assert.equal(journal.done?.['install-reload']?.provider_replaced,false,'Original reload acknowledgement required');
 const reviewed=config.argument_repair;assert(reviewed,'Root-reviewed repair pins required');
 for(const value of Object.values(reviewed))assert.match(value,/^[0-9a-f]{64}$/);
 const beforeBytes=await io.currentJournal();assert.equal(digest(beforeBytes),reviewed.journal_sha256,'Original journal changed');
 await guard();
 const base=`.operator-recovery/${config.run}`;
 const expected=[['sandbox.yaml','manifest_sha256'],['server.mjs','server_sha256'],['public/index.html','html_sha256'],[`${base}/home-worker.mjs`,'worker_sha256'],[`${base}/app marker #.txt`,'app_marker_sha256']];
 const verifyFiles=async()=>{for(const [name,key] of expected)assert.equal(digest(await io.read(name)),reviewed[key],'Installed file changed: '+name);};
 await verifyFiles();assert.equal(digest(journal.candidate.manifest),reviewed.manifest_sha256);
 const next=correctedArgument(journal.candidate.manifest,config.run);
 assert.equal(next,candidateManifest(Buffer.from(journal.before['sandbox.yaml'].bytes,'base64').toString(),config.run),'Only missing argument correction allowed');
 const oldEffective=await io.validate(journal.candidate.manifest),newEffective=await io.validate(next);
 assert(oldEffective.valid&&newEffective.valid);const oldWorkers=structuredClone(oldEffective.effective),newWorkers=structuredClone(newEffective.effective);
 const oldWorker=oldWorkers.workers.find(x=>x.name===WORKER),newWorker=newWorkers.workers.find(x=>x.name===WORKER);assert(oldWorker&&newWorker);assert.equal(newWorker.command,oldWorker.command+' '+config.run);newWorker.command=oldWorker.command;assert.deepEqual(newWorkers,oldWorkers,'Other process changed');
 confirmEffective(await io.validate(Buffer.from(journal.before['sandbox.yaml'].bytes,'base64').toString()),newEffective);
 await io.artifact('argument-repair-before-journal.json',beforeBytes);
 const repair={version:1,kind:'operator-fixture-missing-argument-repair',run:config.run,before_journal_sha256:reviewed.journal_sha256,before_manifest_sha256:reviewed.manifest_sha256,after_manifest_sha256:digest(next),task_inventory_sha256:config.task_inventory_sha256,ai_journal_unchanged:true,ai_task_success:false};
 await io.createRepair(repair);await guard();await verifyFiles();
 repair.pending='manifest-put';await io.saveRepair(repair);await io.write('sandbox.yaml',Buffer.from(next));assert.equal(digest(await io.read('sandbox.yaml')),digest(next),'Repair readback differs');
 repair.pending='reload';repair.manifest_put_acknowledged=true;await io.saveRepair(repair);await guard();await io.reload();
 repair.pending='actual-proof';repair.reload_acknowledged=true;await io.saveRepair(repair);
 const proof=await io.waitProof();assert.equal(proof.run,config.run);assert(proof.hardlink_same_inode);assert.equal(proof.uid,1000);assert.equal(proof.file_mode,0o640);assert.equal(proof.script_mode,0o750);
 await guard();assert.equal(digest(await io.read('sandbox.yaml')),digest(next),'Manifest changed after proof');
 for(const [name,key] of expected.slice(1))assert.equal(digest(await io.read(name)),reviewed[key],'Source changed after proof');
 repair.pending='original-journal-commit';repair.source_proof=proof;repair.proof_verified=true;await io.saveRepair(repair);
 const receipt={...repair,pending:undefined,observed_at:new Date().toISOString(),original_journal_commit_pending:true};const receiptBytes=Buffer.from(JSON.stringify(receipt,null,2)+'\n');await io.artifact('argument-repair-proof-receipt.json',receiptBytes);
 const updated=structuredClone(journal);updated.candidate.manifest=next;updated.installed=true;updated.source_proof=proof;updated.argument_repair={receipt_sha256:digest(receiptBytes),before_journal_sha256:reviewed.journal_sha256,at:receipt.observed_at};
 await io.commitOriginal(reviewed.journal_sha256,updated);repair.pending=null;repair.complete=true;repair.original_journal_updated=true;await io.saveRepair(repair);
 return repair;
}
