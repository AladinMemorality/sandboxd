import assert from 'node:assert/strict';
import test from 'node:test';
import {runProfile, terminalReport} from './profile.mjs';
const nonce='a'.repeat(16),original=Buffer.from('original manifest'),candidate=Buffer.from('candidate manifest');
function fixture({failure=false}={}) {
  const events=[],j={},disk=new Map([['sandbox.yaml',original]]),saved=[];
  const report={version:2,run:nonce,successful:!failure,phases:[{name:failure?'failed':'finished'},{name:'owned_children_stopped'}],build:{exit_code:0,timed_out:false},ready:{api_valid:true},hmr:{transformed_marker_verified:true},load:{requests:64,failures:0},dist_index_sha256:'a'.repeat(64)};
  const ops={guard:async()=>events.push('guard'),read:async name=>name.endsWith('/report.json')?(events.includes('reload')?Buffer.from(JSON.stringify(report)):null):disk.get(name)??null,
    write:async(name,body)=>{events.push('write:'+name);disk.set(name,body);},reload:async()=>events.push('reload'),archive:async()=>events.push('archive'),restoredHealth:async()=>events.push('health')};
  return {j,ops,events,disk,saved,report,input:{j,ops,save:async()=>saved.push(JSON.parse(JSON.stringify(j))),nonce,original,candidate,files:{'guest.py':Buffer.from('fixed helper'),'probe.mjs':Buffer.from('fixed probe')}}};
}
test('successful workload preserves original manifest and journals each mutation before dispatch',async()=>{
  const f=fixture();assert.equal(await runProfile(f.input),true);assert.deepEqual(f.disk.get('sandbox.yaml'),original);assert.equal(f.j.restored,true);
  assert.equal(f.events.filter(x=>x==='reload').length,2);assert(f.saved.some(j=>j.pending?.name==='activate-reload'));
  assert.equal(f.saved.at(-1).pending,null);assert.equal(f.events.at(-1),'health');
  await assert.rejects(runProfile(f.input),/never replayed/);
});
test('known terminal workload failure still restores original services',async()=>{
  const f=fixture({failure:true});assert.equal(await runProfile(f.input),false);assert.equal(f.j.restored,true);assert.deepEqual(f.disk.get('sandbox.yaml'),original);
});
test('ambiguous activation is retained and never retried or rolled back automatically',async()=>{
  const f=fixture();let reloads=0;f.ops.reload=async()=>{reloads++;throw Error('response lost after possible apply');};
  await assert.rejects(runProfile(f.input),/response lost/);assert.equal(reloads,1);assert.equal(f.j.pending.name,'activate-reload');assert.deepEqual(f.disk.get('sandbox.yaml'),candidate);
  await assert.rejects(runProfile(f.input),/never replayed/);assert.equal(reloads,1);
});
test('a concurrent manifest edit is not overwritten on restoration',async()=>{
  const f=fixture();f.ops.archive=async()=>f.disk.set('sandbox.yaml',Buffer.from('new actor manifest'));
  await assert.rejects(runProfile(f.input),/Manifest drift/);assert.equal(f.events.filter(x=>x==='reload').length,1);assert.equal(f.disk.get('sandbox.yaml').toString(),'new actor manifest');
});
test('archive read failure does not leave known-stopped workload configuration installed',async()=>{
  const f=fixture();f.ops.archive=async()=>{throw Error('read unavailable');};assert.equal(await runProfile(f.input),false);assert.equal(f.j.archive_error,true);assert.equal(f.j.restored,true);
});
test('unknown workload completion does not claim cleanup or issue another reload',async()=>{
  const f=fixture();f.ops.read=async name=>f.disk.get(name)??null;let clock=0;
  await assert.rejects(runProfile({...f.input,now:()=>clock,sleep:async()=>{clock+=1000;},pollMs:2000}),/Unknown workload completion/);
  assert.equal(f.events.filter(x=>x==='reload').length,1);assert.equal(f.j.restored,undefined);assert.deepEqual(f.disk.get('sandbox.yaml'),candidate);
});
test('terminal report must identify the same run and confirm child cleanup',()=>{
  const f=fixture();assert.equal(terminalReport(f.report,nonce),true);
  assert.throws(()=>terminalReport({...f.report,run:'b'.repeat(16)},nonce));
  assert.throws(()=>terminalReport({...f.report,phases:[{name:'finished'}]},nonce),/Children/);
  assert.throws(()=>terminalReport({...f.report,hmr:{transformed_marker_verified:false}},nonce));
});
