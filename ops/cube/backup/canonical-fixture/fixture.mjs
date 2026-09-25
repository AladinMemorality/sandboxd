// PREPARED operator fixture. No execution is authorized by this file's presence.
// One action per invocation; no automatic cleanup, create retry or paid retry.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
const exec=promisify(execFile);
const ORIGIN='https://baarcha.tn', PRESET='node-postgres';
const ULID=/^[0-9A-HJKMNP-TV-Z]{26}$/, SHA=/^[a-f0-9]{64}$/;
export const digest=x=>crypto.createHash('sha256').update(x).digest('hex');
const need=(yes,message)=>assert(yes,message);
export function intentAllowed(j, name) { need(!j.pending, 'Unresolved mutation intent: inspect/reconcile; no retry'); need(!j.done?.[name], 'Phase already committed; do not repeat'); }
export function validateConfig(c) {
  need(c?.version===1 && /^[a-f0-9]{64}$/.test(c.controller_id) && /^sha256:[a-f0-9]{64}$/.test(c.controller_image), 'Expected exact controller identity');
  need(/^[a-f0-9]{40}$/.test(c.platform_revision) && /^tpl-[a-zA-Z0-9_-]+$/.test(c.template_id), 'Expected reviewed release/template');
  need(c.stage?.startsWith('/opt/baarcha-bench/cube-canonical-fixture-') && path.normalize(c.stage)===c.stage && !c.stage.endsWith('/'), 'Private fixture stage required');
  need(c.platform_root==='/opt/baarcha/app/landing', 'Unexpected platform root');
  need(c.allowed_app_id==='' || ULID.test(c.allowed_app_id), 'Exact app ID required');
  need(/^[a-f0-9-]{36}$/.test(c.worker_boot_id)&&SHA.test(c.enrollment_runner_sha256),'Reviewed worker boot and lock helper required');
  need(c.funding_millimes===1000, 'Only approved1000 millimes test grant');
  need(SHA.test(c.stop_binary_sha256) && SHA.test(c.start_binary_sha256), 'Reviewed storage34 coordinator hashes required');
  need(c.host_cycle_receipt?.startsWith('/opt/baarcha-bench/') && SHA.test(c.host_cycle_receipt_sha256), 'Real host cycle receipt required');
  return c;
}
export function validatePreview(sb, access) {
  need(ULID.test(sb.id) && sb.runtime_provider==='cube', 'Canonical Cube sandbox required');
  const u=new URL(sb.preview.url), handoff=new URL(access.access_url);
  need(u.protocol==='https:' && !u.port && !u.username && !u.password && !u.search && !u.hash && u.pathname==='/', 'Invalid preview URL');
  need(u.hostname===`s-${sb.id.toLowerCase()}-3000.preview.65.108.225.153.sslip.io`, 'Unexpected exact preview origin');
  need(new URL(access.url).href===u.href && handoff.origin===u.origin && handoff.pathname==='/__sandboxd/preview-auth', 'Preview capability mismatch');
  need(typeof access.token==='string' && /^[\x21-\x7e]{1,4096}$/.test(access.token) && !/[;,]/.test(access.token) && Date.parse(access.expires_at)>Date.now()+30000, 'Invalid preview capability');
  return u.origin;
}
export function promptFor(run) {
  need(/^[a-f0-9]{16}$/.test(run),'Invalid run');
  const marker=`cube-recovery-${run}`;
  return `This is one bounded operator backup fixture. Preserve the existing Node/PostgreSQL starter, dependencies and sandbox configuration. Do not install packages, call external URLs, create images, deploy, read credentials, or change unrelated files. Use local filesystem operations only. Write exactly ${marker}:app (no newline) to recovery-fixture/app marker.txt. Create /home/sandbox/recovery-fixture and write exactly ${marker}:home (no newline) to its home marker.txt. In that directory create relative symlink home-link -> home marker.txt and executable script run.sh containing #!/bin/sh followed by a newline and exit 0 followed by a newline, mode0750. In server.mjs add GET /recovery-proof returning JSON {app: <read app marker>,home:<read home marker>,link:<readlink home-link>,mode:<lstat run.sh mode & 511>} with all reads performed on each request, not hardcoded response values. Add the app marker as visible text to public/index.html without replacing the UI. Do not modify any existing SQL rows. Finish immediately after checking the marker endpoint and existing /health. No follow-up requests are needed.`;
}
async function privatePath(p, dir=false) {
  need(path.isAbsolute(p) && path.normalize(p)===p && await fs.realpath(p)===p,'Noncanonical private path');
  const s=await fs.lstat(p);need(s.uid===process.getuid() && (dir?s.isDirectory():s.isFile()) && (s.mode&0o777)===(dir?0o700:0o600),'Unsafe private path ownership/mode');return s;
}
async function atomic(p,obj) {
  await privatePath(path.dirname(p),true);
  const tmp=p+'.'+crypto.randomBytes(8).toString('hex')+'.tmp', h=await fs.open(tmp,'wx',0o600);
  try {await h.writeFile(JSON.stringify(obj,null,2)+'\n');await h.sync();}finally{await h.close();}
  try {await fs.lstat(p);await privatePath(p);}catch(e){if(e.code!=='ENOENT')throw e;}
  await fs.rename(tmp,p);const d=await fs.open(path.dirname(p),'r');try{await d.sync();}finally{await d.close();}
}
export async function boundedBody(response,max=4*1024*1024) {
  const reader=response.body?.getReader();if(!reader)return Buffer.alloc(0);
  const chunks=[];let size=0;try{for(;;){const {value,done}=await reader.read();if(done)break;size+=value.length;need(size<=max,'Response exceeds fixture limit');chunks.push(value);}return Buffer.concat(chunks);}catch(e){await reader.cancel();throw e;}
}
export async function runTaskToTerminal({submit,poll,cancel,save,now=Date.now,sleep=ms=>new Promise(r=>setTimeout(r,ms)),deadlineMs=180000}) {
  const deadline=now()+deadlineMs;let task,terminal=false;
  try {
    task=await submit();need(ULID.test(task?.id),'Task acknowledgement missing ID');await save(task);
    for(;;){need(now()<deadline,'Task deadline');
      const row=await poll(task.id);if(row.status!=='running'&&row.status!=='queued') {terminal=true;need(row.status==='succeeded' && row.checkpoint_id,'Real task/checkpoint incomplete');return row;}
      await sleep(Math.min(1000,Math.max(0,deadline-now())));
    }
  } catch(error) {
    if(task?.id&&!terminal)try{await cancel(task.id);}catch{/* Keep mutation intent; runtime120s ceiling still applies. */}
    throw error;
  }
}
export class Fixture {
  constructor(config,journal,{request,sql,persist,inspect,rejectedAppProof}) {this.c=config;this.j=journal;this.request=request;this.sql=sql;this.persist=persist;this.inspect=inspect;this.rejectedAppProof=rejectedAppProof;}
  async intent(name) {intentAllowed(this.j,name);this.j.pending={name,at:new Date().toISOString()};await this.persist();}
  async done(name,data) {need(this.j.pending?.name===name,'Mutation intent changed');this.j.done??={};this.j.done[name]=data;delete this.j.pending;await this.persist();}
  owner(role='owner'){const row=this.j.owners?.find(r=>r.role===role);need(row,'Synthetic owner missing');return row;}
  async rt(route,opts={}) {const r=await this.request('runtime',route,opts);need(r.status>=200&&r.status<300,`Runtime HTTP${r.status}`);return r.json;}
  async platform(route,opts={}) {return this.request('platform',route,{person:this.owner(),...opts});}
  async checkApp() {const app=await this.rt(`/v1/apps/${this.j.app}`);need(app.external_user_id===`baarcha:${this.owner().id}`&&app.external_project_id===`cube-recovery:${this.j.run}`&&app.runtime_preset===PRESET,'Owned app identity changed');return app;}
  async prepare() {
    need(!this.c.allowed_app_id,'Prepare requires Cube-disabled config and no app allowlist');
    await this.intent('owners');
    const owners=['owner','foreign'].map(role=>({role,sub:`cube-recovery:${this.j.run}:${role}`,token:crypto.randomBytes(32).toString('base64url')}));
    this.j.owners=owners;await this.persist();
    await this.sql.begin(async tx=>{for(const person of owners){
      const [row]=await tx`INSERT INTO waitlist(google_sub,email,name,handle) VALUES(${person.sub},${`${person.role}-${this.j.run}@example.invalid`},${`Cube recovery ${this.j.run}`},${`cubef${this.j.run}${person.role==='owner'?'a':'b'}`}) RETURNING id`;
      person.id=row.id;await tx`DELETE FROM welcome_email WHERE person_id=${row.id}`;
      await tx`DELETE FROM slack_activity WHERE event_key=${`signup:${row.id}`}`;
      await tx`INSERT INTO platform_session(token_hash,waitlist_id) VALUES(${digest(person.token)},${row.id})`;
    }});
    await this.done('owners',{ids:owners.map(p=>p.id)});
    await this.intent('app');
    const app=await this.rt('/v1/apps',{method:'POST',body:{name:`Cube recovery acceptance ${this.j.run}`,external_user_id:`baarcha:${this.owner().id}`,external_project_id:`cube-recovery:${this.j.run}`,runtime_preset:PRESET}});
    need(ULID.test(app.id)&&!app.current_sandbox_id,'Unexpected app creation');this.j.app=app.id;await this.done('app',{id:app.id});
  }
  async resumeRejectedApp() {
    // One reviewed validation rejection only; this is not general retry logic.
    need(this.j.run==='6ff432593177fb12'&&this.owner().id===103&&this.owner('foreign').id===104,'Not the reviewed rejected fixture');
    need(!this.c.allowed_app_id&&!this.j.app&&!this.j.done.app&&!this.j.rejected_app_reconciled&&this.j.done.owners,'Rejected fixture boundary changed');
    need(this.j.pending?.name==='app'&&this.j.pending.at==='2026-09-25T19:01:32.421Z','Original app intent differs');
    const proof=await this.rejectedAppProof();need(proof.exact_project_rows===0&&proof.owner_apps===0&&proof.recorded_http400===true,'Absent app / definitive400 evidence required');
    const current=await this.rt('/v1/apps?external_user_id=baarcha%3A103');need(Array.isArray(current.apps)&&current.apps.length===0,'Owner API inventory not empty');
    const people=await this.sql`SELECT id,google_sub FROM waitlist WHERE id IN (103,104) ORDER BY id`;
    need(people.length===2&&people.every(p=>this.j.owners.some(o=>o.id===p.id&&o.sub===p.google_sub)),'Existing synthetic owner identity changed');
    this.j.rejected_app_reconciled={at:new Date().toISOString(),old_intent:this.j.pending,definitive_rejection:'unknown runtime_preset',old_preset:'node-postgres-standard',corrected_preset:PRESET,proof};
    delete this.j.pending;await this.persist();
    await this.intent('app');
    const app=await this.rt('/v1/apps',{method:'POST',body:{name:`Cube recovery acceptance ${this.j.run}`,external_user_id:'baarcha:103',external_project_id:`cube-recovery:${this.j.run}`,runtime_preset:PRESET}});
    need(ULID.test(app.id)&&!app.current_sandbox_id,'Unexpected app creation');this.j.app=app.id;await this.done('app',{id:app.id});
  }
  async create() {
    need(this.c.allowed_app_id===this.j.app && this.j.done.app && !this.j.sandbox,'Exact reviewed fixture app required');
    const app=await this.checkApp();need(!app.current_sandbox_id,'App already has sandbox; reconcile instead of create');
    await this.intent('sandbox');const created=await this.rt(`/v1/apps/${this.j.app}/sandbox`,{method:'POST',body:{runtime_preset:PRESET},timeout:180000});
    need(ULID.test(created.id),'Missing canonical sandbox ID');this.j.sandbox=created.id;await this.persist();
    const sb=await this.rt(`/v1/sandboxes/${created.id}`);need(sb.runtime_provider==='cube','Provider mismatch: retain fixture; do not continue');
    const binding=await this.inspect(this.j);need(binding.binding?.template_id===this.c.template_id,'Actual binding template differs');
    this.j.binding=binding.binding;await this.done('sandbox',{id:created.id,provider:binding.binding.runtime_id});
  }
  async preview() {
    const sb=await this.rt(`/v1/sandboxes/${this.j.sandbox}`);need(!sb.active_task_id,'Fixture has active task');
    const access=await this.rt(`/v1/sandboxes/${sb.id}/preview-access`,{method:'POST'});
    const origin=validatePreview(sb,access);
    const denied=await this.request('preview','/',{origin,timeout:5000});need([302,401,403,404].includes(denied.status),'Anonymous Cube preview was not denied');
    return (route,opts={})=>{need(route.startsWith('/')&&!route.startsWith('//')&&!route.includes('#'),'Invalid fixture preview path');return this.request('preview',route,{...opts,origin,cookie:`sandbox_preview=${access.token}`});};
  }
  async fund() {
    await this.checkApp();need(this.j.done.sandbox,'Cube fixture required before credit');await this.intent('credit');
    const ref=`grant:operator:cube-recovery:${this.j.run}`;
    await this.sql.begin(async tx=>{
      const rows=await tx`SELECT id FROM waitlist WHERE id=${this.owner().id} AND google_sub=${this.owner().sub} FOR UPDATE`;need(rows.length===1,'Owner changed');
      await tx`INSERT INTO credit_ledger(waitlist_id,ref,kind,millimes,note) VALUES(${this.owner().id},${ref},'grant',${this.c.funding_millimes},'operator test credit: one Cube backup acceptance task') ON CONFLICT(ref) DO NOTHING`;
      const [line]=await tx`SELECT waitlist_id,millimes,kind FROM credit_ledger WHERE ref=${ref}`;need(line?.waitlist_id===this.owner().id&&line.millimes===1000&&line.kind==='grant','Credit reference collision');
    });
    const [balance]=await this.sql`SELECT COALESCE(SUM(millimes),0)::int AS balance FROM credit_ledger WHERE waitlist_id=${this.owner().id}`;
    need(balance.balance>0&&balance.balance<=1000,'Unexpected synthetic credit balance');await this.done('credit',{ref,millimes:1000,balance:balance.balance,hard_spend_cap:false});
  }
  async task() {
    need(this.j.done.credit && this.j.done.sandbox,'Credit/create incomplete');await this.checkApp();await this.preview();
    const before=await this.rt(`/v1/sandboxes/${this.j.sandbox}/tasks`);need(before.tasks?.length===0,'Synthetic task history is not empty');
    await this.intent('task');
    // Same per-project bridge ownership contract as platform bridgeEnvFor.
    // Runtime timeout is explicit120s even if this coordinator is interrupted.
    let token;
    await this.sql.begin(async tx=>{
      const [account]=await tx`SELECT id FROM waitlist WHERE id=${this.owner().id} AND google_sub=${this.owner().sub} FOR UPDATE`;need(account,'Synthetic owner changed');
      const [credit]=await tx`SELECT COALESCE(SUM(millimes),0)::int AS balance FROM credit_ledger WHERE waitlist_id=${this.owner().id}`;need(credit?.balance===1000,'Synthetic credit changed before task');
      const [usage]=await tx`SELECT COALESCE(SUM(u.requests),0)::int AS requests FROM api_usage u JOIN api_key k ON k.id=u.key_id WHERE k.waitlist_id=${this.owner().id}`;need(usage?.requests===0,'Owner already has model usage; no paid replay');
      const existing=await tx`SELECT token,waitlist_id FROM bridge_token WHERE project_id=${this.j.app}`;
      if(existing.length){need(existing.length===1&&existing[0].waitlist_id===this.owner().id,'Bridge owner mismatch');token=existing[0].token;}
      else{token=crypto.randomBytes(32).toString('hex');await tx`INSERT INTO bridge_token(token,project_id,waitlist_id) VALUES(${token},${this.j.app},${this.owner().id})`;}
    });
    need(/^[a-f0-9]{64}$/.test(token),'Unexpected scoped bridge token');
    const bridge=process.env.BRIDGE_PUBLIC_URL;need(bridge&&new URL(bridge).pathname==='/api/bridge','Existing configured platform bridge required');
    const env={BRIDGE_URL:bridge,BRIDGE_TOKEN:token,BRIDGE_PROJECT:this.j.app,ANTHROPIC_CUSTOM_HEADERS:`x-baarcha-bridge: ${token}`,MAX_THINKING_TOKENS:'0'};
    const tr=await runTaskToTerminal({
      submit:()=>this.rt(`/v1/sandboxes/${this.j.sandbox}/tasks`,{method:'POST',body:{prompt:promptFor(this.j.run),agent:'claude-code',model:process.env.SANDBOXD_MODEL||'glm-5.3-flash[1m]',timeout_s:120,continue:false,env},timeout:20000}),
      save:async task=>{this.j.task=task.id;await this.persist();},
      poll:id=>this.rt(`/v1/sandboxes/${this.j.sandbox}/tasks/${id}`,{timeout:5000}),
      cancel:id=>this.rt(`/v1/sandboxes/${this.j.sandbox}/tasks/${id}/cancel`,{method:'POST',timeout:10000})
    });
    await this.done('task',{id:this.j.task,result:tr,result_sha256:digest(JSON.stringify(tr)),runtime_timeout_s:120});
  }
  async verify() {
    need(this.j.done.task&&!this.j.pending,'Completed exact task required');await this.checkApp();
    const preview=await this.preview();let proof;const until=Date.now()+60000;
    do {const r=await preview('/recovery-proof',{timeout:5000});if(r.status===200){proof=r.json;break;}await new Promise(r=>setTimeout(r,500));}while(Date.now()<until);
    need(proof?.app===`cube-recovery-${this.j.run}:app`&&proof.home===`cube-recovery-${this.j.run}:home`&&proof.link==='home marker.txt'&&proof.mode===0o750,'Independent app/home/link/mode proof failed');
    need((await preview('/health')).json?.status==='ok','SQL health failed');
    const html=await preview('/');need(html.status===200&&html.body.includes(Buffer.from(`cube-recovery-${this.j.run}:app`)),'Authored marker not in actual page');
    this.j.proof=proof;await this.persist();
    if(!this.j.done.sql){await this.intent('sql');this.j.sql=[];await this.persist();for(const suffix of ['baseline','latest']){
      const body=`cube-recovery-${this.j.run}:${suffix}`;const r=await preview('/api/notes',{method:'POST',body:{body}});need(r.status===201&&r.json.body===body,'SQL write acknowledgement missing');this.j.sql.push(r.json);await this.persist();
    }await this.done('sql',{rows:this.j.sql});}
    const rows=(await preview('/api/notes')).json;need(Array.isArray(rows),'SQL read failed');for(const expected of this.j.sql)need(rows.some(r=>String(r.id)===String(expected.id)&&r.body===expected.body&&r.created_at===expected.created_at),'Acknowledged SQL row not preserved');
    const tasks=await this.rt(`/v1/sandboxes/${this.j.sandbox}/tasks`);need(tasks.tasks?.some(t=>t.id===this.j.task&&t.can_revert&&t.checkpoint_id),'Checkpoint not available');
    const events=await this.request('runtime',`/v1/sandboxes/${this.j.sandbox}/tasks/${this.j.task}/events`,{timeout:15000,max:16*1024*1024});need(events.status===200&&events.body.length>0&&events.body.includes(Buffer.from('event:')),'Task events unavailable');
    this.j.history={result:this.j.done.task.result,events_sha256:digest(events.body),events_bytes:events.body.length};
    await writePrivate(path.join(this.c.stage,'task-events.sse'),events.body,true);await this.persist();
    if(!this.j.done.capture)await this.capture();
    const binding=await this.inspect(this.j);need(binding.binding?.runtime_id===this.j.binding.runtime_id,'Provider identity changed');
    this.j.metering=await this.sql`SELECT u.endpoint,u.model,u.outcome,u.basis,u.requests,u.input_tokens,u.output_tokens,u.price_book_id FROM api_usage u JOIN api_key k ON k.id=u.key_id WHERE k.waitlist_id=${this.owner().id}`;need(this.j.metering.some(r=>r.requests>0&&r.outcome==='ok'&&r.basis==='measured'&&(r.input_tokens>0||r.output_tokens>0)),'Actual successful owner model metering missing');
    this.j.verification={at:new Date().toISOString(),binding:binding.binding,proof,sql:this.j.sql,history:this.j.history,cover:this.j.cover,actual_browser_visual_review:false,backup:false,restore:false};await this.persist();
  }
  async capture() {
    await this.intent('capture');const r=await this.platform(`/api/projects/${this.j.app}/screenshot?mode=hero`,{method:'POST',timeout:90000,max:16*1024*1024});
    need(r.status===200&&r.body.length>1000&&r.body[0]===255&&r.body[1]===216,'Actual screenshot pixels missing');await writePrivate(path.join(this.c.stage,'private-capture.jpg'),r.body);
    const [app]=await this.sql`SELECT visibility,cover_upload_id FROM published_app WHERE project_id=${this.j.app} AND owner_waitlist_id=${this.owner().id}`;
    need(app?.visibility==='private'&&app.cover_upload_id,'Expected private draft cover');
    const [upload]=await this.sql`SELECT id,project_id,waitlist_id FROM upload WHERE id=${app.cover_upload_id} AND deleted_at IS NULL`;need(upload?.project_id===this.j.app&&upload.waitlist_id===this.owner().id,'Scoped upload mismatch');
    for(const [person,status]of[[this.owner(),200],[this.owner('foreign'),404],[null,404]]){const image=await this.platform(`/files/${encodeURIComponent(upload.id)}`,{person});need(image.status===status,'Private image ACL mismatch');if(status===200)need(digest(image.body)===digest(r.body),'Owner cover bytes differ from actual capture');}
    for(const [person,status]of[[this.owner('foreign'),404],[null,401]]){const denied=await this.platform(`/api/projects/${this.j.app}/screenshot`,{method:'POST',person});need(denied.status===status,'Capture owner authorization mismatch');}
    this.j.cover={id:upload.id,sha256:digest(r.body),private:true,owner_read:true,foreign_denied:true,anonymous_denied:true};await this.done('capture',this.j.cover);
  }
}
async function writePrivate(p,data,identical=false){try{await privatePath(p);if(identical&&digest(await fs.readFile(p))===digest(data))return;throw Error('Artifact already exists; preserve evidence');}catch(e){if(e.code!=='ENOENT')throw e;}const h=await fs.open(p,'wx',0o600);try{await h.writeFile(data);await h.sync();}finally{await h.close();}}

