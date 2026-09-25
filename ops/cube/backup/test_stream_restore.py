import hashlib
import io
import json
import os
from pathlib import Path
import sys
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import stream_restore as s

class ReceiverTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name).resolve();os.chmod(self.root,0o700)
        self.p=patch.object(s,'ROOT',self.root);self.p.start();self.addCleanup(self.p.stop)
        self.token='a'*32;self.data=b'opaque fixture plaintext';self.job=self.root/'new-job'
    def test_partial_receive_never_claims_gpg_or_publishes(self):
        receipt=s.receive(self.job,self.token,4096,io.BytesIO(self.data))
        self.assertFalse(receipt['gpg_verified']);self.assertFalse((self.job/'decrypted.tar').exists())
        self.assertEqual((self.job/'decrypted.UNVERIFIED.tar').stat().st_mode&0o777,0o600)
    def test_exact_publish_is_noclobber_and_not_application_restore(self):
        r=s.receive(self.job,self.token,4096,io.BytesIO(self.data))
        out=s.publish(self.job,self.token,r['bytes'],r['sha256'],'b'*64)
        self.assertTrue(out['gpg_exit_zero_verified_by_offhost_operator']);self.assertFalse(out['application_restore_verified'])
        self.assertEqual((self.job/'decrypted.tar').read_bytes(),self.data)
        with self.assertRaises(FileExistsError):s.publish(self.job,self.token,r['bytes'],r['sha256'],'b'*64)
    def test_wrong_nonce_and_post_receive_tamper_block_publish(self):
        r=s.receive(self.job,self.token,4096,io.BytesIO(self.data))
        with self.assertRaises(RuntimeError):s.publish(self.job,'c'*32,r['bytes'],r['sha256'],'b'*64)
        (self.job/'decrypted.UNVERIFIED.tar').write_bytes(b'x'*len(self.data))
        with self.assertRaises(RuntimeError):s.publish(self.job,self.token,r['bytes'],r['sha256'],'b'*64)
        self.assertFalse((self.job/'decrypted.tar').exists())
    def test_oversize_empty_existing_job_and_escape_refused(self):
        with self.assertRaises(RuntimeError):s.receive(self.job,self.token,3,io.BytesIO(self.data))
        self.assertFalse((self.job/'receiver.json').exists())
        with self.assertRaises(FileExistsError):s.receive(self.job,self.token,4096,io.BytesIO(self.data))
        with self.assertRaises(RuntimeError):s.receive(self.root/'..'/'bad',self.token,4096,io.BytesIO(self.data))
        with self.assertRaises(RuntimeError):s.receive(self.root/'empty',self.token,4096,io.BytesIO())
    def test_send_requires_pinned_readback_digest_and_does_not_print_json(self):
        job=self.root/'store';job.mkdir(mode=0o700);f=job/'readback.gpg';f.write_bytes(self.data);os.chmod(f,0o600)
        out=io.BytesIO()
        with self.assertRaises(RuntimeError):s.send(f,len(self.data),'f'*64,out)
        self.assertEqual(out.getvalue(),b'')
        s.send(f,len(self.data),hashlib.sha256(self.data).hexdigest(),out);self.assertEqual(out.getvalue(),self.data)

