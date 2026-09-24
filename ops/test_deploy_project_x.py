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
import json,os,sys,sqlite3
from pathlib import Path
a=sys.argv[1:]; root=Path(os.environ['FAKE_ROOT']); p=root/'docker.json'; s=json.loads(p.read_text())
with (root/'commands.jsonl').open('a') as f:f.write(json.dumps(a)+'\n')
fail=os.environ.get('FAIL_MODE','')
def save():p.write_text(json.dumps(s))
def image(name):
 if name.startswith('sha256:'):return name
 if name not in s['images']:sys.exit(1)
 return s['images'][name]
def env():
 return dict(line.split('=',1) for line in (root/'src/.env').read_text().splitlines() if '=' in line)
def inspect():
 e=env();e['SANDBOXD_CUBE_ENABLED']=s.get('cube','false')
 return [{'Image':s['current'],'State':{'Running':s['running']},'Config':{'Env':[k+'='+v for k,v in e.items()],'Labels':{'com.docker.compose.project':'src'}}}]
if a[0]=='compose':
 command=next(x for x in a if x in ['ps','config','up','stop'])
 if command=='ps':print('controller')
 elif command=='config':
  e=env();print(json.dumps({'services':{'sandboxd':{'environment':e,'ports':[{'target':9000,'published':e['TEST_PORT'],'host_ip':'127.0.0.1'}]}}}))
 elif command=='stop':
  if fail=='stop':sys.exit(1)
  s['running']=False;s['phase']='stopped';save()
 elif command=='up':
  assert a[-1]=='sandboxd' and all(x in a for x in ['--no-deps','--no-build','--pull','never'])
  files=[a[i+1] for i,x in enumerate(a) if x=='-f']; active=json.load(open(files[-1]))['services']['sandboxd']
  assert active['environment']['SANDBOXD_CUBE_ENABLED']=='false'
  s['current']=image(active['image']);s['running']=True;s['phase']='candidate' if ':release-' in active['image'] else 'rollback';save()
  if s['phase']=='candidate':
   with sqlite3.connect(root/'data/state/sandboxd.db') as db:db.execute("insert into events values ('accepted-during-release')")
   if fail in ['up','stop']:sys.exit(1)
   if fail=='image':s['current']='sha256:'+'9'*64;save()
elif a[0]=='inspect':
 if '-f' in a:print(s['current'])
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
 if a[-1]=='version':
  sha=next(x.split(':release-',1)[1] for x in a if x.startswith('sandboxd-control-plane:release-'))
  print('sandboxd '+sha+' ('+sha[:12]+')')
 elif fail=='acceptance':sys.exit(1)
elif a[:2]==['rm','-f']:
 assert a[-1].startswith('sandboxd-deploy-check-')
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
        self.root = Path(self.tmp.name)
        self.src = self.root / "src"
        self.src.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        (self.bin / "docker").write_text(DOCKER)
        (self.bin / "flock").write_text("#!/usr/bin/env python3\nimport fcntl,sys\nfcntl.flock(int(sys.argv[-1]),fcntl.LOCK_EX|fcntl.LOCK_NB)\n")
        # The fake Docker checks finish immediately. The outer test timeout
        # remains real; production uses the host's GNU timeout for image tests.
        (self.bin / "timeout").write_text("#!/usr/bin/env python3\nimport os,sys\na=sys.argv[1:]\nwhile a[0].startswith('--'):a.pop(0)\na.pop(0)\nos.execvp(a[0],a)\n")
        for path in self.bin.iterdir():
            path.chmod(0o755)
        self.git("init", "-q")
        self.git("config", "user.email", "deploy-test@invalid.example")
        self.git("config", "user.name", "Synthetic deploy test")
        for directory in ("host", "image", "control-plane", "traefik/dynamic"):
            (self.src / directory).mkdir(parents=True, exist_ok=True)
        (self.src / "host/sandbox-isolation.sh").write_text(ISOLATION)
        (self.src / "host/sandbox-isolation.sh").chmod(0o755)
        (self.src / "docker-compose.yml").write_text("services: {sandboxd: {image: old}}\n")
        (self.src / "image/Dockerfile").write_text("FROM old\n")
        (self.src / "control-plane/Dockerfile").write_text("FROM old\n")
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
               "PROJECT_X_SRC_DIR": str(self.src), "PROJECT_X_DEPLOY_STATE": str(self.root / "deploy-state")}
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
