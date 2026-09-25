// Native-host coordinator. No task submission, credit mutation or remote exec.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {OperatorFixture,APP,SANDBOX,digest} from './fixture.mjs';
import {boundedBody,validatePreview} from '../canonical-fixture/fixture.mjs';
const exec=promisify(execFile),need=(x,m)=>assert(x,m),here=path.dirname(fileURLToPath(import.meta.url));
async function privatePath(p,dir=false){const s=await fs.lstat(p);need(path.isAbsolute(p)&&await fs.realpath(p)===p&&s.uid===0&&(s.mode&0o777)===(dir?0o700:0o600)&&(dir?s.isDirectory():s.isFile()&&s.nlink===1&&s.size<=32*1024*1024),'Unsafe private path');}
async function durable(p,bytes,replace=false){await privatePath(path.dirname(p),true);const tmp=replace?p+'.new':p;const f=await fs.open(tmp,'wx',0o600);try{await f.writeFile(bytes);await f.sync();}finally{await f.close();}if(replace){await privatePath(p);await fs.rename(tmp,p);}const d=await fs.open(path.dirname(p),'r');try{await d.sync();}finally{await d.close();}}
async function main(){
 need(process.platform==='linux'&&process.getuid()===0&&process.env.CUBE_OPERATOR_RECOVERY_PARENT===String(process.ppid),'Use reviewed native-root lock wrapper');
 const [configPath,action]=process.argv.slice(2);need(['prepare','install','restoreManifest','verify','inspect'].includes(action),'Unknown action');await privatePath(configPath);
 const c=JSON.parse(await fs.readFile(configPath));need(c.version===1&&c.app_id===APP&&c.sandbox_id===SANDBOX,'Only exact owned canary');
 need(c.stage.startsWith('/opt/baarcha-bench/cube-operator-recovery-')&&path.normalize(c.stage)===c.stage,'Private operator stage');await privatePath(c.stage,true);
 need(digest(await fs.readFile(path.join(here,'../canonical-fixture/fixture.mjs')))===c.canonical_fixture_sha256,'Canonical transport helper changed');
 for(const name of ['main.mjs','fixture.mjs','home-worker.mjs'])need(digest(await fs.readFile(path.join(here,name)))===c.source_sha256[name],'Source code changed');
 await privatePath(c.original_journal);need(c.original_journal!==path.join(c.stage,'journal.json'),'Original journal must never be reused');
 for(const value of [c.original_journal_sha256,c.task_inventory_sha256,c.controller_id,...Object.values(c.before_sha256||{})])need(/^[a-f0-9]{64}$/.test(value),'Missing reviewed identity/hash');need(/^sha256:[a-f0-9]{64}$/.test(c.controller_image)&&/^[a-f0-9]{32}$/.test(c.runtime_id),'Provider/controller image identity');need(['sandbox.yaml','server.mjs','public/index.html'].every(x=>c.before_sha256[x]),'All original source hashes required');
 const originalBytes=await fs.readFile(c.original_journal);need(digest(originalBytes)===c.original_journal_sha256,'Original journal changed');const original=JSON.parse(originalBytes);need(original.app===APP&&original.sandbox===SANDBOX,'Original canary changed');
 const owner=original.owners.find(x=>x.id===103&&x.role==='owner'),foreign=original.owners.find(x=>x.id===104&&x.role==='foreign');need(owner&&foreign,'Exact existing owners required');
 const rt=new URL(process.env.SANDBOXD_URL);need(rt.origin==='http://127.0.0.1:9090'&&rt.pathname==='/'&&!rt.username&&!rt.password&&!rt.search&&!rt.hash&&process.env.SANDBOXD_TOKEN,'Existing exact loopback runtime authority required');
 const require=createRequire('/opt/baarcha/app/landing/package.json');const sql=require('postgres')(process.env.DATABASE_URL,{max:1,connect_timeout:5,connection:{statement_timeout:10000}});
 const journalPath=path.join(c.stage,'journal.json');let j;
 try{await privatePath(journalPath);j=JSON.parse(await fs.readFile(journalPath));}catch(e){if(e.code!=='ENOENT')throw e;need(action==='prepare','Journal missing');j={version:1,kind:'operator-authored-recovery-data',run:c.run,app:APP,sandbox:SANDBOX,done:{},original_journal_sha256:c.original_journal_sha256,ai_coding_passed:false};await durable(journalPath,JSON.stringify(j,null,2)+'\n');}
 need(j.run===c.run&&j.original_journal_sha256===c.original_journal_sha256,'Separate journal identity changed');
 if(action==='inspect'){console.log(JSON.stringify({pending:j.pending?.name||null,prepared:!!j.prepared,installed:!!j.installed,manifest_restored:!!j.manifest_restored,verified:!!j.verified,ai_coding_passed:false}));await sql.end();return;}
 need(!j.pending,'Ambiguous prior mutation retained; manual reconciliation required');
 const request=async(url,{method='GET',json,bytes,headers={},max=2*1024*1024,timeout=15000}={})=>{
  if(json!==undefined){headers={...headers,'content-type':'application/json'};bytes=JSON.stringify(json);}
  const response=await fetch(url,{method,headers,body:bytes,redirect:'manual',signal:AbortSignal.timeout(timeout)});const body=await boundedBody(response,max);let data;try{data=JSON.parse(body);}catch{}return{status:response.status,body,json:data};
 };
 const runtime=(route,o={})=>{need(route.startsWith('/v1/')&&!route.startsWith('//'),'Unexpected runtime path');return request(new URL(route,rt),{...o,headers:{authorization:'Bearer '+process.env.SANDBOXD_TOKEN,...o.headers}});};
 const rtJSON=async(route,o)=>{const r=await runtime(route,o);need(r.status>=200&&r.status<300,'Runtime request failed');return r.json;};
 const platform=(route,person,o={})=>{need(route.startsWith('/')&&!route.startsWith('//'),'Unexpected platform path');return request('https://baarcha.tn'+route,{...o,headers:person?{cookie:'punicas_platform='+person.token}:{}});};
 let previewCache;
 const preview=async(route,o={})=>{
  need(['/','/health','/api/notes','/operator-recovery-proof'].includes(route),'Unexpected preview route');
  if(!previewCache||previewCache.until<Date.now()+20000){const sb=await rtJSON(`/v1/sandboxes/${SANDBOX}`);const a=await rtJSON(`/v1/sandboxes/${SANDBOX}/preview-access`,{method:'POST'});previewCache={origin:validatePreview(sb,a),token:a.token,until:Date.parse(a.expires_at)};}
  return request(previewCache.origin+route,{...o,headers:{cookie:'sandbox_preview='+previewCache.token}});
 };
 const io={
  save:async value=>durable(journalPath,JSON.stringify(value,null,2)+'\n',true),
  artifact:async(name,bytes)=>{need(!name.includes('/')&&!name.includes('..'),'Artifact path');await durable(path.join(c.stage,name),bytes);},
  originalJournal:()=>fs.readFile(c.original_journal),workerSource:()=>fs.readFile(path.join(here,'home-worker.mjs')),
  guard:async()=>{
   need(process.env.CUBE_OPERATOR_RECOVERY_PARENT===String(process.ppid),'Lock wrapper no longer owns execution');
   const cp=JSON.parse((await exec('docker',['inspect','src-sandboxd-1'],{timeout:10000,maxBuffer:2*1024*1024})).stdout)[0];need(cp.Id===c.controller_id&&cp.Image===c.controller_image&&cp.State.Running,'Controller changed');
   const env=Object.fromEntries(cp.Config.Env.map(x=>[x.slice(0,x.indexOf('=')),x.slice(x.indexOf('=')+1)]));need(env.SANDBOXD_CUBE_ENABLED==='true'&&env.SANDBOXD_CUBE_ROLLOUT==='allowlist'&&env.SANDBOXD_CUBE_APP_IDS===APP,'Exact one-app Cube scope required');
   const a=await rtJSON(`/v1/apps/${APP}`);need(a.external_user_id==='baarcha:103'&&a.external_project_id==='cube-recovery:6ff432593177fb12'&&a.current_sandbox_id===SANDBOX&&a.runtime_preset==='node-postgres','Canary owner/config changed');
   const script="import contextlib,sqlite3,json,sys\nwith contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:\n db.execute('BEGIN');assert db.execute(\"SELECT count(*) FROM task WHERE status='running'\").fetchone()[0]==0;assert db.execute(\"SELECT count(*) FROM cube_admission WHERE state='pending'\").fetchone()[0]==0;assert db.execute(\"SELECT count(*) FROM cube_recovery WHERE phase<>'complete'\").fetchone()[0]==0;r=db.execute('SELECT runtime_id FROM runtime_binding WHERE sandbox_id=? AND provider=?',(sys.argv[1],'cube')).fetchall();assert r==[(sys.argv[2],)];print('verified')";
   await exec('python3',['-c',script,SANDBOX,c.runtime_id],{timeout:10000,maxBuffer:4096});
   const rows=await sql`SELECT id,google_sub FROM waitlist WHERE id IN (103,104)`;need(rows.length===2&&rows.some(r=>r.id===103&&r.google_sub===owner.sub)&&rows.some(r=>r.id===104&&r.google_sub===foreign.sub),'Owners changed');
  },
  tasks:async()=>{const rows=(await rtJSON(`/v1/sandboxes/${SANDBOX}/tasks`)).tasks;need(Array.isArray(rows),'Task inventory');const full=[];for(const r of rows)full.push(await rtJSON(`/v1/sandboxes/${SANDBOX}/tasks/${r.id}`));return full.sort((a,b)=>a.id.localeCompare(b.id));},
  events:async id=>{need(/^[0-9A-HJKMNP-TV-Z]{26}$/.test(id),'Task ID');const r=await runtime(`/v1/sandboxes/${SANDBOX}/tasks/${id}/events`,{max:16*1024*1024,timeout:20000});need(r.status===200,'History events missing');return r.body;},
  read:async name=>{const r=await runtime(`/v1/sandboxes/${SANDBOX}/files/content?path=${encodeURIComponent(name)}`);if(r.status===404)return null;need(r.status===200,'File read failed');return r.body;},
  write:async(name,bytes)=>{need(bytes.length<=2*1024*1024,'Must support complete readback');const r=await runtime(`/v1/sandboxes/${SANDBOX}/files?path=${encodeURIComponent(name)}`,{method:'PUT',bytes,headers:{'content-type':'application/octet-stream'}});need(r.status===200,'File write acknowledgement missing');},
  checkSyntax:async source=>{const filename=path.join(c.stage,'candidate-server.mjs');await durable(filename,source);await exec(process.execPath,['--check',filename],{timeout:5000,maxBuffer:4096});},
  validate:manifest=>rtJSON('/v1/runtime/manifest/validate',{method:'POST',json:{manifest}}),
  reload:async()=>{await rtJSON(`/v1/sandboxes/${SANDBOX}/recreate`,{method:'POST',json:{reload_manifest:true},timeout:45000});previewCache=null;},
  waitProof:async()=>{const until=Date.now()+60000;while(Date.now()<until){try{const r=await preview('/operator-recovery-proof',{timeout:5000});if(r.status===200)return r.json;}catch{}await new Promise(r=>setTimeout(r,500));}throw Error('Actual operator data readiness failed');},
  health:async()=>{const r=await preview('/health');need(r.status===200,'Health failed');return r.json;},
  html:async()=>{const r=await preview('/');need(r.status===200,'Page failed');return r.body.toString();},
  notes:async()=>{const r=await preview('/api/notes');need(r.status===200&&Array.isArray(r.json),'SQL read failed');return r.json;},
  insertNote:async body=>{const r=await preview('/api/notes',{method:'POST',json:{body}});need(r.status===201,'SQL acknowledgement missing');return r.json;},
  privateCapture:async()=>{
   const r=await platform(`/api/projects/${APP}/screenshot?mode=hero`,owner,{method:'POST',max:16*1024*1024,timeout:90000});need(r.status===200&&r.body.length>1000&&r.body[0]===255&&r.body[1]===216,'Actual capture missing');await io.artifact('private-capture.jpg',r.body);
   const [app]=await sql`SELECT visibility,cover_upload_id FROM published_app WHERE project_id=${APP} AND owner_waitlist_id=103`;need(app?.visibility==='private'&&app.cover_upload_id,'Private draft cover required');
   const [upload]=await sql`SELECT id,project_id,waitlist_id FROM upload WHERE id=${app.cover_upload_id} AND deleted_at IS NULL`;need(upload?.project_id===APP&&upload.waitlist_id===103,'Private upload scope changed');
   for(const [person,status] of [[owner,200],[foreign,404],[null,404]]){const image=await platform('/files/'+encodeURIComponent(upload.id),person,{max:16*1024*1024});need(image.status===status,'Private image ACL failed');if(status===200)need(digest(image.body)===digest(r.body),'Captured owner image differs');}
   for(const [person,status] of [[foreign,404],[null,401]]){const denied=await platform(`/api/projects/${APP}/screenshot`,person,{method:'POST'});need(denied.status===status,'Screenshot creation ACL failed');}
   return{id:upload.id,sha256:digest(r.body),owner_read:true,foreign_denied:true,anonymous_denied:true,actual_visual_review:false};
  }
 };
 try{const f=new OperatorFixture(c,j,io);await f[action]();console.log(JSON.stringify({action,completed:true,operator_authored:true,ai_coding_passed:false,backup:false,restore:false}));}
 catch(error){await durable(path.join(c.stage,`failure-${Date.now()}.json`),JSON.stringify({action,pending:j.pending?.name||null,error:'Operator fixture refused; no automatic retry or repair'})+'\n');throw error;}
 finally{await sql.end({timeout:5});}
}
main().catch(()=>{console.error('Operator recovery fixture stopped; inspect private journal. No AI task, credit, cleanup or retry performed.');process.exitCode=1;});
