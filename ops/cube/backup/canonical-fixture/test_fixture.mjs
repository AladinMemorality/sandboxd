import test from 'node:test';
import assert from 'node:assert/strict';
import {Fixture,boundedBody,digest,intentAllowed,promptFor,runTaskToTerminal,validateConfig,validatePreview} from './fixture.mjs';
const id='01M3CKN983PFRGMD711PCEPDFD',sb='01M3CKN99ZF90BEEA4DS66YAQV',task='01M3CKN983PFRGMD711PCEPDFE';
const config={version:1,controller_id:'a'.repeat(64),controller_image:'sha256:'+'b'.repeat(64),platform_revision:'c'.repeat(40),template_id:'tpl-reviewed',stage:'/opt/baarcha-bench/cube-canonical-fixture-test',platform_root:'/opt/baarcha/app/landing',allowed_app_id:id,funding_millimes:1000,stop_binary_sha256:'d'.repeat(64),start_binary_sha256:'e'.repeat(64),host_cycle_receipt:'/opt/baarcha-bench/complete.json',host_cycle_receipt_sha256:'f'.repeat(64),worker_boot_id:'11111111-1111-1111-1111-111111111111',enrollment_runner_sha256:'a'.repeat(64)};
const journal=()=>({run:'abcdef1234567890',app:id,owners:[{id:21,role:'owner',sub:'fixture-owner',token:'private-owner'},{id:22,role:'foreign',sub:'fixture-foreign',token:'private-foreign'}],done:{app:{id}}});
const app=j=>({id,external_user_id:'baarcha:21',external_project_id:`cube-recovery:${j.run}`,runtime_preset:'node-postgres-standard'});
const preview=`https://s-${sb.toLowerCase()}-3000.preview.65.108.225.153.sslip.io/`;
const access=()=>({url:preview,access_url:preview+'__sandboxd/preview-auth?token=fixture',token:'fixture',expires_at:new Date(Date.now()+600000).toISOString()});

