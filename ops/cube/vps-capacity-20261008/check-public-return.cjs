// Real browser return after a reviewed app is explicitly asleep. No model calls.
const {chromium}=require('/tmp/baarcha-public-app-return-20261008/landing/node_modules/playwright-core');
const {execFileSync}=require('node:child_process');
const fs=require('node:fs');
const cases={
 nos:{sid:'01M415VT76M0M88GDY2NWWE942',slug:'nos-national-operating-system',text:'Tunisie'},
 derja:{sid:'01M46NANDW8YVG3Z2F2EXZCY1F',slug:'derja-review-studio',text:'TUNISIAN DERJA'},
};
const name=process.argv[2];const app=cases[name];if(!app)throw Error('Explicit nos or derja case required');
function state(action='read',expected=null){
 const request={sid:app.sid,action,expected};
 const python=`import contextlib,hashlib,json,sqlite3,subprocess,urllib.request
v=json.loads(${JSON.stringify(JSON.stringify(request))})
def snapshot():
 with contextlib.closing(sqlite3.connect('file:/var/lib/sandboxd/state/sandboxd.db?mode=ro',uri=True)) as db:
  row=db.execute('select s.status,b.runtime_id,a.state,a.charged from sandbox s join runtime_binding b on b.sandbox_id=s.id join cube_admission a on a.runtime_id=b.runtime_id where s.id=?',(v['sid'],)).fetchone();assert row
  tasks=db.execute('select task_id,status from task where sandbox_id=? order by task_id',(v['sid'],)).fetchall()
  return {'status':row[0],'runtime_id':row[1],'admission':row[2],'charged':row[3],'history':hashlib.sha256(json.dumps(tasks).encode()).hexdigest(),'busy':any(t[1] in ('running','queued') for t in tasks)}
before=snapshot()
if v['action']=='stop':
 assert v['expected']['status']=='stopped' and not before['busy'] and before['runtime_id']==v['expected']['runtime_id'] and before['history']==v['expected']['history'],'User activity or placement changed; preserve app'
 assert before['status'] in ('running','stopped') and before['admission'] in ('active','released')
 container=json.loads(subprocess.check_output(['docker','inspect','src-sandboxd-1']))[0];env=dict(x.split('=',1) for x in container['Config']['Env']);token=env['SANDBOXD_API_TOKENS'].split(',')[0].split('=',1)[1]
 req=urllib.request.Request('http://127.0.0.1:9090/v1/sandboxes/'+v['sid']+'/stop',method='POST',headers={'Authorization':'Bearer '+token})
 with urllib.request.urlopen(req,timeout=180) as response:assert response.status==200;response.read()
 before=snapshot();assert before['status']=='stopped' and before['admission']=='released' and before['charged']==0
else:assert v['action']=='read'
print(json.dumps(before))
`;
 return JSON.parse(execFileSync('/usr/bin/ssh',['-S','/tmp/baarcha-vps-resumed-v4.sock','project-x','python3 -'],{input:python,encoding:'utf8',timeout:200000,stdio:['pipe','pipe','pipe']}));
}
(async()=>{
 const before=state();if(before.busy)throw Error('Existing user task; public test postponed');
 const browser=await chromium.launch({headless:true,executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',ignoreDefaultArgs:['--disable-back-forward-cache']});
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 const target='https://baarcha.tn/apps/aladin/'+app.slug;const started=Date.now();let result,opened=false;
 async function ready(){
  const until=Date.now()+120000;
  while(Date.now()<until){
   const loaded=page.locator('.app-stage.is-live:not(.is-loading) iframe.app-frame');
   const handle=await loaded.elementHandle();const frame=handle&&await handle.contentFrame();
   if(frame){
    const text=await frame.locator('body').innerText().catch(()=> '');
    if(text.includes(app.text)&&!text.includes('sandbox_explicit_start_required')){
     const url=new URL(frame.url());if(url.protocol==='https:'&&await loaded.isVisible())return {frame:url.origin+url.pathname,textCharacters:text.length,liveFrameVisible:true};
    }
   }
   await page.waitForTimeout(1000);
  }
  throw Error('Visible live app did not render');
 }
 try{
  await page.goto(target,{waitUntil:'domcontentloaded',timeout:45000});const first=await ready();opened=true;
  await page.goto('https://baarcha.tn/apps',{waitUntil:'domcontentloaded'});
  const asleep=before.status==='stopped'?state('stop',before):null;
  await page.goBack({waitUntil:'domcontentloaded'});const returned=await ready();
  await page.waitForTimeout(1500);if(errors.length)throw Error('Uncaught browser errors: '+errors.length);
  await page.screenshot({path:'/tmp/baarcha-vps-return-'+name+'.png',fullPage:true});
  result={passed:true,case:name,first,returned,explicitly_asleep_while_away:!!asleep,existing_running_preserved:before.status==='running',pageErrors:errors,seconds:(Date.now()-started)/1000};
 }finally{
  await browser.close();
  if(opened&&before.status==='stopped')state('stop',before);
 }
 fs.writeFileSync('/tmp/baarcha-vps-return-'+name+'.json',JSON.stringify(result));console.log(JSON.stringify(result));
})().catch(e=>{console.error(e.message);process.exit(1)});
