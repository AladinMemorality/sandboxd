// Explicit operator coding measurement; never changes rollout or grants credit.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
const exec=promisify(execFile);
const APP='01M3CZB4HXT2Y8HP8CEY75PCWY', SB='01M3D1Q0E1KM1FEM244XVHEC65';
export const priorTasks={
  '01M3D27V0SHQ863WHPCAENGVTY':['agent_timeout','e3d133d81550b2ce085720f05aee367174f7bc9c'],
  '01M3D30ENCD5BWQK4WN4DGV371':['agent_error','8c755391b2f3981b1c33f410654152716bddbb32'],
  '01M3D3Z2T9CM6WSW9QB311KXB7':['agent_timeout','0526e55ca5033079bbc3cbd1f798e8b18f5deba6'],
};
export function validateHistory(rows) {
  assert.equal(rows.length,3,'Task history changed');
  assert.equal(new Set(rows.map(t=>t.id)).size,3,'Duplicate task history');
  for(const t of rows){assert(priorTasks[t.id],'Unexpected task');assert.equal(t.status,'failed');assert.equal(t.failure_reason,priorTasks[t.id][0]);assert.equal(t.checkpoint_id,priorTasks[t.id][1]);}
}
export function thinkingEnv(raw) {
  const n=raw===undefined||raw.trim()===''?2000:Number(raw);
  const budget=Number.isFinite(n)?Math.max(0,Math.min(32000,n)):2000;
  return budget?{MAX_THINKING_TOKENS:String(budget)}:{};
}
export const prompt=`Add a small real feature to this existing Node/PostgreSQL notes app: a search box that filters notes through GET /api/notes?query=.... Preserve existing notes, the create-note form, and all recovery-fixture files. Use a parameterized SQL query; reject query values longer than100 characters with HTTP400. Keep unfiltered requests working. Show loading, empty and error states in the existing page. Add focused Node built-in tests for query validation and run them plus node --check server.mjs. Do not install dependencies, alter database schema, delete rows, deploy, or inspect credentials. Read each relevant source file once, make the edits, run checks, and finish. This is a bounded operator performance measurement on its own test project.`;
const hash=b=>crypto.createHash('sha256').update(b).digest('hex');
async function privateFile(p){assert.equal(await fs.realpath(p),p);const s=await fs.lstat(p);assert(s.isFile()&&s.uid===0&&(s.mode&0o777)===0o600);return fs.readFile(p);}
async function persist(p,j){const tmp=p+'.tmp';const h=await fs.open(tmp,'wx',0o600);try{await h.writeFile(JSON.stringify(j,null,2)+'\n');await h.sync();}finally{await h.close();}await fs.rename(tmp,p);const d=await fs.open(path.dirname(p),'r');try{await d.sync();}finally{await d.close();}}

export async function runTask({request,save,j,sleep=ms=>new Promise(r=>setTimeout(r,ms)),now=Date.now,deadlineMs=660000}) {
  assert(!j.pending&&!j.task,'No submission replay');
  j.pending={phase:'coding-task-submit',at:new Date().toISOString()};await save();
  let terminal=false;const until=now()+deadlineMs;
  try {
    const t=await request('submit');assert(/^[0-9A-HJKMNP-TV-Z]{26}$/.test(t.id),'Missing task acknowledgement');
    j.task=t.id;await save();
    while(now()<until){const t=await request('poll',j.task);assert(['running','queued','succeeded','failed','cancelled'].includes(t.status),'Unknown task state');if(!['running','queued'].includes(t.status)){terminal=true;j.result=t;j.completed_at=new Date().toISOString();delete j.pending;await save();return t;}await sleep(1000);}
    throw Error('Operator task watchdog elapsed');
  } finally {
    if(j.task&&!terminal){try{await request('cancel',j.task);}catch{}/* unresolved intent retained */}
  }
}

