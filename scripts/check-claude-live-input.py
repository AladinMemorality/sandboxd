"""Native Claude Code live-input smoke with a local fake model, no credentials.
Run as a non-root user with claude on PATH. No external model calls.
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
with tempfile.TemporaryDirectory(prefix='steer-probe-') as tmp:
 env={'PATH':os.environ['PATH'],'HOME':tmp,'ANTHROPIC_API_KEY':'test','ANTHROPIC_BASE_URL':'http://127.0.0.1:'+str(srv.server_port),'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC':'1'}
 p=subprocess.Popen(['claude','-p','--bare','--input-format','stream-json','--output-format','stream-json','--replay-user-messages','--verbose','--dangerously-skip-permissions','--model','glm-5.3-flash'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,cwd=tmp,env=env)
 def send(text,uid):p.stdin.write(json.dumps({'type':'user','uuid':uid,'message':{'role':'user','content':text},'parent_tool_use_id':None,'session_id':''})+'\n');p.stdin.flush()
 send('Run sleep 3 using Bash then report completion.','00000000-0000-4000-8000-000000000001')
 def follow():
  if ready.wait(20):
   time.sleep(.1);send('STEER_NOW: incorporate this new request now.','00000000-0000-4000-8000-000000000002');print('SENT_FOLLOWUP',flush=True)
 threading.Thread(target=follow,daemon=True).start()
 def kill_later():
  time.sleep(40)
  if p.poll() is None:p.kill()
 threading.Thread(target=kill_later,daemon=True).start()
 for l in p.stdout:
  d=json.loads(l)
  if d.get('type')=='result':p.stdin.close()
 code=p.wait();print('EXIT',code,'CALLS',len(seen),'SECOND_CALL_HAS_STEERING',len(seen)>1 and 'STEER_NOW' in json.dumps(seen[1].get('messages',[])),flush=True)
 assert code == 0, p.stderr.read()[-1800:]
 assert len(seen)>1 and 'STEER_NOW' in json.dumps(seen[1].get('messages',[])), 'Follow-up missed the next model call'
srv.shutdown()
