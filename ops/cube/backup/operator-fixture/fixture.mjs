// Explicit operator fixture; intentionally independent of AI task success.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
export const APP='01M3CZB4HXT2Y8HP8CEY75PCWY', SANDBOX='01M3D1Q0E1KM1FEM244XVHEC65';
export const WORKER='operator_recovery_data';
export const digest=x=>crypto.createHash('sha256').update(x).digest('hex');
const need=(x,m)=>assert(x,m);
export function candidateManifest(original,run){
 need(/^[a-f0-9]{16}$/.test(run),'Invalid run');need(!original.includes('\r')&&!original.includes('\t'),'Noncanonical manifest formatting');
 need(!original.includes('operator_frontend_profile')&&!original.includes(WORKER),'Another operator worker is installed');
 const lines=original.split('\n'),starts=lines.flatMap((s,i)=>s==='workers:'?[i]:[]);need(starts.length===1,'Exactly one block workers mapping required');
 let end=starts[0]+1;while(end<lines.length&&(!lines[end]||/^\s|^#/.test(lines[end])))end++;
 lines.splice(end,0,`  - name: ${WORKER}`,`    command: node .operator-recovery/${run}/home-worker.mjs`,'    restart_after_task: false');
 return lines.join('\n');
}
export function patchServer(original,run){
 need(/^[a-f0-9]{16}$/.test(run),'Invalid run');const anchor='const app = express();';
 need(original.split(anchor).length===2&&!original.includes('operatorReadRecoveryProof')&&!original.includes('/operator-recovery-proof'),'Server requires explicit review; no guessed patch');
 const head=`import {readRecoveryProof as operatorReadRecoveryProof} from './.operator-recovery/${run}/home-worker.mjs';\n`;
 return head+original.replace(anchor,anchor+`\napp.get('/operator-recovery-proof', (_request,response)=>{try{response.set('Cache-Control','no-store').json(operatorReadRecoveryProof('${run}'));}catch{response.status(500).json({error:'Operator recovery proof failed'});}});`);
}
export function patchHTML(original,run){
 need(/^[a-f0-9]{16}$/.test(run),'Invalid run');need(original.split('</body>').length===2&&!original.includes('operator-authored-recovery-data'),'HTML requires explicit review');
 return original.replace('</body>',`<p id="operator-authored-recovery-data">Operator recovery marker: operator-recovery-${run}:app</p>\n</body>`);
}
export function confirmEffective(before,after){
 need(before?.valid&&after?.valid,'Both manifests must validate');const a=structuredClone(after.effective),b=structuredClone(before.effective);
 need(a.workers.filter(x=>x.name===WORKER).length===1,'Exactly one added worker required');a.workers=a.workers.filter(x=>x.name!==WORKER);assert.deepEqual(a,b,'Existing web/workers changed');
}
export class OperatorFixture{
 constructor(config,journal,io){this.c=config;this.j=journal;this.io=io;need(config.app_id===APP&&config.sandbox_id===SANDBOX,'Only exact owned canary');need(/^[a-f0-9]{16}$/.test(config.run),'Invalid run');}
 async guard(){
  await this.io.guard();const tasks=await this.io.tasks();
  need(tasks.length===4&&tasks.every(t=>t.status==='failed'),'Four original terminal failures required; never submit/cancel a task');
  need(digest(JSON.stringify(tasks))===this.c.task_inventory_sha256,'Task history changed');
  need(digest(await this.io.originalJournal())===this.c.original_journal_sha256,'Original AI journal changed');
 }
 async intent(name){need(!this.j.pending&&!this.j.done?.[name],'Unresolved or repeated mutation intent');this.j.pending={name,at:new Date().toISOString()};await this.io.save(this.j);}
 async done(name,data){need(this.j.pending?.name===name,'Intent differs');this.j.done??={};this.j.done[name]=data;delete this.j.pending;await this.io.save(this.j);}
 async put(name,bytes,expected){
  await this.guard();need(bytes.length<=2*1024*1024,'Full readback limit exceeded');const current=await this.io.read(name);need(expected===null?current===null:current!==null&&digest(current)===expected,'Source changed before write');
  await this.intent('write:'+name);await this.io.write(name,bytes);need(digest(await this.io.read(name))===digest(bytes),'Readback differs');await this.done('write:'+name,{sha256:digest(bytes)});
 }
 async prepare(){
  await this.guard();need(!this.j.prepared,'Already prepared');need(!this.j.pending,'Pending intent');
  await this.io.artifact('original-ai-journal.json',await this.io.originalJournal());
  const raw={};for(const name of ['sandbox.yaml','server.mjs','public/index.html']){raw[name]=await this.io.read(name);need(raw[name]!==null&&digest(raw[name])===this.c.before_sha256[name],'Source requires exact review');await this.io.artifact('before-'+name.replaceAll('/','-'),raw[name]);}
  this.j.before=Object.fromEntries(Object.entries(raw).map(([k,v])=>[k,{sha256:digest(v),bytes:v.toString('base64')} ]));
  for(const task of await this.io.tasks()){const events=await this.io.events(task.id);need(events.length>0,'Missing original task events');await this.io.artifact(`failed-task-${task.id}.events`,events);await this.io.artifact(`failed-task-${task.id}.json`,Buffer.from(JSON.stringify(task)+'\n'));}
  const manifest=candidateManifest(raw['sandbox.yaml'].toString(),this.c.run);confirmEffective(await this.io.validate(raw['sandbox.yaml'].toString()),await this.io.validate(manifest));
  this.j.candidate={manifest,server:patchServer(raw['server.mjs'].toString(),this.c.run),html:patchHTML(raw['public/index.html'].toString(),this.c.run)};
  await this.io.checkSyntax(this.j.candidate.server);
  this.j.prepared=true;this.j.ai_success=false;await this.io.save(this.j);
 }
 async install(){
  await this.guard();need(this.j.prepared&&!this.j.installed,'Prepare first');const base=`.operator-recovery/${this.c.run}`;
  for(const [name,bytes] of [[`${base}/home-worker.mjs`,await this.io.workerSource()],[`${base}/app marker #.txt`,Buffer.from(`operator-recovery-${this.c.run}:app`)]])await this.put(name,bytes,null);
  // All original source backups are durable before any replacement. Every
  // mutation has an explicit pending journal entry; ambiguous acknowledgements
  // are retained and never automatically retried.
  await this.guard();await this.put('server.mjs',Buffer.from(this.j.candidate.server),this.c.before_sha256['server.mjs']);
  await this.put('public/index.html',Buffer.from(this.j.candidate.html),this.c.before_sha256['public/index.html']);
  await this.put('sandbox.yaml',Buffer.from(this.j.candidate.manifest),this.c.before_sha256['sandbox.yaml']);
  await this.guard();await this.intent('install-reload');await this.io.reload();await this.done('install-reload',{provider_replaced:false});
  const ready=await this.io.waitProof();need(ready.run===this.c.run&&ready.hardlink_same_inode&&ready.uid===1000&&ready.file_mode===0o640&&ready.script_mode===0o750,'Home/file proof differs');
  this.j.installed=true;this.j.source_proof=ready;await this.io.save(this.j);
 }
 async restoreManifest(){
  await this.guard();need(this.j.installed&&!this.j.manifest_restored,'Installed helper required');
  const current=await this.io.read('sandbox.yaml');need(digest(current)===digest(this.j.candidate.manifest),'Another manifest change; do not overwrite');
  await this.intent('restore-manifest');await this.io.write('sandbox.yaml',Buffer.from(this.j.before['sandbox.yaml'].bytes,'base64'));
  need(digest(await this.io.read('sandbox.yaml'))===this.c.before_sha256['sandbox.yaml'],'Manifest restore readback differs');await this.io.reload();
  await this.done('restore-manifest',{sha256:this.c.before_sha256['sandbox.yaml']});this.j.manifest_restored=true;await this.io.save(this.j);
 }
 async verify(){
  await this.guard();need(this.j.manifest_restored,'Restore original process manifest before proof');
  const proof=await this.io.waitProof();assert.deepEqual(proof,this.j.source_proof,'Fresh filesystem proof changed');
  need((await this.io.health()).status==='ok','Original PostgreSQL health');need((await this.io.html()).includes(`operator-recovery-${this.c.run}:app`),'Visible operator marker missing');
  await this.intent('sql-row');const marker=`operator-recovery-${this.c.run}:acknowledged-row`;
  need(!(await this.io.notes()).some(r=>r.body===marker),'Existing row requires reconciliation; do not duplicate');
  const row=await this.io.insertNote(marker);need(row.body===marker&&row.id&&row.created_at,'SQL acknowledgement missing');this.j.sql_row=row;await this.io.save(this.j);
  need((await this.io.notes()).some(r=>String(r.id)===String(row.id)&&r.body===row.body&&r.created_at===row.created_at),'Independent SQL readback differs');await this.done('sql-row',row);
  await this.intent('private-capture');const cover=await this.io.privateCapture();need(cover.owner_read&&cover.foreign_denied&&cover.anonymous_denied,'Private capture ACL incomplete');await this.done('private-capture',cover);
  await this.guard();this.j.verified={at:new Date().toISOString(),operator_authored:true,source_data_only:true,ai_coding_passed:false,backup:false,restore:false,failed_tasks_preserved:4,actual_browser_visual_review:false};await this.io.save(this.j);
 }
}