test('config refuses uncontrolled stage, credit, app IDs and missing binary pins',()=>{
  assert.deepEqual(validateConfig({...config}),config);
});
test('config hostile variants are refused',()=>{
  for(const change of [{stage:'/tmp/other'},{funding_millimes:50000},{allowed_app_id:'*'},{stop_binary_sha256:''},{controller_id:'short'},{platform_root:'/tmp/pkg'},{host_cycle_receipt_sha256:'bogus'}])assert.throws(()=>validateConfig({...config,...change}));
});
test('preview credentials stay on exact stable Cube origin',()=>{
  const row={id:sb,runtime_provider:'cube',preview:{url:preview}};
  assert.equal(validatePreview(row,access()),new URL(preview).origin);
  for(const a of [{...access(),access_url:'https://example.com/__sandboxd/preview-auth'}, {...access(),token:'bad;cookie'}, {...access(),expires_at:'2000-01-01'}])assert.throws(()=>validatePreview(row,a));
  assert.throws(()=>validatePreview({...row,runtime_provider:'docker'},access()));
});
test('intent refuses replay or any unresolved earlier operation',()=>{
  assert.throws(()=>intentAllowed({pending:{name:'app'},done:{}},'task'));
  assert.throws(()=>intentAllowed({done:{task:{}}},'task'));
  intentAllowed({done:{}},'task');
});
test('task prompt confines synthetic names and refuses injection run identifier',()=>{
  assert.match(promptFor('a'.repeat(16)),/home marker.txt/);assert.match(promptFor('a'.repeat(16)),/mode0750/);
  assert.throws(()=>promptFor('x;curl evil'));
});
test('real completed task needs checkpoint; no cancel after terminal',async()=>{
  let cancelled=0,saved=0;
  const result=await runTaskToTerminal({submit:async()=>({id:task}),save:async()=>saved++,poll:async()=>({id:task,status:'succeeded',checkpoint_id:'ref'}),cancel:async()=>cancelled++});
  assert.equal(result.checkpoint_id,'ref');assert.equal(saved,1);assert.equal(cancelled,0);
  await assert.rejects(runTaskToTerminal({submit:async()=>({id:task}),save:async()=>{},poll:async()=>({status:'succeeded'}),cancel:async()=>cancelled++}));assert.equal(cancelled,0);
});
test('deadline cancels exact acknowledged task once and never resubmits',async()=>{
  let clock=0,submitted=0;const cancelled=[];
  await assert.rejects(runTaskToTerminal({submit:async()=>{submitted++;return{id:task};},save:async()=>{},poll:async()=>({status:'running'}),cancel:async id=>cancelled.push(id),now:()=>clock,sleep:async ms=>clock+=ms,deadlineMs:2500}),/deadline/);
  assert.equal(clock,2500);assert.equal(submitted,1);assert.deepEqual(cancelled,[task]);
});
test('poll/storage failure requests cancellation; lost submit does not fabricate ID',async()=>{
  const cancelled=[];await assert.rejects(runTaskToTerminal({submit:async()=>({id:task}),save:async()=>{throw Error('disk error');},poll:async()=>{},cancel:async id=>cancelled.push(id)}));assert.deepEqual(cancelled,[task]);
  await assert.rejects(runTaskToTerminal({submit:async()=>{throw Error('lost acknowledgement');},save:async()=>{},poll:async()=>{},cancel:async()=>assert.fail('unknown task')}));
});
test('oversized response is cancelled rather than buffered without bound',async()=>{
  let cancelled=false;const body=new ReadableStream({start(c){c.enqueue(new Uint8Array(9));},cancel(){cancelled=true;}});
  await assert.rejects(boundedBody({body},8),/limit/);assert.equal(cancelled,true);
});
test('ambiguous create retains intent and forbids another provider POST',async()=>{
  const j=journal();let creates=0,persisted=0;
  const f=new Fixture(config,j,{persist:async()=>persisted++,inspect:async()=>({}),request:async(_kind,route)=>{
    if(route===`/v1/apps/${id}`)return{status:200,json:app(j)};
    creates++;assert.equal(j.pending.name,'sandbox');throw Error('provider acknowledgement lost');
  }});
  await assert.rejects(f.create());assert.equal(j.pending.name,'sandbox');assert.equal(creates,1);assert(persisted>0);
  await assert.rejects(f.create());assert.equal(creates,1);
});
test('wrong provider acknowledgement retained, never silently accepted or deleted',async()=>{
  const j=journal(), calls=[];
  const f=new Fixture(config,j,{persist:async()=>{},inspect:async()=>({}),request:async(_kind,route,opts)=>{calls.push([route,opts?.method]);return{status:200,json:route.endsWith('/sandbox')?{id:sb}:route.includes('/sandboxes/')?{id:sb,runtime_provider:'docker'}:app(j)};}});
  await assert.rejects(f.create(),/Provider mismatch/);assert.equal(j.sandbox,sb);assert.equal(j.pending.name,'sandbox');assert(!calls.some(([,method])=>method==='DELETE'));
});
test('canonical task sets120s and scoped bridge, one POST, result/checkpoint journal',async()=>{
  const j=journal();j.sandbox=sb;j.done.sandbox={};j.done.credit={};const posts=[];
  const tx=async(strings)=>strings.join('').includes('SELECT id FROM waitlist')?[{id:21}]:strings.join('').includes('AS balance')?[{balance:1000}]:strings.join('').includes('AS requests')?[{requests:0}]:strings.join('').includes('SELECT token')?[{token:'a'.repeat(64),waitlist_id:21}]:[];
  const sql={begin:async fn=>fn(tx)};const old=process.env.BRIDGE_PUBLIC_URL;process.env.BRIDGE_PUBLIC_URL='https://baarcha.tn/api/bridge';
  const f=new Fixture(config,j,{sql,persist:async()=>{},request:async(kind,route,opts)=>{
    if(kind==='preview')return{status:401};
    if(route===`/v1/apps/${id}`)return{status:200,json:app(j)};
    if(route.endsWith('/preview-access'))return{status:200,json:access()};
    if(route===`/v1/sandboxes/${sb}`)return{status:200,json:{id:sb,runtime_provider:'cube',preview:{url:preview}}};
    if(route.endsWith('/tasks')&&opts?.method==='POST'){posts.push(opts.body);return{status:202,json:{id:task}};}
    if(route.endsWith('/tasks'))return{status:200,json:{tasks:[]}};
    return{status:200,json:{id:task,status:'succeeded',checkpoint_id:'ref'}};
  }});
  try{await f.task();}finally{if(old===undefined)delete process.env.BRIDGE_PUBLIC_URL;else process.env.BRIDGE_PUBLIC_URL=old;}
  assert.equal(posts.length,1);assert.equal(posts[0].timeout_s,120);assert.equal(posts[0].continue,false);assert.equal(posts[0].env.BRIDGE_PROJECT,id);assert.equal(posts[0].env.ANTHROPIC_CUSTOM_HEADERS,'x-baarcha-bridge: '+'a'.repeat(64));
  assert.deepEqual(Object.keys(posts[0].env).sort(),['ANTHROPIC_CUSTOM_HEADERS','BRIDGE_PROJECT','BRIDGE_TOKEN','BRIDGE_URL','MAX_THINKING_TOKENS']);assert.equal(j.done.task.runtime_timeout_s,120);assert(!j.pending);
});
test('foreign bridge owner prevents any task POST and retains review intent',async()=>{
  const j=journal();j.sandbox=sb;j.done.credit={};j.done.sandbox={};
  const tx=async strings=>strings.join('').includes('SELECT id FROM waitlist')?[{id:21}]:strings.join('').includes('AS balance')?[{balance:1000}]:strings.join('').includes('AS requests')?[{requests:0}]:[{token:'a'.repeat(64),waitlist_id:22}];
  const f=new Fixture(config,j,{sql:{begin:async fn=>fn(tx)},persist:async()=>{},request:async()=>assert.fail('Unexpected request')});
  f.checkApp=async()=>{};f.preview=async()=>{};f.rt=async route=>{assert(route.endsWith('/tasks'));return{tasks:[]};};
  await assert.rejects(f.task(),/Bridge owner/);assert.equal(j.pending.name,'task');
});

