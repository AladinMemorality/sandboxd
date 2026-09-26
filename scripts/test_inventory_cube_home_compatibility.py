#!/usr/bin/env python3
"""Metadata scanner fixtures; no Docker, tenant code or network access."""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("inventory-cube-home-compatibility.py")
SID = "01M2P1KJ8086W06ANFAV50KA93"


class InventoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.workspaces = self.root / "workspaces"
        self.home = self.workspaces / SID
        app = self.home / "workspace/app"
        app.mkdir(parents=True)
        (self.home / ".runtimed").mkdir()
        (app / "package.json").write_text('{"dependencies":{"react":"1","vite":"1"}}')
        (app / "sandbox.yaml").write_text("command: pnpm vite\nport: 3000\n")
        self.db = self.root / "state.db"
        with sqlite3.connect(self.db) as conn:
            conn.execute("CREATE TABLE app(id TEXT,runtime_preset TEXT)")
            conn.execute("INSERT INTO app VALUES('app','')")
            conn.execute("CREATE TABLE sandbox(id TEXT,app_id TEXT,status TEXT,workspace_mnt TEXT,container_id TEXT,runtime_provider TEXT)")
            conn.execute("INSERT INTO sandbox VALUES(?,'app','stopped','','fixture','docker')", (SID,))
        self.out = self.root / "private"

    def scan(self):
        return subprocess.run([sys.executable, str(SCRIPT), str(self.out)],
                              env={**os.environ, "CUBE_INVENTORY_WORKSPACES": str(self.workspaces),
                                   "CUBE_INVENTORY_DATABASE": str(self.db)},
                              capture_output=True, text=True, timeout=20)

    def test_private_candidates_preserve_literal_link_and_retain_auth(self):
        tool = self.home / "hubenv/bin"
        tool.mkdir(parents=True)
        (tool / "python3").symlink_to("/usr/bin/python3.13")
        (self.home / ".claude").mkdir()
        (self.home / ".claude/auth.json").write_text("DO_NOT_READ_OR_EMIT_PROVIDER_SECRET")
        (self.home / ".runtimed/token").write_text("DO_NOT_EMIT_RUNTIME_SECRET")
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stderr)
        manifest = json.loads((self.out / "candidate-home-manifests.json").read_text())[SID]
        self.assertEqual(manifest["version"], 2)
        self.assertEqual(manifest["links"], [{"path": "hubenv/bin/python3", "target": "/usr/bin/python3.13", "kind": "python-interpreter"}])
        self.assertTrue(any(e["path"] == ".claude" and e["disposition"] == "retained" for e in manifest["entries"]))
        for file in self.out.iterdir():
            self.assertNotIn("DO_NOT_", file.read_text())
            self.assertEqual(file.stat().st_mode & 0o077, 0)

    def test_does_not_follow_owner_directory_link(self):
        outside = self.root / "outside"
        outside.mkdir()
        (outside / "secret.txt").write_text("OUTSIDE_DATA")
        (self.home / "custom").symlink_to(outside, target_is_directory=True)
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads((self.out / "review-private.json").read_text())["projects"][0]
        self.assertEqual(report["unreviewed_home_links"], 1)
        for file in self.out.iterdir():
            self.assertNotIn("secret.txt", file.read_text())
            self.assertNotIn("OUTSIDE_DATA", file.read_text())

    def test_cube_home_is_not_read_as_a_docker_source(self):
        cube_id = "01M3D1Q0E1KM1FEM244XVHEC65"
        with sqlite3.connect(self.db) as conn:
            conn.execute("INSERT INTO app VALUES('cube-app','node-postgres')")
            conn.execute("INSERT INTO sandbox VALUES(?,'cube-app','running','','','cube')", (cube_id,))
        # No host home exists for this Cube guest. Its old archive, if present,
        # must not be mistaken for the live source of customer data.
        result = self.scan()
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads((self.out / "review-private.json").read_text())
        self.assertEqual(report["sandbox_count"], 2)
        self.assertEqual(report["already_cube_sandboxes"], [cube_id])
        self.assertEqual(report["apps_without_sandbox"], [])
        self.assertEqual([p["sandbox_id"] for p in report["projects"]], [SID])
        manifests = json.loads((self.out / "candidate-home-manifests.json").read_text())
        self.assertEqual(list(manifests), [SID])

    def test_refuses_symlinked_workspace_ancestor(self):
        original = self.workspaces
        real = self.root / "real-workspaces"
        original.rename(real)
        original.symlink_to(real, target_is_directory=True)
        result = self.scan()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.out / "candidate-home-manifests.json").exists())


if __name__ == "__main__":
    unittest.main()
