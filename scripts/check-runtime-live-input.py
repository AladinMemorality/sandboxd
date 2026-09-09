"""Runtime + Claude Code live-input smoke against a local fake model.
Run in the sandbox base image as its default user with --network none,
mounting this file read-only and using --entrypoint python3. No credentials.
"""
import http.server,threading,json,subprocess,tempfile,os,time
seen=[];ready=threading.Event()
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*a):pass
 def do_POST(self):
  b=json.loads(self.rfile.read(int(self.headers.get('content-length','0'))))
  if 'count_tokens' in self.path:
   self.send_response(200);self.end_headers();self.wfile.write(b'{"input_tokens":100}');return
  seen.append(b);n=len(seen)
  ready.set()
  if n==1:
   blocks=[{'type':'tool_use','id':'tool_probe','name':'Bash','input':{'command':'sleep 3','description':'Test pause'}}];stop='tool_use'
  else:
   blocks=[{'type':'text','text':'STEERING_SEEN' if 'STEER_NOW' in json.dumps(b.get('messages',[])) else 'STEERING_MISSING'}];stop='end_turn'
  self.send_response(200);self.send_header('content-type','text/event-stream');self.end_headers()
  events=[('message_start',{'type':'message_start','message':{'id':'msg_probe'+str(n),'type':'message','role':'assistant','model':'glm-5.3-flash','content':[],'stop_reason':None,'usage':{'input_tokens':100,'output_tokens':0}}})]
  for i,block in enumerate(blocks):
   if block['type']=='tool_use':
    init=dict(block,input={});delta={'type':'input_json_delta','partial_json':json.dumps(block['input'])}
   else:init={'type':'text','text':''};delta={'type':'text_delta','text':block['text']}
   events.extend([('content_block_start',{'type':'content_block_start','index':i,'content_block':init}),('content_block_delta',{'type':'content_block_delta','index':i,'delta':delta}),('content_block_stop',{'type':'content_block_stop','index':i})])
  events.extend([('message_delta',{'type':'message_delta','delta':{'stop_reason':stop},'usage':{'output_tokens':10}}),('message_stop',{'type':'message_stop'})])
  for name,data in events:self.wfile.write(('event: '+name+'\ndata: '+json.dumps(data)+'\n\n').encode());self.wfile.flush()
srv=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler);threading.Thread(target=srv.serve_forever,daemon=True).start()

import pathlib,socket,http.client
class UnixHTTP(http.client.HTTPConnection):
 def connect(self):self.sock=socket.socket(socket.AF_UNIX);self.sock.connect(sock)
def request(method,path,body=None):
 c=UnixHTTP('runtimed',timeout=5);c.request(method,path,json.dumps(body) if body else None,{'Content-Type':'application/json'});r=c.getresponse();b=r.read();c.close();return r.status,json.loads(b)
with tempfile.TemporaryDirectory(prefix='runtime-steer-') as tmp:
 root=pathlib.Path(tmp);app=root/'app';app.mkdir();rt=root/'runtime';sock=str(rt/'sock')
 (app/'sandbox.yaml').write_text('version: 1\nworkers:\n  - name: idle\n    command: "sleep 120"\nbuild:\n  command: "true"\n')
 env=dict(os.environ,HOME=tmp,RUNTIMED_APP_DIR=str(app),RUNTIMED_DIR=str(rt),RUNTIMED_TEMPLATE='none')
 with open(root/'runtime.log','w+') as log:
  p=subprocess.Popen(['/usr/local/bin/runtimed'],env=env,stdout=log,stderr=log)
  try:
   for _ in range(100):
    if pathlib.Path(sock).exists():break
    time.sleep(.1)
   task='01M239TESTLIVEINPUT00000000'
   e={'HOME':tmp,'ANTHROPIC_API_KEY':'test','ANTHROPIC_BASE_URL':'http://127.0.0.1:'+str(srv.server_port),'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC':'1','CLAUDE_CODE_SIMPLE':'1'}
   code,b=request('POST','/tasks',{'task_id':task,'prompt':'Run sleep 3 using Bash then report completion.','agent':'claude-code','model':'glm-5.3-flash','timeout_s':45,'continue':False,'env':e});assert code==202,(code,b)
   assert ready.wait(25),'model was not called'
   msg={'message_id':'00000000-0000-4000-8000-000000000002','prompt':'STEER_NOW: incorporate this correction immediately.'}
   for _ in range(2):
    code,b=request('POST','/tasks/'+task+'/messages',msg);assert code==202,(code,b)
   for _ in range(250):
    time.sleep(.1)
    code,status=request('GET','/status')
    if not status.get('active_task'):break
   assert not status.get('active_task'),'task still running'
   assert len(seen)>1 and 'STEER_NOW' in json.dumps(seen[1].get('messages',[])),'next model call missed input'
   results=list(rt.rglob('result.json'));assert results,'no saved result'
   result=json.loads(results[0].read_text());assert result['status']=='succeeded',result
   logs=''.join(x.read_text() for x in rt.rglob('*.jsonl'))
   events=[json.loads(line) for f in rt.rglob('events.jsonl') for line in f.read_text().splitlines()]
   assert sum(x['type']=='input' and x['data'].get('status')=='accepted' for x in events)==1,'duplicate delivered twice'
   assert sum(x['type']=='input' and x['data'].get('status')=='received' for x in events)==1,'missing input acknowledgement'
   print(json.dumps({'runtime_task':result['status'],'next_model_call_received_followup':True,'duplicate_message_accepted_once':True,'model_calls':len(seen)}))
  except Exception:
   log.flush();log.seek(0);print(log.read()[-6000:]);print('CALLS',len(seen));
   for f in rt.rglob('*'):
    if f.is_file() and not f.is_socket():print(str(f),f.read_text(errors='replace')[-10000:])
   raise
  finally:
   p.terminate();p.wait(timeout=10)
srv.shutdown()
