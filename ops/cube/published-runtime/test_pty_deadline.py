"""Exercise real PTY backpressure without contacting a sandbox or containerd."""
import multiprocessing,subprocess,sys,time,unittest
from unittest.mock import patch
import worker

REAL_POPEN=subprocess.Popen

def exercise(mode,result):
    children=[]
    payload=b'x'*(4*1024*1024)
    peer={
      'blocked':"import tty,time;tty.setraw(0);print('RUNTIME_READY',flush=True);time.sleep(30)",
      'exit':"import tty;tty.setraw(0);print('RUNTIME_READY',flush=True)",
      'drain':"import tty,sys,json;tty.setraw(0);print('RUNTIME_READY',flush=True);data=sys.stdin.buffer.read(4194304);print('RUNTIME_RECEIPT='+json.dumps({'bytes':len(data)}),flush=True)",
    }[mode]
    def spawn(_args,**kwargs):
        child=REAL_POPEN([sys.executable,'-c',peer],**kwargs);children.append(child);return child
    began=time.monotonic()
    with patch.object(worker.subprocess,'Popen',spawn):
        try:
            receipt=worker.execute('a'*32,'','',payload,timeout=.35 if mode!='drain' else 3)
            outcome={'receipt':receipt}
        except RuntimeError as error:outcome={'error':str(error)}
    result.put({**outcome,'seconds':time.monotonic()-began,'children_reaped':all(p.poll() is not None for p in children)})

class PTYDeadline(unittest.TestCase):
    def check_peer(self,mode):
        ctx=multiprocessing.get_context('spawn');result=ctx.Queue();process=ctx.Process(target=exercise,args=(mode,result));process.start()
        try:
            process.join(6)
            self.assertFalse(process.is_alive(),'PTY transfer bypassed its deadline')
            self.assertEqual(process.exitcode,0)
            value=result.get(timeout=1);self.assertTrue(value['children_reaped']);return value
        finally:
            if process.is_alive():process.kill();process.join()
            result.close();result.join_thread()
    def test_stalled_reader_cannot_block_write_past_deadline(self):
        value=self.check_peer('blocked');self.assertEqual(value['error'],'missing guest receipt');self.assertLess(value['seconds'],2)
    def test_departed_peer_returns_without_hanging(self):
        value=self.check_peer('exit');self.assertEqual(value['error'],'missing guest receipt');self.assertLess(value['seconds'],2)
    def test_partial_nonblocking_writes_deliver_complete_payload(self):
        value=self.check_peer('drain');self.assertEqual(value['receipt']['bytes'],4*1024*1024)
if __name__=='__main__':unittest.main()
