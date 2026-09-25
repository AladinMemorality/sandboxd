import test from 'node:test';
import assert from 'node:assert/strict';
import {priorTasks,validateHistory,thinkingEnv,runTask} from './task.mjs';
const history=()=>Object.entries(priorTasks).map(([id,[failure_reason,checkpoint_id]])=>({id,status:'failed',failure_reason,checkpoint_id}));
const id='01M3D3Z2T9CM6WSW9QB311KXB8';
test('only exact terminal history permits a distinct coding measurement',()=>{
  validateHistory(history());
  for(const change of [r=>r.pop(),r=>r[0].status='running',r=>r[0].checkpoint_id='wrong',r=>r[1]={...r[0]}]){const rows=history();change(rows);assert.throws(()=>validateHistory(rows));}
});
test('thinking configuration matches platform default and zero omission',()=>{
  assert.deepEqual(thinkingEnv(undefined),{MAX_THINKING_TOKENS:'2000'});
  assert.deepEqual(thinkingEnv('0'),{});assert.deepEqual(thinkingEnv('bad'),{MAX_THINKING_TOKENS:'2000'});
  assert.deepEqual(thinkingEnv('99999'),{MAX_THINKING_TOKENS:'32000'});
});
test('intent precedes exactly one submission; terminal failure is retained',async()=>{
  const j={},saved=[],calls=[];const result=await runTask({j,save:async()=>saved.push(structuredClone(j)),request:async kind=>{calls.push(kind);return kind==='submit'?{id}:{status:'failed',failure_reason:'agent_error'};}});
  assert.equal(saved[0].pending.phase,'coding-task-submit');assert.deepEqual(calls,['submit','poll']);assert.equal(result.status,'failed');assert.equal(j.result.failure_reason,'agent_error');assert(!j.pending);
  await assert.rejects(runTask({j,save:async()=>{},request:async()=>assert.fail('replay')}));
});
test('lost submission acknowledgement retains intent and never guesses a cancellation',async()=>{
  const j={},calls=[];await assert.rejects(runTask({j,save:async()=>{},request:async kind=>{calls.push(kind);throw Error('lost ack');}}));
  assert.deepEqual(calls,['submit']);assert(j.pending);assert(!j.task);
  await assert.rejects(runTask({j,save:async()=>{},request:async()=>assert.fail('replay')}));
});
test('watchdog cancels exact acknowledged task once and preserves unresolved state',async()=>{
  const j={},calls=[];let now=0;
  await assert.rejects(runTask({j,deadlineMs:2000,now:()=>now,sleep:async ms=>now+=ms,save:async()=>{},request:async(kind,task)=>{calls.push([kind,task]);return kind==='submit'?{id}:{status:'running'};}}));
  assert.deepEqual(calls.at(-1),['cancel',id]);assert.equal(calls.filter(([k])=>k==='submit').length,1);assert(j.pending);
});