// Main runner intentionally left below pure exported functions for offline tests.
async function main() {
  const args=process.argv.slice(2);need(args.length===6&&args[0]==='--config'&&args[2]==='--action'&&args[4]==='--execute-sha256','Use --config PRIVATE --action prepare|resume-rejected-app|create|fund|task|verify|inspect --execute-sha256 EXACT');
  need(process.platform==='linux'&&process.getuid()===0,'Native host root required');
  need(process.env.CUBE_FIXTURE_LOCKED_PARENT===String(process.ppid),'Use the reviewed Python lock wrapper');
  need(digest(await fs.readFile(fileURLToPath(import.meta.url)))===args[5],'Script hash mismatch');
  await privatePath(args[1]);const c=validateConfig(JSON.parse(await fs.readFile(args[1],'utf8')));
  need(['prepare','resume-rejected-app','create','fund','task','verify','inspect'].includes(args[3]),'Unknown phase');
  await privatePath(c.stage,true);
  const lock=await fs.open(path.join(c.stage,'running.lock'),'wx',0o600);await lock.writeFile(String(process.pid));await lock.sync();
  let sql;try{
    const receipt=await fs.readFile(c.host_cycle_receipt);await privatePath(c.host_cycle_receipt);need(digest(receipt)===c.host_cycle_receipt_sha256,'Host cycle evidence drift');const completed=JSON.parse(receipt);need(completed.phase==='complete'&&completed.real_coordinator_cycle===true&&completed.global_cube_enabled===false,'Actual completed host cycle required');
    const rt=new URL(process.env.SANDBOXD_URL);need(rt.protocol==='http:'&&['127.0.0.1','localhost'].includes(rt.hostname)&&!rt.username&&!rt.password&&!rt.search&&!rt.hash,'Canonical loopback API required');
    need(process.env.SANDBOXD_TOKEN&&process.env.DATABASE_URL,'Operator environment required');
    const {stdout}=await exec('docker',['inspect','src-sandboxd-1'],{timeout:10000,maxBuffer:1024*1024});const [cp]=JSON.parse(stdout);
    need(cp.Id===c.controller_id&&cp.Image===c.controller_image&&cp.State.Running,'Controller identity drift');
    const env=Object.fromEntries(cp.Config.Env.map(x=>[x.slice(0,x.indexOf('=')),x.slice(x.indexOf('=')+1)]));
    need(env.SANDBOXD_CUBE_ROLLOUT!=='global','Global routing forbidden for operator fixture');
    if(['prepare','resume-rejected-app'].includes(args[3])||(args[3]==='inspect'&&env.SANDBOXD_CUBE_ENABLED==='false'))need(env.SANDBOXD_CUBE_ENABLED==='false','Prepare requires Cube disabled');
    else {const admission=JSON.parse(env.SANDBOXD_CUBE_ADMISSION||'null');need(admission?.max_active===4&&admission.cpu_count===2&&admission.memory_mb===2048&&admission.writable_disk_mb===10240&&admission.storage_guard,'Reviewed four-slot storage-guard admission required');need(env.SANDBOXD_CUBE_ENABLED==='true'&&env.SANDBOXD_CUBE_APP_IDS===c.allowed_app_id,'Exact single-app allowlist required');need(JSON.parse(env.SANDBOXD_CUBE_TEMPLATES)[PRESET]===c.template_id,'Template config drift');}
    const rev=(await exec('git',['-C','/opt/baarcha/app','rev-parse','HEAD'],{timeout:5000})).stdout.trim();need(rev===c.platform_revision,'Platform release drift');
    // Real source34 enrollment and explicit coordinator receipt are mandatory.
    const stop='/usr/local/libexec/baarcha-cube-worker-stop',start='/usr/local/libexec/baarcha-cube-worker-start';
    need(digest(await fs.readFile(stop))===c.stop_binary_sha256&&digest(await fs.readFile(start))===c.start_binary_sha256,'Coordinator rebuild/pin mismatch');
    const inspect=async j=>{
      const script=`import sqlite3,json,sys\nc=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True);c.row_factory=sqlite3.Row;c.execute('BEGIN');c.execute('SELECT * FROM cube_storage_policy LIMIT 1');\nassert sys.argv[2]=='inspect' or c.execute(\"SELECT count(*) FROM cube_recovery WHERE phase<>'complete'\").fetchone()[0]==0\nassert sys.argv[2]=='inspect' or c.execute(\"SELECT count(*) FROM cube_admission WHERE state='pending'\").fetchone()[0]==0\nrows=[dict(x) for x in c.execute('SELECT sandbox_id,runtime_id,template_id,domain,config_revision,config_applied_revision FROM runtime_binding')];assert len(rows)<=1\nassert not rows or rows[0]['sandbox_id']==sys.argv[1]\nbaseline=[dict(x) for x in c.execute(\"SELECT id,runtime_provider FROM sandbox ORDER BY id\") if x['id']!=sys.argv[1]]\nprint(json.dumps({'binding':rows[0] if rows else None,'baseline':baseline}));c.rollback()`;
      const out=await exec('python3',['-c',script,j.sandbox||'',args[3]],{timeout:10000,maxBuffer:65536});return JSON.parse(out.stdout);
    };
    const jp=path.join(c.stage,'journal.json');let j;
    try{await privatePath(jp);j=JSON.parse(await fs.readFile(jp,'utf8'));}catch(e){if(e.code!=='ENOENT')throw e;need(args[3]==='prepare','Missing fixture journal');j={version:1,run:crypto.randomBytes(8).toString('hex'),created_at:new Date().toISOString(),done:{}};await atomic(jp,j);}
    need(j.version===1&&/^[a-f0-9]{16}$/.test(j.run),'Invalid fixture journal');if(j.app&&c.allowed_app_id)need(j.app===c.allowed_app_id,'Allowed app differs from journal');
    const current=await inspect(j);
    if(j.baseline){for(const old of j.baseline)need(current.baseline.some(row=>row.id===old.id&&row.runtime_provider===old.runtime_provider),'Existing sandbox baseline changed');j.new_baseline_additions=current.baseline.filter(row=>!j.baseline.some(old=>old.id===row.id));}else{need(args[3]==='prepare','Baseline missing');j.baseline=current.baseline;await atomic(jp,j);}
    const require=createRequire(path.join(c.platform_root,'package.json'));sql=require('postgres')(process.env.DATABASE_URL,{max:1,connect_timeout:10,idle_timeout:5,connection:{statement_timeout:30000}});
    const persist=()=>atomic(jp,j);
    const request=async(kind,route,opts={})=>{
      const url=new URL(route,kind==='runtime'?rt:kind==='platform'?ORIGIN:opts.origin);
      const headers=kind==='runtime'?{authorization:`Bearer ${process.env.SANDBOXD_TOKEN}`}:kind==='platform'?(opts.person?{cookie:`punicas_platform=${opts.person.token}`} : {}):(opts.cookie?{cookie:opts.cookie}:{});
      if(opts.body!==undefined)headers['content-type']='application/json';
      const r=await fetch(url,{method:opts.method||'GET',redirect:'manual',headers,body:opts.body===undefined?undefined:JSON.stringify(opts.body),signal:AbortSignal.timeout(opts.timeout||15000)});
      const body=await boundedBody(r,opts.max);let json;try{json=JSON.parse(body);}catch{}return{status:r.status,body,json};
    };
    const rejectedAppProof=async()=>{
      const script="import contextlib,sqlite3,json\nwith contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as c:\n c.execute('BEGIN'); print(json.dumps({'exact_project_rows':c.execute('SELECT count(*) FROM app WHERE external_project_id=?',('cube-recovery:6ff432593177fb12',)).fetchone()[0],'owner_apps':c.execute('SELECT count(*) FROM app WHERE external_user_id=?',('baarcha:103',)).fetchone()[0]}))";
      const output=await exec('python3',['-c',script],{timeout:10000,maxBuffer:4096});const proof=JSON.parse(output.stdout);
      const names=(await fs.readdir(c.stage)).filter(n=>/^failure-[0-9]+\.json$/.test(n));need(names.length>0&&names.length<=10,'Missing bounded rejection evidence');
      const failures=[];for(const name of names){const p=path.join(c.stage,name);await privatePath(p);failures.push(JSON.parse(await fs.readFile(p,'utf8')));}
      proof.recorded_http400=failures.some(e=>e.action==='prepare'&&e.pending==='app'&&e.message==='Runtime HTTP400');return proof;
    };
    const f=new Fixture(c,j,{request,sql,persist,inspect,rejectedAppProof});
    if(args[3]==='inspect'){console.log(JSON.stringify({phase:j.pending?.name||'idle',app:j.app||null,sandbox:j.sandbox||null,task:j.task||null,done:Object.keys(j.done),verification:!!j.verification}));return;}
    need(!j.pending||args[3]==='resume-rejected-app','Pending mutation retained; inspect/reconcile manually before further work');
    try {await f[args[3]==='resume-rejected-app'?'resumeRejectedApp':args[3]]();} catch(error) {await atomic(path.join(c.stage,`failure-${Date.now()}.json`),{at:new Date().toISOString(),action:args[3],pending:j.pending?.name||null,error_class:error?.name||'Error',message:error?.name==='AssertionError'?String(error.message).slice(0,300):'bounded fixture operation failed; retain journal'});throw error;}console.log(JSON.stringify({action:args[3],completed:true,app:j.app||null,sandbox:j.sandbox||null,retained_for_backup:true}));
  } finally {if(sql)await sql.end({timeout:5});await lock.close();await fs.unlink(path.join(c.stage,'running.lock'));}
}
if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url))main().catch(()=>{console.error('Fixture stopped; inspect the private journal. No automatic retry or cleanup.');process.exitCode=1;});