async function main(){
  assert(process.platform==='linux'&&process.getuid()===0,'Linux root required');
  assert.equal(process.env.CUBE_CODING_PROFILE_LOCKED_PARENT,String(process.ppid),'Operator wrapper required');
  const cfg=JSON.parse(await privateFile(process.argv[2]));
  assert.equal(cfg.app_id,APP);assert.equal(cfg.sandbox_id,SB);
  assert.equal(cfg.provider_id,'095a0076b90b4b009ab995837909e276');assert.equal(cfg.template_id,'tpl-ce1ee426e686460bbc8c3bfc');
  assert.equal(cfg.controller_id,'2d8604031c475ba00d85120ebf39f7b14351252fbc87eb624a68445491c47850');
  assert.equal(cfg.platform_revision,'1b4e9ac2a02dde0e51021dbe09898548860df4a1');
  assert.equal(cfg.task_sha256,hash(await fs.readFile(fileURLToPath(import.meta.url))));
  assert.equal(cfg.prompt_sha256,hash(prompt));
  assert(cfg.stage.startsWith('/opt/baarcha-bench/cube-coding-task-')&&path.normalize(cfg.stage)===cfg.stage);
  assert.equal(await fs.realpath(cfg.stage),cfg.stage);const st=await fs.lstat(cfg.stage);assert(st.isDirectory()&&st.uid===0&&(st.mode&0o777)===0o700);
  const jp=path.join(cfg.stage,'task.json');assert.equal(await fs.access(jp).then(()=>true,()=>false),false,'Retain existing evidence');
  const cp=JSON.parse((await exec('docker',['inspect','src-sandboxd-1'],{timeout:10000})).stdout)[0];
  assert(cp.State.Running&&cp.Id===cfg.controller_id&&cp.Image===cfg.controller_image,'Controller changed');
  const env=Object.fromEntries(cp.Config.Env.map(s=>s.split(/=(.*)/s).slice(0,2)));
  assert.equal(env.SANDBOXD_CUBE_APP_IDS,APP);assert.equal(env.SANDBOXD_CUBE_ENABLED,'true');assert.equal(env.SANDBOXD_CUBE_ROLLOUT,'allowlist');
  assert.equal((await exec('git',['-C','/opt/baarcha/app','rev-parse','HEAD'])).stdout.trim(),cfg.platform_revision);
  const base=new URL(process.env.SANDBOXD_URL);assert(base.protocol==='http:'&&['127.0.0.1','localhost'].includes(base.hostname));
  async function rt(route,body){const r=await fetch(new URL(route,base),{method:body?'POST':'GET',headers:{authorization:`Bearer ${process.env.SANDBOXD_TOKEN}`,...(body?{'content-type':'application/json'}:{})},body:body?JSON.stringify(body):undefined,redirect:'error',signal:AbortSignal.timeout(15000)});assert(r.ok,`Runtime HTTP${r.status}`);const chunks=[];let size=0;const reader=r.body.getReader();try{for(;;){const {done,value}=await reader.read();if(done)break;size+=value.length;assert(size<=4*1024*1024,'Runtime response limit');chunks.push(value);}}catch(e){await reader.cancel();throw e;}return JSON.parse(Buffer.concat(chunks).toString());}
  const app=await rt(`/v1/apps/${APP}`);assert.equal(app.external_user_id,'baarcha:103');assert.equal(app.external_project_id,'cube-recovery:6ff432593177fb12');
  const sb=await rt(`/v1/sandboxes/${SB}`);assert.equal(sb.runtime_provider,'cube');assert.equal(sb.status,'running');
  const inspect=`import contextlib,json,sqlite3,sys\nwith contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as c:\n c.row_factory=sqlite3.Row;c.execute('BEGIN');r=c.execute('SELECT runtime_id,template_id,config_revision,config_applied_revision FROM runtime_binding WHERE sandbox_id=?',(sys.argv[1],)).fetchall();n=c.execute(\"SELECT count(*) FROM task WHERE sandbox_id=? AND status IN ('queued','running')\",(sys.argv[1],)).fetchone()[0];print(json.dumps({'binding':[dict(x) for x in r],'active_tasks':n}));c.rollback()`;
  const live=JSON.parse((await exec('python3',['-c',inspect,SB],{timeout:10000,maxBuffer:4096})).stdout);
  assert.equal(live.active_tasks,0);assert.equal(live.binding.length,1);assert.equal(live.binding[0].runtime_id,cfg.provider_id);assert.equal(live.binding[0].template_id,cfg.template_id);assert.equal(live.binding[0].config_revision,live.binding[0].config_applied_revision);
  const all=await rt(`/v1/sandboxes/${SB}/tasks`);const rows=[];for(const t of all.tasks||[])rows.push(await rt(`/v1/sandboxes/${SB}/tasks/${t.id}`));validateHistory(rows);
  const require=createRequire('/opt/baarcha/app/landing/package.json');const sql=require('postgres')(process.env.DATABASE_URL,{max:1,connect_timeout:5,connection:{statement_timeout:10000}});
  const {creditState}=await import('file:///opt/baarcha/app/landing/src/lib/platform/credit.ts');const {closePg}=await import('file:///opt/baarcha/app/landing/src/lib/pg.ts');
  try {
    const credit=await creditState(103);assert(credit.available>=1500,'Insufficient remaining operator test allowance');
    const [bridge]=await sql`SELECT token,waitlist_id FROM bridge_token WHERE project_id=${APP}`;assert(bridge?.waitlist_id===103&&/^[a-f0-9]{64}$/.test(bridge.token));
    const url=new URL(process.env.BRIDGE_PUBLIC_URL);assert.equal(url.origin,'https://baarcha.tn');assert.equal(url.pathname,'/api/bridge');
    const model=process.env.SANDBOXD_MODEL?.trim()||'glm-5.3-flash[1m]',agent=process.env.SANDBOXD_AGENT?.trim()||'claude-code';
    const j={version:1,app:APP,sandbox:SB,prior_tasks:Object.keys(priorTasks),prompt_sha256:hash(prompt),started_at:new Date().toISOString(),model,agent,thinking:thinkingEnv(process.env.SANDBOXD_MAX_THINKING_TOKENS),runtime_timeout_s:600,credit_before:credit,credit_granted:0};
    if(process.argv[3]==='--check'){console.log(JSON.stringify({preflight:true,app:APP,sandbox:SB,model,agent,thinking:j.thinking,available_credit_millimes:credit.available,submitted:false}));return;}
    assert.equal(process.argv.length,3,'Unexpected action');
    await persist(jp,j);
    const env={...j.thinking,BRIDGE_URL:url.href,BRIDGE_TOKEN:bridge.token,BRIDGE_PROJECT:APP,ANTHROPIC_CUSTOM_HEADERS:`x-baarcha-bridge: ${bridge.token}`};
    const result=await runTask({j,save:()=>persist(jp,j),request:(kind,id)=>kind==='submit'?rt(`/v1/sandboxes/${SB}/tasks`,{prompt,model,agent,continue:false,timeout_s:600,env}):rt(`/v1/sandboxes/${SB}/tasks/${id}${kind==='cancel'?'/cancel':''}`,kind==='cancel'?{}:undefined)});
    j.credit_after=await creditState(103);await persist(jp,j);
    console.log(JSON.stringify({task:j.task,status:result.status,duration_ms:result.duration_ms,files_changed:result.files_changed?.length||0,accepted:false,reason:'Independent feature verification and profile analysis remain required'}));
  }finally{await sql.end({timeout:5});await closePg();}
}
if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url))main().catch(()=>{console.error('Coding profile refused or failed; retain private journal, no automatic replay.');process.exitCode=1;});
