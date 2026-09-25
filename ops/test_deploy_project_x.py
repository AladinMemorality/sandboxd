"""Exercise the real shell release script with fake Docker/isolation commands.

Git/worktrees, SQLite WAL backup, filesystem preservation, flock and loopback
authenticated readiness are real. No Docker daemon or production path is used.
Run: python3 -m unittest discover -s ops -p test_deploy_project_x.py -v
"""
import fcntl
import http.server
import json
import os
from pathlib import Path
import sqlite3
import stat
import subprocess
import tempfile
import threading
import unittest

SCRIPT = Path(__file__).with_name("deploy-project-x.sh").resolve()
OLD_CP = "sha256:" + "1" * 64
OLD_BASE = "sha256:" + "2" * 64
NEW_CP = "sha256:" + "3" * 64
NEW_BASE = "sha256:" + "4" * 64

DOCKER = r'''#!/usr/bin/env python3
import json,os,sys,sqlite3,stat
from pathlib import Path
a=sys.argv[1:]; root=Path(os.environ['FAKE_ROOT']); p=root/'docker.json'; s=json.loads(p.read_text())
with (root/'commands.jsonl').open('a') as f:f.write(json.dumps(a)+'\n')
fail=os.environ.get('FAIL_MODE','')
def save():p.write_text(json.dumps(s))
def cid():return s.get('id','a'*64)
def image(name):
 if name.startswith('sha256:'):return name
 if name not in s['images']:sys.exit(1)
 return s['images'][name]
def env():
 return dict(line.split('=',1) for line in (root/'src/.env').read_text().splitlines() if '=' in line)
def config():
 e=env(); services={'sandboxd':{'environment':e,'ports':[{'target':9000,'published':e['TEST_PORT'],'host_ip':'127.0.0.1'}]}}
 for name in ('runtime-compose.json','active-images.json'):
  p=root/'deploy-state'/name
  if not p.exists():continue
  for service, fields in json.loads(p.read_text())['services'].items():
   target=services.setdefault(service,{})
   for key,value in fields.items():
    if key=='environment':target.setdefault(key,{}).update(value)
    else:target[key]=value
 return {'services':services}
def inspect():
 e=dict(config()['services']['sandboxd']['environment'])
 if 'cube' in s:e['SANDBOXD_CUBE_ENABLED']=s['cube']
 return [{'Id':cid(),'Image':s['current'],'State':{'Running':s['running']},'Config':{'Env':[k+'='+v for k,v in e.items()],'Labels':{'com.docker.compose.project':'src'}}}]
if a[0]=='compose':
 command=next(x for x in a if x in ['ps','config','up','stop'])
 if command=='ps':print(a[-1] if a[-1].startswith('cube-management-') else cid())
 elif command=='config':
  print(json.dumps(config()))
 elif command=='stop':
  if fail=='stop':sys.exit(1)
  s['running']=False;s['phase']='stopped';save()
 elif command=='up':
  if a[-2:]==['cube-management-api','cube-management-proxy']:
   assert '--force-recreate' in a and '--no-deps' in a
   if fail=='relay' and s['phase']=='candidate':sys.exit(1)
   sys.exit(0)
  assert a[-1]=='sandboxd' and all(x in a for x in ['--no-deps','--no-build','--pull','never'])
  files=[a[i+1] for i,x in enumerate(a) if x=='-f']; active=json.load(open(files[-1]))['services']['sandboxd']
  assert config()['services']['sandboxd']['environment'].get('SANDBOXD_CUBE_ENABLED','false')==env().get('SANDBOXD_CUBE_ENABLED','false')
  s['current']=image(active['image']);s['running']=True;s['phase']='candidate' if ':release-' in active['image'] else 'rollback';s['id']=('b' if s['phase']=='candidate' else 'c')*64;save()
  if s['phase']=='candidate':
   if fail=='pin_after':(root/'src/traefik/dynamic/myhometroc.yml').write_text('changed-after-start')
   with sqlite3.connect(root/'data/state/sandboxd.db') as db:db.execute("insert into events values ('accepted-during-release')")
   if fail=='pin_drift':
    config=root/'worker/worker-stop.json';value=json.loads(config.read_text());value['receipt']='operator-changed';config.write_text(json.dumps(value))
   if fail=='pin_boot':(root/'worker/boot').write_text('ffffffff-ffff-ffff-ffff-ffffffffffff')
   if fail=='pin_schema':
    with sqlite3.connect(root/'data/state/sandboxd.db') as db:db.execute('INSERT INTO migration VALUES(35)')
   if fail in ['up','stop']:sys.exit(1)
   if fail=='image':s['current']='sha256:'+'9'*64;save()
elif a[0]=='inspect':
 if '-f' in a:print(s['current'])
 elif a[-2:]==['cube-management-api','cube-management-proxy']:
  namespace='container:obsolete' if fail=='namespace' and s['phase']=='candidate' else 'container:'+cid()
  relay={'HostConfig':{'NetworkMode':namespace},'State':{'Running':True,'Health':{'Status':'healthy'}}}
  print(json.dumps(inspect()+[relay,relay]))
 else:print(json.dumps(inspect()))
elif a[:2]==['image','inspect']:
 name=a[-1];ident=image(name)
 if '-f' in a and 'Labels' in a[a.index('-f')+1]:print(s.get('labels',{}).get(name,''))
 elif '-f' in a:print(ident)
 else:print(json.dumps([{'Id':ident}]))
elif a[:2]==['image','tag']:
 s['images'][a[-1]]=image(a[-2]);save()
elif a[0]=='build':
 tag=a[a.index('-t')+1]
 if fail=='build' and tag.startswith('sandboxd-control-plane:'):sys.exit(1)
 s['images'][tag]='sha256:'+('3' if tag.startswith('sandboxd-control-plane:') else '4')*64
 s.setdefault('labels',{})[tag]=a[a.index('--label')+1].split('=',1)[1];save()
elif a[0]=='run':
 assert all(x in a for x in ['--rm','--init','--network','none','--cpus','--memory','1g'])
 assert a[a.index('--name')+1].startswith('sandboxd-deploy-check-')
 for i,arg in enumerate(a):
  if arg=='-v':
   source,target,mode=a[i+1].rsplit(':',2); source=Path(source)
   assert mode=='ro' and source.is_file()
   assert source.parent.name=='check-fixtures'
   assert stat.S_IMODE(source.stat().st_mode)==0o644, 'Remapped sandbox user cannot read private worktree fixture'
 if a[-1]=='version':
  sha=next(x.split(':release-',1)[1] for x in a if x.startswith('sandboxd-control-plane:release-'))
  print('sandboxd '+sha+' ('+sha[:12]+')')
 elif fail=='acceptance':sys.exit(1)
elif a[:2]==['rm','-f']:
 assert a[-1].startswith('sandboxd-deploy-check-')
elif a[0]=='exec':
 assert a[1:4]==['-i',cid(),'curl'] and a[-2:]==['--config','-']
 request=sys.stdin.read()
 assert 'url = ' in request and '/sandboxes?limit=1' in request
 print('503' if fail=='cube_api' and s['phase']=='candidate' else ('200' if 'X-API-Key:' in request else '401'),end='')
else:raise Exception('unexpected Docker command '+repr(a))
'''

