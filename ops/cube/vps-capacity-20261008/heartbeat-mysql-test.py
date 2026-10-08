import json,os,pathlib,secrets,subprocess,time
os.umask(0o077)
root=pathlib.Path('/root/baarcha-heartbeat-test-20261008');root.mkdir(mode=0o700,exist_ok=True)
name='baarcha-heartbeat-mysql-test-20261008'
image=json.loads(subprocess.check_output(['docker','inspect','cube-sandbox-mysql']))[0]['Image']
pw=secrets.token_hex(32);(root/'root.env').write_text('MYSQL_ROOT_PASSWORD='+pw+'\n')
subprocess.run(['docker','run','-d','--name',name,'--label','baarcha.fixture=heartbeat-20261008','--network','none','--cpus','1','--memory','1536m','--memory-swap','1536m','--tmpfs','/var/lib/mysql:rw,size=768m','--env-file',str(root/'root.env'),'-v',str(root/'heartbeat.test')+':/heartbeat.test:ro',image,'--log-bin=mysql-bin','--server-id=91888','--binlog-row-image=MINIMAL','--innodb-buffer-pool-size=64M','--innodb-redo-log-capacity=32M'],check=True,stdout=subprocess.DEVNULL)
def sql(q):
 r=subprocess.run(['docker','exec','-i',name,'sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --batch --skip-column-names'],input=q,text=True,capture_output=True)
 if r.returncode:raise RuntimeError('fixture MySQL unavailable')
 return r.stdout
try:
 for _ in range(180):
  try:sql('SELECT 1');break
  except RuntimeError:time.sleep(1)
 else:raise RuntimeError('fixture database startup timed out')
 sql('CREATE DATABASE cube_heartbeat_test')
 env=dict(os.environ,CUBE_HEARTBEAT_TEST_DSN='root:'+pw+'@unix(/var/run/mysqld/mysqld.sock)/cube_heartbeat_test?parseTime=true')
 r=subprocess.run(['docker','exec','-e','CUBE_HEARTBEAT_TEST_DSN',name,'/heartbeat.test','-test.v','-test.run','TestHeartbeatMySQLInventoryAndLogVolume','-test.timeout','60s'],env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 (root/'result.log').write_text(r.stdout);print(r.stdout)
 if r.returncode:raise RuntimeError('heartbeat SQL integration failed')
finally:
 obj=json.loads(subprocess.check_output(['docker','inspect',name]))[0]
 assert obj['Config']['Labels'].get('baarcha.fixture')=='heartbeat-20261008'
 subprocess.run(['docker','rm','-f',name],check=True,stdout=subprocess.DEVNULL)
 (root/'root.env').unlink()