class PipelineTests(unittest.TestCase):
    data=b'q'*180000
    def command(self,code):return [sys.executable,'-c',code]
    def run_transfer(self,reader_exit=0,gpg_exit=0,receiver_exit=0,checksum=None,delay=False):
        reader=self.command(f'import sys,time;time.sleep({5 if delay else 0});sys.stdout.buffer.write(b"q"*180000);sys.stdout.flush();sys.exit({reader_exit})')
        decrypt=self.command(f'import sys;sys.stdout.buffer.write(sys.stdin.buffer.read());sys.stdout.flush();sys.exit({gpg_exit})')
        receiver=self.command(f'import sys,json;data=sys.stdin.buffer.read();print(json.dumps({{"bytes":len(data)}}));sys.exit({receiver_exit})')
        return s.pipeline(reader,decrypt,receiver,len(self.data),checksum or hashlib.sha256(self.data).hexdigest(),0.1 if delay else 5)
    def test_success_hashes_complete_stream_and_uses_bounded_pipes(self):
        self.assertEqual(self.run_transfer(),{'bytes':len(self.data)})
    def test_gpg_nonzero_rejected_even_after_all_plaintext_was_written(self):
        with self.assertRaises(RuntimeError):self.run_transfer(gpg_exit=2)
    def test_reader_nonzero_and_receiver_nonzero_rejected(self):
        for args in [{'reader_exit':3},{'receiver_exit':4}]:
            with self.subTest(args=args),self.assertRaises(RuntimeError):self.run_transfer(**args)
    def test_hash_mismatch_blocks_success_after_zero_exits(self):
        with self.assertRaises(RuntimeError):self.run_transfer(checksum='f'*64)
    def test_deadline_terminates_owned_children(self):
        with self.assertRaises(RuntimeError):self.run_transfer(delay=True)
    def test_expired_master_cannot_fall_back_to_new_connection(self):
        args=s.ssh_args('/private/test.sock',['send','a;not-command','1','f'*64])
        self.assertIn('-oProxyCommand=false',args);self.assertIn("'a;not-command'",args[-1])

@unittest.skipUnless(shutil.which('gpg'),'actual GPG unavailable')
class ActualGPGTests(unittest.TestCase):
    def test_actual_decryption_and_final_ciphertext_tamper(self):
        with tempfile.TemporaryDirectory(prefix='cube-stream-gpg-',dir='/private/tmp' if sys.platform=='darwin' else None) as temp:
            root=Path(temp).resolve();home=root/'keys';home.mkdir(mode=0o700)
            base=[shutil.which('gpg'),'--no-options','--homedir',str(home),'--batch','--pinentry-mode','loopback','--passphrase','']
            def command(args):return subprocess.run(args,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True,timeout=30)
            try:
                command(base+['--quick-generate-key','Cube stream disposable fixture','rsa2048','encr','1d'])
                listing=command(base+['--with-colons','--list-keys']).stdout.decode()
                fingerprint=next(line.split(':')[9] for line in listing.splitlines() if line.startswith('fpr:'))
                plain=root/'plain';plain.write_bytes(b'latest fixture SQL/history bytes\n'*1000)
                cipher=root/'cipher.gpg';command(base+['--trust-model','always','--recipient',fingerprint,'--output',str(cipher),'--encrypt',str(plain)])
                receiver=[sys.executable,'-c','import sys,json,hashlib;d=sys.stdin.buffer.read();print(json.dumps({"bytes":len(d),"sha256":hashlib.sha256(d).hexdigest()}))']
                def transfer_file(path):
                    reader=[sys.executable,'-c','import sys;sys.stdout.buffer.write(open(sys.argv[1],"rb").read())',str(path)]
                    return s.pipeline(reader,base+['--decrypt'],receiver,path.stat().st_size,hashlib.sha256(path.read_bytes()).hexdigest(),30)
                result=transfer_file(cipher)
                self.assertEqual(result,{'bytes':plain.stat().st_size,'sha256':hashlib.sha256(plain.read_bytes()).hexdigest()})
                bad=root/'bad.gpg';data=bytearray(cipher.read_bytes());data[-1]^=1;bad.write_bytes(data)
                # Expected ciphertext hash intentionally matches the damaged
                # input: this must fail on real GPG integrity, not our hash check.
                with self.assertRaises(RuntimeError):transfer_file(bad)
            finally:
                if shutil.which('gpgconf'):subprocess.run([shutil.which('gpgconf'),'--homedir',str(home),'--kill','gpg-agent'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5)

if __name__=='__main__':unittest.main()
