// Run with the platform host environment. Tests the operator's Olive fixture.
import assert from 'node:assert/strict';
import {readFile,writeFile} from 'node:fs/promises';
import {sql} from '/opt/baarcha/app/landing/src/lib/pg.ts';
import {agentGatewaySecret} from '/opt/baarcha/app/landing/src/lib/platform/agent-usage.ts';
const root='/opt/baarcha-bench/design-skills-20261001';
const db=sql();
const primary=process.argv.includes('--primary');
try {
  const [bridge]=await db`SELECT token,waitlist_id FROM bridge_token WHERE project_id='01M3M0CE8PKPMB38TV3RXY7WMF'`;
  assert.equal(Number(bridge?.waitlist_id),1);
  const image={type:'image',source:{type:'base64',media_type:'image/jpeg',data:(await readFile(root+'/screenshot-after.jpg')).toString('base64')}};
  const question='Inspect the screenshot. Report the visible brand name, quote the main heading, and say whether there is a dark sidebar. If you cannot see the image, say so.';
  const records=[];
  for(const variant of ['direct','tool_result']) {
    const messages=variant==='direct'?[{role:'user',content:[{type:'text',text:question},image]}]:[
      {role:'user',content:question},
      {role:'assistant',content:[{type:'tool_use',id:'toolu_fixture_image',name:'Read',input:{file_path:'/tmp/fixture.jpg'}}]},
      {role:'user',content:[{type:'tool_result',tool_use_id:'toolu_fixture_image',content:[{type:'text',text:'Image file read successfully.'},image]}]},
    ];
    const response=await fetch(primary?`${process.env.PUNICAS_AI_URL.replace(/\/$/,'')}/v1/messages`:'https://baarcha.tn/api/v1/messages',{
      method:'POST',headers:{'content-type':'application/json','x-api-key':primary?process.env.PUNICAS_AI_KEY:agentGatewaySecret(),'x-baarcha-bridge':bridge.token,'anthropic-version':'2023-06-01'},
      body:JSON.stringify({model:process.env.SANDBOXD_MODEL||'glm-5.3-flash[1m]',max_tokens:350,stream:false,system:'Report only visible content accurately. Never invent image details.',messages,tools:[{name:'Read',description:'Read a file',input_schema:{type:'object',properties:{file_path:{type:'string'}},required:['file_path']}}]}),signal:AbortSignal.timeout(120000)});
    const body=await response.json();
    const record={variant,status:response.status,model:body.model,content:body.content?.filter(c=>c.type==='text')};
    records.push(record);console.log(JSON.stringify(record));
  }
  await writeFile(root+(primary?'/vision-primary-after.json':'/vision-after.json'),JSON.stringify(records,null,2));
  for(const record of records) {
    assert.equal(record.status,200);
    const text=record.content?.filter(c=>c.type==='text').map(c=>c.text).join('\n')||'';
    assert.match(text,/Olive/i);assert.match(text,/Make room for focused work/i);
    assert.doesNotMatch(text,/\[image omitted\]/i);
  }
} finally {await db.end();}