test('capture owner/foreign/anonymous checks use the exact scoped upload without publishing',async()=>{
  const fs=await import('node:fs/promises'),os=await import('node:os'),path=await import('node:path');
  const stage=await fs.mkdtemp(path.join(os.tmpdir(),'cube-fixture-acl-'));await fs.chmod(stage,0o700);
  const j=journal(),pixels=Buffer.alloc(1100,1);pixels[0]=255;pixels[1]=216;const calls=[];
  const sql=async strings=>strings.join('').includes('published_app')?[{visibility:'private',cover_upload_id:'fixture-image'}]:[{id:'fixture-image',project_id:id,waitlist_id:21}];
  const f=new Fixture({...config,stage},j,{sql,persist:async()=>{},request:async(kind,route,opts)=>{
    calls.push({kind,route,person:opts.person?.id});assert(!route.includes('publish'));
    if(route.includes('/screenshot?'))return{status:200,body:pixels};
    if(route.startsWith('/files/'))return{status:opts.person?.id===21?200:404,body:opts.person?.id===21?pixels:Buffer.alloc(0)};
    return{status:opts.person?404:401,body:Buffer.alloc(0)};
  }});
  try{await f.capture();assert.equal(j.cover.id,'fixture-image');assert.equal(j.cover.private,true);assert.equal(j.done.capture.anonymous_denied,true);assert.equal(calls.filter(r=>r.route==='/files/fixture-image').length,3);assert.equal(calls.at(-1).person,undefined);}
  finally{await fs.rm(stage,{recursive:true});}
});
test('same image owned by another project is refused before granting access',async()=>{
  const fs=await import('node:fs/promises'),os=await import('node:os'),path=await import('node:path');const stage=await fs.mkdtemp(path.join(os.tmpdir(),'cube-fixture-acl-'));await fs.chmod(stage,0o700);
  const j=journal(),pixels=Buffer.alloc(1100,1);pixels[0]=255;pixels[1]=216;
  const sql=async strings=>strings.join('').includes('published_app')?[{visibility:'private',cover_upload_id:'shared-image'}]:[{id:'shared-image',project_id:'another-project',waitlist_id:21}];
  const f=new Fixture({...config,stage},j,{sql,persist:async()=>{},request:async(_kind,route)=>{assert(route.includes('/screenshot?'));return{status:200,body:pixels};}});
  try{await assert.rejects(f.capture(),/Scoped upload/);assert.equal(j.pending.name,'capture');assert(!j.done.capture);}finally{await fs.rm(stage,{recursive:true});}
});
