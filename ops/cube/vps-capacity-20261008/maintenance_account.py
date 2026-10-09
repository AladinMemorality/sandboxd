"""Hold platform account locks only for the explicitly selected maintenance apps."""
import contextlib,json,pathlib,select,subprocess,time
ROOT=pathlib.Path('/opt/baarcha/operations/vps-50-profiles-20261008')
@contextlib.contextmanager
def account_maintenance(ids, journal):
    assert ids and len(ids)<=50 and len(set(ids))==len(ids)
    assert all(len(sid)==26 and sid.isalnum() for sid in ids)
    with (journal/'account-maintenance.PRIVATE.log').open('ab') as log:
        command=['/opt/baarcha/node22/bin/node','--env-file=/opt/baarcha/landing.env',str(ROOT/'maintenance-account.mjs'),*ids]
        process=subprocess.Popen(command,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=log,bufsize=0)
        try:
            deadline=time.monotonic()+1810
            while True:
                assert time.monotonic()<deadline,'Account maintenance lease timed out'
                assert select.select([process.stdout],[],[],30)[0] or process.poll() is None,'Account guard exited'
                if not select.select([process.stdout],[],[],0)[0]:continue
                line=process.stdout.readline();assert line,'Account guard exited; private diagnostic retained'
                log.write(line);log.flush()
                if line.startswith(b'MAINTENANCE_READY='):
                    value=json.loads(line.split(b'=',1)[1]);assert value['sandboxes']==ids
                    break
            yield
        finally:
            process.stdin.close();process.stdin=None
            try:
                output,_=process.communicate(timeout=180);log.write(output)
                assert process.returncode==0,'Account guard did not finish its accounting checkpoint'
            except subprocess.TimeoutExpired:
                process.terminate();output,_=process.communicate(timeout=30);log.write(output)
                raise
            finally:log.flush()
