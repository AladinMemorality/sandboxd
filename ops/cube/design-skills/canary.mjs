// Run on the platform host with its environment loaded. Only the existing
// operator-owned Olive fixture is used. No application edits are requested.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {readFile,writeFile} from 'node:fs/promises';
import {sql} from '/opt/baarcha/app/landing/src/lib/pg.ts';

const root='/opt/baarcha-bench/design-skills-20261001';
const project='01M3M0CE8PKPMB38TV3RXY7WMF', sandbox='01M3M0CEAEDRTK878AA5TMGENT';
const base=process.env.SANDBOXD_URL.replace(/\/$/,'');
const headers={authorization:`Bearer ${process.env.SANDBOXD_TOKEN}`,'content-type':'application/json'};
async function api(path, options={}) {
  const r=await fetch(base+path,{headers,...options,signal:AbortSignal.timeout(120000)});
  if(!r.ok) throw new Error(`Controller ${path.split('?')[0]} returned ${r.status}`);
  return r;
}
const file=async path=>Buffer.from(await (await api(`/v1/sandboxes/${sandbox}/files/content?path=${encodeURIComponent(path)}`)).arrayBuffer());
const hash=b=>createHash('sha256').update(b).digest('hex');
const sourcePaths=['src/App.tsx','src/index.css','AGENTS.md','CLAUDE.md'];
const db=sql();
try {
  const [bridge]=await db`SELECT token,waitlist_id FROM bridge_token WHERE project_id=${project}`;
  assert.equal(Number(bridge?.waitlist_id),1,'fixture must belong to the operator');
  const before={};
  for(const p of sourcePaths) before[p]=hash(await file(p));
  const env={BRIDGE_URL:process.env.BRIDGE_PUBLIC_URL||'https://baarcha.tn/api/bridge',BRIDGE_TOKEN:bridge.token,BRIDGE_PROJECT:project,ANTHROPIC_CUSTOM_HEADERS:`x-baarcha-bridge: ${bridge.token}`,MAX_THINKING_TOKENS:'1000'};
  const prompt="Review only: verify the shared Baarcha design pack supplied in this task. Do not edit application files, project instructions, or documentation. Read its GUIDE.md and taste-redesign/SKILL.md, then its self-screenshot/SKILL.md. Use the screenshot bridge once and open the returned image with Read. Describe three concrete visual observations from the actual image, including one improvement you would make. Report any capture or image-viewing failure accurately. Do not implement improvements, generate images, or install dependencies.";
  const started=Date.now();
  const accepted=await (await api(`/v1/sandboxes/${sandbox}/tasks`,{method:'POST',body:JSON.stringify({prompt,agent:'claude-code',model:process.env.SANDBOXD_MODEL||'glm-5.3-flash[1m]',timeout_s:300,continue:false,env})})).json();
  await writeFile(root+'/canary-accepted.json',JSON.stringify({id:accepted.id,sandbox,project,dispatch_ms:Date.now()-started},null,2));
  console.log(JSON.stringify({accepted:accepted.id,dispatch_ms:Date.now()-started}));
  let result;
  for(let i=0;i<80;i++) {
    result=await (await api(`/v1/sandboxes/${sandbox}/tasks/${accepted.id}`)).json();
    if(result.status!=='running')break;
    await new Promise(resolve=>setTimeout(resolve,5000));
  }
  assert.notEqual(result.status,'running','canary did not finish in its window');
  const manifest=JSON.parse(await readFile(root+'/pack-manifest.json','utf8'));
  for(const f of manifest.files) assert.equal(hash(await file(manifest.directory+'/'+f.path)),f.sha256,`wrong delivered file ${f.path}`);
  for(const p of sourcePaths) assert.equal(hash(await file(p)),before[p],`review changed ${p}`);
  const record={task:accepted.id,status:result.status,elapsed_ms:Date.now()-started,pack:manifest.directory,verified_files:manifest.files.length,application_files_preserved:sourcePaths,agent_message:result.agent_message_final,error:result.error_message};
  await writeFile(root+'/canary-result.json',JSON.stringify(record,null,2));
  console.log(JSON.stringify(record));
  assert.equal(result.status,'succeeded');
} finally {await db.end();}
