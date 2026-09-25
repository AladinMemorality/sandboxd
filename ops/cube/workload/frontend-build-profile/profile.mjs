// Operator coordinator: one owned fixture, no task submission or model access.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';
const exec=promisify(execFile), hash=b=>crypto.createHash('sha256').update(b).digest('hex');
const APP='01M3CZB4HXT2Y8HP8CEY75PCWY',SB='01M3D1Q0E1KM1FEM244XVHEC65';
const TASKS=['01M3D27V0SHQ863WHPCAENGVTY','01M3D30ENCD5BWQK4WN4DGV371','01M3D3Z2T9CM6WSW9QB311KXB7','01M3D5JY2K1WDBJW19N33ZHBP8'];
export const sha256=hash;
export function terminalReport(report,nonce){
  assert.equal(report?.run,nonce);assert.equal(report.version,2);
  assert(Array.isArray(report.phases)&&report.phases.some(p=>p.name==='owned_children_stopped'),'Children not confirmed stopped');
  assert(report.phases.some(p=>p.name==='finished'||p.name==='failed'),'No terminal phase');
  if(report.successful){assert.equal(report.build?.exit_code,0);assert.equal(report.build?.timed_out,false);assert.equal(report.ready?.api_valid,true);assert.equal(report.hmr?.transformed_marker_verified,true);assert.equal(report.load?.requests,64);assert.equal(report.load?.failures,0);assert.match(report.dist_index_sha256,/^[a-f0-9]{64}$/);}
  return report.successful===true;
}
export async function runProfile({j,save,ops,nonce,original,candidate,files,now=Date.now,sleep=ms=>new Promise(r=>setTimeout(r,ms)),pollMs=500000}){
  assert(!j.pending&&!j.started,'Existing execution journal is never replayed');
  const mutate=async(name,fn)=>{assert(!j.pending);j.pending={name,at:new Date().toISOString()};await save();const value=await fn();j.done[name]=true;j.pending=null;await save();return value;};
  j.started=true;j.done={};await save();
  await ops.guard();
  assert.equal(hash(await ops.read('sandbox.yaml')),hash(original),'Original manifest drift');
  for(const [name,body] of Object.entries(files)){
    const relative=`.operator-frontend-build-20260925/${nonce}/input/${name}`;
    assert.equal(await ops.read(relative,true),null,'Stage already exists');
    await mutate('upload-'+name,()=>ops.write(relative,body));
    assert.equal(hash(await ops.read(relative)),hash(body),'Staged source changed');
  }
  assert.equal(await ops.read(`.operator-frontend-build-20260925/${nonce}/output/report.json`,true),null,'Prior workload report exists');
  await ops.guard();assert.equal(hash(await ops.read('sandbox.yaml')),hash(original),'Manifest changed before activation');
  await mutate('install-manifest',()=>ops.write('sandbox.yaml',candidate));
  assert.equal(hash(await ops.read('sandbox.yaml')),hash(candidate));
  await mutate('activate-reload',()=>ops.reload());
  const until=now()+pollMs;let report;
  while(now()<until){
    try{const raw=await ops.read(`.operator-frontend-build-20260925/${nonce}/output/report.json`,true);if(raw){const parsed=JSON.parse(raw);if(parsed.phases?.some(p=>p.name==='owned_children_stopped')){terminalReport(parsed,nonce);report=parsed;break;}}}catch{/* Read-only polls may span a supervisor restart. */}
    await sleep(1000);
  }
  assert(report,'Unknown workload completion: retain manifest/journal, do not replay reload');
  j.report=report;j.workload_successful=terminalReport(report,nonce);await save();
  try{await ops.archive();}catch{j.archive_error=true;await save();}
  await ops.guard();assert.equal(hash(await ops.read('sandbox.yaml')),hash(candidate),'Manifest drift: do not overwrite');
  await mutate('restore-manifest',()=>ops.write('sandbox.yaml',original));
  assert.equal(hash(await ops.read('sandbox.yaml')),hash(original));
  await mutate('restore-reload',()=>ops.reload());
  await ops.restoredHealth();j.restored=true;j.finished_at=new Date().toISOString();await save();
  return j.workload_successful&&!j.archive_error;
}
async function privateFile(p,max=4*1024*1024){assert.equal(await fs.realpath(p),p);const st=await fs.lstat(p);assert(st.isFile()&&st.uid===0&&(st.mode&0o777)===0o600&&st.size<=max);return fs.readFile(p);}
async function writeNew(p,b){const h=await fs.open(p,'wx',0o600);try{await h.writeFile(b);await h.sync();}finally{await h.close();}const d=await fs.open(path.dirname(p),'r');try{await d.sync();}finally{await d.close();}}
async function persist(p,j){const tmp=p+'.tmp';await writeNew(tmp,JSON.stringify(j,null,2)+'\n');await fs.rename(tmp,p);const d=await fs.open(path.dirname(p),'r');try{await d.sync();}finally{await d.close();}}
async function bounded(response,max){if(!response.body)return Buffer.alloc(0);const chunks=[];let bytes=0;for await(const c of response.body){bytes+=c.length;assert(bytes<=max,'Response exceeds limit');chunks.push(c);}return Buffer.concat(chunks);}
async function main(){
  assert(process.platform==='linux'&&process.getuid()===0);assert.equal(process.env.CUBE_FRONTEND_PROFILE_LOCKED_PARENT,String(process.ppid));
  assert(['--check','--execute'].includes(process.argv[3])&&process.argv.length===4);
  const cfg=JSON.parse(await privateFile(process.argv[2]));
  assert.equal(cfg.app_id,APP);assert.equal(cfg.sandbox_id,SB);assert.match(cfg.run,/^[a-f0-9]{16}$/);
  assert.equal(cfg.controller_id,'93169a6a71c6d1e35785683418e0c0421674b2fc48baaf973005d542f9c11914');
  assert.equal(cfg.controller_image,'sha256:74f76d6b6db87b4c710c4c4fbf57db463e15e16f30be6e2abb60ee11fd5119ee');
  assert.equal(cfg.platform_revision,'1b4e9ac2a02dde0e51021dbe09898548860df4a1');
  assert.equal(cfg.provider_id,'095a0076b90b4b009ab995837909e276');assert.equal(cfg.template_id,'tpl-ce1ee426e686460bbc8c3bfc');
  assert.equal(hash(await fs.readFile(fileURLToPath(import.meta.url))),cfg.profile_sha256);
  assert(cfg.stage.startsWith('/opt/baarcha-bench/cube-frontend-profile-')&&path.normalize(cfg.stage)===cfg.stage);
  assert.equal(await fs.realpath(cfg.stage),cfg.stage);const st=await fs.lstat(cfg.stage);assert(st.isDirectory()&&st.uid===0&&(st.mode&0o777)===0o700);
  const base=new URL(process.env.SANDBOXD_URL);assert(base.protocol==='http:'&&['localhost','127.0.0.1'].includes(base.hostname));
  async function rt(route,{body,method='GET',raw=false,missing=false,timeout=15000,max=4*1024*1024}={}){
    const r=await fetch(new URL(route,base),{method,redirect:'error',headers:{authorization:`Bearer ${process.env.SANDBOXD_TOKEN}`,...(body?{'content-type':Buffer.isBuffer(body)?'application/octet-stream':'application/json'}:{})},body:body?Buffer.isBuffer(body)?body:JSON.stringify(body):undefined,signal:AbortSignal.timeout(timeout)});
    if(missing&&r.status===404){await r.body?.cancel();return null;}assert(r.ok,`Runtime HTTP${r.status}`);const b=await bounded(r,max);return raw?b:b.length?JSON.parse(b):null;
  }
  const read=(name,missing=false)=>rt(`/v1/sandboxes/${SB}/files/content?path=${encodeURIComponent(name)}`,{raw:true,missing,max:2*1024*1024});
  async function guard(){
    const cp=JSON.parse((await exec('docker',['inspect','src-sandboxd-1'],{timeout:10000,maxBuffer:1024*1024})).stdout)[0];assert(cp.State.Running&&cp.Id===cfg.controller_id&&cp.Image===cfg.controller_image);
    const env=Object.fromEntries(cp.Config.Env.map(s=>s.split(/=(.*)/s).slice(0,2)));assert.equal(env.SANDBOXD_CUBE_APP_IDS,APP);assert.equal(env.SANDBOXD_CUBE_ENABLED,'true');assert.equal(env.SANDBOXD_CUBE_ROLLOUT,'allowlist');
    assert.equal((await exec('git',['-C','/opt/baarcha/app','rev-parse','HEAD'],{timeout:5000})).stdout.trim(),cfg.platform_revision);
    const app=await rt(`/v1/apps/${APP}`);assert.equal(app.external_user_id,'baarcha:103');assert.equal(app.external_project_id,'cube-recovery:6ff432593177fb12');assert.equal(app.current_sandbox_id,SB);
    const sb=await rt(`/v1/sandboxes/${SB}`);assert.equal(sb.runtime_provider,'cube');assert.equal(sb.status,'running');assert(!sb.active_task_id);
    const script=`import json,sqlite3,sys\nc=sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True);c.row_factory=sqlite3.Row;c.execute('BEGIN');r=c.execute('SELECT runtime_id,template_id,config_revision,config_applied_revision FROM runtime_binding WHERE sandbox_id=?',(sys.argv[1],)).fetchall();n=c.execute("SELECT count(*) FROM task WHERE sandbox_id=? AND status IN ('queued','running')",(sys.argv[1],)).fetchone()[0];print(json.dumps({'binding':[dict(x) for x in r],'active':n}));c.rollback();c.close()`;
    const actual=JSON.parse((await exec('python3',['-c',script,SB],{timeout:10000,maxBuffer:4096})).stdout);assert.equal(actual.active,0);assert.equal(actual.binding.length,1);assert.equal(actual.binding[0].runtime_id,cfg.provider_id);assert.equal(actual.binding[0].template_id,cfg.template_id);assert.equal(actual.binding[0].config_revision,actual.binding[0].config_applied_revision);
    const list=await rt(`/v1/sandboxes/${SB}/tasks`);assert.equal(list.tasks.length,TASKS.length);assert.deepEqual(list.tasks.map(t=>t.id).sort(),[...TASKS].sort());
    const history=[];for(const id of TASKS){const t=await rt(`/v1/sandboxes/${SB}/tasks/${id}`);assert(['succeeded','failed','cancelled'].includes(t.status));history.push({id,status:t.status,checkpoint:t.checkpoint_id,finished_at:t.finished_at});}
    return {sb,history_sha256:hash(JSON.stringify(history)),server_sha256:hash(await read('server.mjs'))};
  }
  async function health(sb){
    const access=await rt(`/v1/sandboxes/${SB}/preview-access`,{method:'POST'});const origin=new URL(sb.preview.url);
    assert.equal(origin.href,`https://s-${SB.toLowerCase()}-3000.preview.65.108.225.153.sslip.io/`);assert.equal(new URL(access.url).href,origin.href);const handoff=new URL(access.access_url);assert.equal(handoff.origin,origin.origin);assert.equal(handoff.pathname,'/__sandboxd/preview-auth');assert(typeof access.token==='string'&&/^[\x21-\x7e]{1,4096}$/.test(access.token)&&!/[;,]/.test(access.token));
    async function get(route){const r=await fetch(new URL(route,origin),{headers:{cookie:`sandbox_preview=${access.token}`},redirect:'error',signal:AbortSignal.timeout(5000)});assert.equal(r.status,200);return JSON.parse(await bounded(r,1024*1024));}
    assert.equal((await get('/health')).status,'ok');const notes=await get('/api/notes');assert(Array.isArray(notes));notes.sort((a,b)=>String(a.id).localeCompare(String(b.id)));return {notes_count:notes.length,notes_sha256:hash(JSON.stringify(notes)),pg_health:true};
  }
  const before=await guard();const beforeHealth=await health(before.sb);const original=await read('sandbox.yaml');
  const backup=path.join(cfg.stage,'original-read.yaml');
  if(await fs.access(backup).then(()=>true,()=>false))assert.equal(hash(await privateFile(backup)),hash(original),'Manifest changed since preparation');else await writeNew(backup,original);
  const prepared=path.join(cfg.stage,'prepared');const source=path.dirname(fileURLToPath(import.meta.url));
  if(!await fs.access(prepared).then(()=>true,()=>false))await exec('python3',[path.join(source,'prepare.py'),'--original',backup,'--out',prepared,'--run',cfg.run],{timeout:10000,maxBuffer:4096});
  const plan=JSON.parse(await privateFile(path.join(prepared,'plan.json')));assert.equal(plan.run,cfg.run);const candidate=await privateFile(path.join(prepared,'candidate.yaml'));assert.equal(hash(original),plan.files['original.yaml']);assert.equal(hash(candidate),plan.files['candidate.yaml']);
  const files={};for(const name of ['guest.py','probe.mjs']){files[name]=await privateFile(path.join(prepared,name));assert.equal(hash(files[name]),cfg.sources[name]);assert.equal(hash(files[name]),plan.files[name]);}
  const prior=await rt('/v1/runtime/manifest/validate',{method:'POST',body:{manifest:original.toString()}}),next=await rt('/v1/runtime/manifest/validate',{method:'POST',body:{manifest:candidate.toString()}});
  assert(prior.valid&&next.valid);assert.deepEqual(next.effective,{...prior.effective,workers:[...(prior.effective.workers||[]),{name:'operator_frontend_profile',command:plan.command}]});
  const evidence={checked_at:new Date().toISOString(),controller:cfg.controller_id,platform_revision:cfg.platform_revision,app:APP,sandbox:SB,run:cfg.run,history_sha256:before.history_sha256,server_sha256:before.server_sha256,...beforeHealth,original_manifest_sha256:hash(original),candidate_manifest_sha256:hash(candidate),validation_passed:true,guest_mutation:false};
  await writeNew(path.join(cfg.stage,`check-${Date.now()}.json`),JSON.stringify(evidence,null,2)+'\n');
  if(process.argv[3]==='--check'){console.log(JSON.stringify(evidence));return;}
  const jp=path.join(cfg.stage,'profile.json');assert(!await fs.access(jp).then(()=>true,()=>false),'Retain old journal; no replay');
  const j={version:1,run:cfg.run,app:APP,sandbox:SB,before:evidence};await writeNew(jp,JSON.stringify(j,null,2)+'\n');
  const archive=async()=>{for(const name of ['report.json','ready.json','warm.json','load.json','hmr.json','build.log','ready.log','warm.log','hmr.log']){const b=await read(`.operator-frontend-build-20260925/${cfg.run}/output/${name}`,true);if(b)await writeNew(path.join(cfg.stage,'guest-'+name),b);}};
  const okay=await runProfile({j,save:()=>persist(jp,j),nonce:cfg.run,original,candidate,files,ops:{read,guard,
    write:(name,body)=>rt(`/v1/sandboxes/${SB}/files?path=${encodeURIComponent(name)}`,{method:'PUT',body}),
    reload:()=>rt(`/v1/sandboxes/${SB}/recreate`,{method:'POST',body:{reload_manifest:true},timeout:120000}),archive,
    restoredHealth:async()=>{let last;const until=Date.now()+60000;while(Date.now()<until){try{const after=await guard();assert.equal(after.history_sha256,before.history_sha256);assert.equal(after.server_sha256,before.server_sha256);assert.deepEqual(await health(after.sb),beforeHealth);j.after_verified=true;return;}catch(e){last=e;await new Promise(r=>setTimeout(r,1000));}}throw last;}}});
  console.log(JSON.stringify({restored:j.restored,workload_successful:okay,report_saved:true}));assert(okay,'Workload failed; original manifest restored, evidence retained');
}
if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url))main().catch(()=>{console.error('Frontend profile stopped; retain private journal. No automatic mutation replay.');process.exitCode=1;});