ISOLATION = '''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
r=Path(os.environ['FAKE_ROOT']);p=r/'docker.json';s=json.loads(p.read_text())
with (r/'commands.jsonl').open('a') as f:f.write(json.dumps(['isolation',s['phase']])+'\\n')
if os.environ.get('FAIL_MODE')=='isolation' and s['phase']=='candidate':sys.exit(1)
'''


class DeployTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="runtime-deploy-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.src = self.root / "src"
        self.src.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "docker").write_text(DOCKER)
        (self.bin / "flock").write_text("#!/usr/bin/env python3\nimport fcntl,sys\nfcntl.flock(int(sys.argv[-1]),fcntl.LOCK_EX|fcntl.LOCK_NB)\n")
        # The fake Docker checks finish immediately. The outer test timeout
        # remains real; production uses the host's GNU timeout for image tests.
        (self.bin / "timeout").write_text("#!/usr/bin/env python3\nimport os,sys\na=sys.argv[1:]\nwhile a[0].startswith('--'):a.pop(0)\na.pop(0)\nos.execvp(a[0],a)\n")
        (self.bin / 'ssh').write_text("#!/usr/bin/env python3\nimport os,pathlib\nprint((pathlib.Path(os.environ['FAKE_ROOT'])/'worker/boot').read_text().strip())\n")
        for path in self.bin.iterdir():
            path.chmod(0o755)
        self.git("init", "-q")
        self.git("config", "user.email", "deploy-test@invalid.example")
        self.git("config", "user.name", "Synthetic deploy test")
        for directory in ("host", "image", "control-plane", "traefik/dynamic", "scripts", "image/services/postgres"):
            (self.src / directory).mkdir(parents=True, exist_ok=True)
        (self.src / "host/sandbox-isolation.sh").write_text(ISOLATION)
        (self.src / "host/sandbox-isolation.sh").chmod(0o755)
        (self.src / "docker-compose.yml").write_text("services: {sandboxd: {image: old}}\n")
        (self.src / "image/Dockerfile").write_text("FROM old\n")
        (self.src / "control-plane/Dockerfile").write_text("FROM old\n")
        for fixture in ("scripts/vite-reload-regression.mjs", "image/services/postgres/paths.test.mjs", "image/services/postgres/worker.test.mjs"):
            (self.src / fixture).write_text("// Checked-in public fixture: " + fixture + "\n")
        self.git("add", ".")
        self.git("commit", "-qm", "old")
        self.old = self.git("rev-parse", "HEAD")
        (self.src / "release.txt").write_text("candidate")
        self.git("add", ".")
        self.git("commit", "-qm", "candidate")
        self.sha = self.git("rev-parse", "HEAD")
        self.git("remote", "add", "origin", str(self.src))
        self.git("checkout", "--detach", self.old)
        self.alias = self.src / "traefik/dynamic/myhometroc.yml"
        self.alias.write_text("private-existing-alias: preserve-exactly\n")
        data = self.root / "data/state"
        data.mkdir(parents=True)
        self.db = sqlite3.connect(data / "sandboxd.db")
        self.db.execute("PRAGMA journal_mode=WAL")
        self.db.execute("create table events (value text)")
        self.db.execute("insert into events values ('committed-in-WAL')")
        self.db.commit()
        self.addCleanup(self.db.close)
        test = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                state = json.loads((test.root / "docker.json").read_text())
                authenticated = self.headers.get("Authorization") == "Bearer synthetic-token"
                code = 200 if self.path in ("/healthz", "/readyz") or authenticated else 401
                if test.mode == "auth" and state["phase"] == "candidate":
                    code = 200
                self.send_response(code)
                self.end_headers()

        self.http = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.http.serve_forever, daemon=True).start()
        self.addCleanup(self.http.server_close)
        self.addCleanup(self.http.shutdown)
        self.env_text = f"SANDBOXD_DATA_DIR={self.root}/data\nSANDBOXD_IMAGE=sandboxd-base:0.3.0\nSANDBOXD_API_TOKENS=default=synthetic-token\nSANDBOXD_VERSION=old\nTEST_PORT={self.http.server_port}\n"
        (self.src / ".env").write_text(self.env_text)
        (self.root / "docker.json").write_text(json.dumps({"images": {"sandboxd-base:0.3.0": OLD_BASE, "sandboxd-control-plane:0.3.0": OLD_CP}, "current": OLD_CP, "running": True, "phase": "old"}))
        self.mode = ""

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.src), *args], stderr=subprocess.DEVNULL, text=True).strip()

    def deploy(self, mode=""):
        self.mode = mode
        env = {**os.environ, "PATH": str(self.bin) + os.pathsep + os.environ["PATH"], "FAKE_ROOT": str(self.root), "FAIL_MODE": mode,
               "PROJECT_X_SRC_DIR": str(self.src), "PROJECT_X_DEPLOY_STATE": str(self.root / "deploy-state"), "PROJECT_X_WORKER_STOP_CONFIG": str(self.root / "worker/worker-stop.json")}
        return subprocess.run(["bash", str(SCRIPT), self.sha], env=env, text=True, capture_output=True, timeout=20)

    def commands(self):
        return [json.loads(line) for line in (self.root / "commands.jsonl").read_text().splitlines()]

    def assert_preserved(self):
        self.assertEqual(self.alias.read_text(), "private-existing-alias: preserve-exactly\n")
        self.assertFalse(any("down" in c or "--remove-orphans" in c or c[:2] == ["container", "rm"] for c in self.commands()))

    def test_success_builds_before_activation_and_online_backup_includes_wal(self):
        result = self.deploy()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.sha)
        state = json.loads((self.root / "docker.json").read_text())
        self.assertEqual(state["current"], NEW_CP)
        self.assertEqual(state["images"]["sandboxd-base:0.3.0"], NEW_BASE)
        commands = self.commands()
        self.assertLess(max(i for i, c in enumerate(commands) if c[0] == "build"), next(i for i, c in enumerate(commands) if "up" in c))
        self.assertEqual(sum(c[0] == "run" for c in commands), 4)
        self.assertLess(max(i for i, c in enumerate(commands) if c[0] == "run"), next(i for i, c in enumerate(commands) if "up" in c))
        backup = next((self.root / "deploy-state/releases").glob("*/sandboxd.backup.sqlite"))
        release = backup.parent
        # Real Git creates the image build context. Source is public and Docker
        # COPY keeps these modes; a remapped sandbox user needs to read it.
        # This catches the original 077 umask producing root-owned 0600 image
        # files even when separately staged test bind mounts were readable.
        source = release / "source"
        for tracked in ("image/Dockerfile", "control-plane/Dockerfile", "scripts/vite-reload-regression.mjs", "image/services/postgres/paths.test.mjs"):
            self.assertEqual(stat.S_IMODE((source / tracked).stat().st_mode), 0o644)
        self.assertEqual(stat.S_IMODE((source / "host/sandbox-isolation.sh").stat().st_mode), 0o755)
        self.assertEqual(stat.S_IMODE(source.stat().st_mode), 0o755)
        fixtures = release / "check-fixtures"
        self.assertEqual({p.name for p in fixtures.iterdir()}, {"vite-reload-regression.mjs", "paths.test.mjs", "worker.test.mjs"})
        for copied in fixtures.iterdir():
            self.assertEqual(stat.S_IMODE(copied.stat().st_mode), 0o644)
            self.assertTrue(copied.read_text().startswith("// Checked-in public fixture:"))
        for private_dir in (release, fixtures, self.root / "deploy-state"):
            self.assertEqual(stat.S_IMODE(private_dir.stat().st_mode), 0o700)
        for private_file in (release / "env.before", release / "config.json", release / "container.before.json", backup):
            self.assertEqual(stat.S_IMODE(private_file.stat().st_mode), 0o600)
        with sqlite3.connect(backup) as db:
            self.assertEqual(db.execute("select value from events").fetchall(), [("committed-in-WAL",)])
        self.assertEqual(self.db.execute("select count(*) from events").fetchone()[0], 2)
        self.assert_preserved()

    def test_build_failure_does_not_change_live_checkout_env_or_mutable_tag(self):
        result = self.deploy("build")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.old)
        self.assertEqual((self.src / ".env").read_text(), self.env_text)
        self.assertEqual(json.loads((self.root / "docker.json").read_text())["images"]["sandboxd-base:0.3.0"], OLD_BASE)
        self.assertFalse(any("up" in c or "stop" in c for c in self.commands()))
        self.assert_preserved()

    def test_failures_stop_candidate_then_restore_old_code_without_rewinding_writes(self):
        for mode in ("up", "isolation", "image", "auth"):
            with self.subTest(mode=mode):
                if mode != "up":
                    self.tearDown()
                    self.doCleanups()
                    self.setUp()
                result = self.deploy(mode)
                self.assertNotEqual(result.returncode, 0)
                state = json.loads((self.root / "docker.json").read_text())
                self.assertEqual(state["current"], OLD_CP, result.stderr)
                self.assertTrue(state["running"], result.stderr)
                self.assertEqual(self.git("rev-parse", "HEAD"), self.old)
                self.assertEqual((self.src / ".env").read_text(), self.env_text)
                self.assertEqual(self.db.execute("select count(*) from events").fetchone()[0], 2)
                commands = self.commands()
                ups = [i for i, c in enumerate(commands) if "up" in c]
                stop = next(i for i, c in enumerate(commands) if "stop" in c)
                self.assertLess(ups[0], stop)
                self.assertLess(stop, ups[1])
                self.assert_preserved()

    def test_stop_failure_prevents_rollback_source_and_data_changes(self):
        result = self.deploy("stop")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.sha)
        self.assertEqual(self.db.execute("select count(*) from events").fetchone()[0], 2)
        self.assertEqual(sum("up" in c for c in self.commands()), 1)

    def test_cube_enabled_refused_before_build(self):
        state = json.loads((self.root / "docker.json").read_text())
        state["cube"] = "true"
        (self.root / "docker.json").write_text(json.dumps(state))
        self.assertNotEqual(self.deploy().returncode, 0)
        self.assertFalse(any(c[0] == "build" or "up" in c for c in self.commands()))

    def test_reverse_egress_enabled_refused_before_build(self):
        with (self.src / ".env").open("a") as f:
            f.write("SANDBOXD_CUBE_REVERSE_EGRESS=true\n")
        self.assertNotEqual(self.deploy().returncode, 0)
        self.assertFalse(any(c[0] == "build" or "up" in c for c in self.commands()))

    def test_existing_cube_ownership_refused_even_when_disabled(self):
        self.db.execute("create table app_runtime (provider text)")
        self.db.execute("insert into app_runtime values ('cube')")
        self.db.commit()
        self.assertNotEqual(self.deploy().returncode, 0)
        self.assertFalse(any(c[0] == "build" or "up" in c for c in self.commands()))

    def enable_existing_cube(self):
        cube = {
            "SANDBOXD_CUBE_ENABLED": "true", "SANDBOXD_CUBE_ROLLOUT": "global",
            "SANDBOXD_CUBE_REVERSE_EGRESS": "true", "SANDBOXD_CUBE_AGENT_RELAY_NETWORK_VERIFIED": "true",
            "SANDBOXD_CUBE_EGRESS_CLIENT_PROFILE": "proxy-http-v1", "SANDBOXD_CUBE_API_KEY": "synthetic-cube-key",
            "SANDBOXD_CUBE_API_URL": "http://127.0.0.1:20300",
            "SANDBOXD_CUBE_TEMPLATES": '{"react-pro":"synthetic-template"}',
        }
        self.env_text += "".join(k + "=" + v + "\n" for k, v in cube.items())
        (self.src / ".env").write_text(self.env_text)
        state = self.root / "deploy-state"
        state.mkdir()
        relay = {"network_mode": "service:sandboxd", "image": "relay@sha256:" + "5" * 64}
        (state / "runtime-compose.json").write_text(json.dumps({"services": {
            "sandboxd": {"environment": cube}, "cube-management-api": relay, "cube-management-proxy": relay,
        }}))
        (state / "active-images.json").write_text(json.dumps({"services": {"sandboxd": {
            "image": OLD_CP, "environment": cube, "volumes": ["/synthetic/accepted:/synthetic/accepted:ro"],
        }}}))
        self.db.execute("create table app_runtime (provider text)")
        self.db.execute("insert into app_runtime values ('cube')")
        self.db.commit()
        self.install_worker_config()
        return cube

    def install_worker_config(self):
        directory = self.root / "worker"
        directory.mkdir(mode=0o700)
        migrations = directory / "migrations"
        migrations.mkdir(mode=0o700)
        for number in range(1, 35):
            (migrations / f"{number:04d}_fixture.sql").write_text("-- fixture\n")
        self.db.execute("CREATE TABLE migration(id INTEGER PRIMARY KEY)")
        self.db.executemany("INSERT INTO migration VALUES(?)", [(i,) for i in range(1, 35)])
        self.db.commit()
        boot = "11111111-1111-1111-1111-111111111111"
        (directory / "boot").write_text(boot)
        config = {"version": 1, "controller_id": "a" * 64, "database": str(self.root / "data/state/sandboxd.db"),
                  "worker_boot_id": boot, "worker_machine_id": "machine", "data_uuid": "data-uuid", "migrations": str(migrations),
                  "receipt": "retained-private-receipt", "api_key": "not-in-logs",
                  "admission": {"storage_guard": {"expected_boot_id": boot, "worker_machine_id": "machine", "inner_fs_uuid": "data-uuid"}}}
        (directory / "worker-stop.json").write_text(json.dumps(config))
        (directory / "worker-stop.json").chmod(0o600)
        return config

    def test_configured_docker_release_also_refreshes_pin(self):
        before = self.install_worker_config()
        result = self.deploy()
        self.assertEqual(result.returncode, 0, result.stderr)
        after = json.loads((self.root / "worker/worker-stop.json").read_text())
        self.assertEqual(after, dict(before, controller_id="b" * 64))
        self.assertNotIn("not-in-logs", result.stdout + result.stderr)

    def test_stale_pin_refused_before_build(self):
        self.install_worker_config()
        path = self.root / "worker/worker-stop.json"
        config = json.loads(path.read_text()); config["controller_id"] = "d" * 64
        path.write_text(json.dumps(config))
        result = self.deploy()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(c[0] == "build" or "up" in c for c in self.commands()))

    def test_worker_config_permissions_symlink_and_stop_marker_fail_closed(self):
        for mode in ("permissions", "symlink", "marker"):
            with self.subTest(mode=mode):
                if mode != "permissions":
                    self.doCleanups(); self.setUp()
                self.install_worker_config()
                path = self.root / "worker/worker-stop.json"
                if mode == "permissions": path.chmod(0o644)
                elif mode == "symlink":
                    target = path.with_name("retained.json"); path.rename(target); path.symlink_to(target)
                else: (self.root / "data/state/sandboxd.db.worker-stop.json").write_text("retained stop")
                result = self.deploy()
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(c[0] == "build" or "up" in c for c in self.commands()))

    def test_cube_release_requires_installed_worker_configuration(self):
        self.enable_existing_cube()
        (self.root / "worker/worker-stop.json").unlink()
        result = self.deploy()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(c[0] == "build" or "up" in c for c in self.commands()))

    def test_changed_config_or_boot_or_schema_never_overwritten(self):
        for mode in ("pin_drift", "pin_boot", "pin_schema"):
            with self.subTest(mode=mode):
                if mode != "pin_drift":
                    self.doCleanups(); self.setUp()
                original = self.install_worker_config()
                result = self.deploy(mode)
                self.assertNotEqual(result.returncode, 0)
                config = json.loads((self.root / "worker/worker-stop.json").read_text())
                self.assertEqual(config["controller_id"], original["controller_id"])
                if mode == "pin_drift": self.assertEqual(config["receipt"], "operator-changed")
                self.assertFalse(json.loads((self.root / "docker.json").read_text())["running"])

    def test_rollback_refreshes_previously_refreshed_controller_pin(self):
        before = self.install_worker_config()
        result = self.deploy("pin_after")
        self.assertNotEqual(result.returncode, 0)
        after = json.loads((self.root / "worker/worker-stop.json").read_text())
        self.assertEqual(after, dict(before, controller_id="c" * 64))
        receipts = list((self.root / "deploy-state/releases").glob("*/worker-stop-pin-*.json"))
        self.assertEqual(len(receipts), 2)

    def test_cube_release_preserves_configuration_and_reconnects_relays(self):
        cube = self.enable_existing_cube()
        result = self.deploy()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        override = json.loads((self.root / "deploy-state/active-images.json").read_text())["services"]["sandboxd"]
        self.assertEqual(override["environment"], cube)
        self.assertEqual(override["volumes"], ["/synthetic/accepted:/synthetic/accepted:ro"])
        ups = [c for c in self.commands() if "up" in c]
        self.assertEqual(len(ups), 2)
        self.assertEqual(ups[0][-1], "sandboxd")
        self.assertEqual(ups[1][-2:], ["cube-management-api", "cube-management-proxy"])
        self.assertEqual(self.db.execute("select provider from app_runtime").fetchall(), [("cube",)])
        self.assert_preserved()

    def test_cube_relay_failure_rolls_back_code_and_reconnects_old_controller(self):
        cube = self.enable_existing_cube()
        result = self.deploy("relay")
        self.assertNotEqual(result.returncode, 0)
        state = json.loads((self.root / "docker.json").read_text())
        self.assertEqual(state["current"], OLD_CP, result.stderr)
        self.assertTrue(state["running"])
        self.assertEqual(self.git("rev-parse", "HEAD"), self.old)
        override = json.loads((self.root / "deploy-state/active-images.json").read_text())["services"]["sandboxd"]
        self.assertEqual(override["environment"], cube)
        relays = [c for c in self.commands() if "up" in c and c[-2:] == ["cube-management-api", "cube-management-proxy"]]
        self.assertEqual(len(relays), 2)
        self.assertEqual(self.db.execute("select count(*) from events").fetchone()[0], 2)

    def test_cube_release_rejects_public_or_mutable_management_relays(self):
        self.enable_existing_cube()
        path = self.root / "deploy-state/runtime-compose.json"
        config = json.loads(path.read_text())
        config["services"]["cube-management-api"]["ports"] = [{"target": 20300, "published": "20300"}]
        path.write_text(json.dumps(config))
        self.assertNotEqual(self.deploy().returncode, 0)
        self.assertFalse(any(c[0] == "build" or "up" in c for c in self.commands()))

    def test_cube_relay_old_namespace_triggers_rollback(self):
        self.enable_existing_cube()
        result = self.deploy("namespace")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("obsolete controller namespace", result.stderr)
        self.assertEqual(json.loads((self.root / "docker.json").read_text())["current"], OLD_CP)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.old)

    def test_cube_api_failure_after_relay_health_triggers_rollback(self):
        self.enable_existing_cube()
        result = self.deploy("cube_api")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Cube API authentication/readiness failed", result.stderr)
        self.assertEqual(json.loads((self.root / "docker.json").read_text())["current"], OLD_CP)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.old)
        self.assertNotIn("synthetic-cube-key", result.stdout + result.stderr)

    def test_cube_without_prior_image_override_preserves_runtime_on_success_and_rollback(self):
        for mode in ("", "relay"):
            with self.subTest(mode=mode):
                if mode:
                    self.doCleanups()
                    self.setUp()
                self.enable_existing_cube()
                path = self.root / "deploy-state/active-images.json"
                path.unlink()
                result = self.deploy(mode)
                self.assertEqual(result.returncode == 0, not mode, result.stdout + result.stderr)
                override = json.loads(path.read_text())["services"]["sandboxd"]
                self.assertNotIn("environment", override)
                expected = OLD_CP if mode else NEW_CP
                self.assertEqual(json.loads((self.root / "docker.json").read_text())["current"], expected)

    def test_exact_image_acceptance_failure_leaves_live_release_unchanged(self):
        self.assertNotEqual(self.deploy("acceptance").returncode, 0)
        self.assertEqual(self.git("rev-parse", "HEAD"), self.old)
        self.assertEqual(json.loads((self.root / "docker.json").read_text())["images"]["sandboxd-base:0.3.0"], OLD_BASE)
        self.assertFalse(any("up" in c or "stop" in c for c in self.commands()))

    def test_retry_reuses_immutable_images_without_rebuilding(self):
        self.assertEqual(self.deploy().returncode, 0)
        before = sum(c[0] == "build" for c in self.commands())
        result = self.deploy()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sum(c[0] == "build" for c in self.commands()), before)

    def test_concurrent_deployment_lock_is_refused(self):
        path = self.root / "deploy-state"
        path.mkdir()
        with (path / "deploy.lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.assertNotEqual(self.deploy().returncode, 0)
        self.assertFalse((self.root / "commands.jsonl").exists())


if __name__ == "__main__":
    unittest.main()
