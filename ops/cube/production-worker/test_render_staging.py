import importlib.util
import json
from pathlib import Path
import stat
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("staging", Path(__file__).with_name("render-staging.py"))
staging = importlib.util.module_from_spec(spec)
spec.loader.exec_module(staging)


class StagingTest(unittest.TestCase):
    def test_private_credentials_distinct_and_no_external_side_effects(self):
        with tempfile.TemporaryDirectory() as tmp:
            one, two = Path(tmp) / "one", Path(tmp) / "two"
            key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixtureOnly operator"
            staging.render(one, key)
            staging.render(two, key)
            self.assertEqual(stat.S_IMODE(one.stat().st_mode), 0o700)
            for path in one.iterdir():
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            env = dict(line.split("=", 1) for line in (one / "cube-install.env").read_text().splitlines() if line and not line.startswith("#"))
            self.assertEqual(len(env["CUBE_API_KEY"]), 64)
            self.assertIn(env["CUBE_SANDBOX_MYSQL_PASSWORD"], env["DATABASE_URL"])
            self.assertNotEqual((one / "cube-install.env").read_bytes(), (two / "cube-install.env").read_bytes())
            for path in one.iterdir():
                if path.name != "cube-install.env":
                    self.assertNotIn(env["CUBE_API_KEY"], path.read_text())
            unit = (one / "baarcha-cube-worker-01.service").read_text()
            self.assertIn("hostfwd=tcp:127.0.0.1:20300-:3000", unit)
            self.assertNotIn("19222", unit)
            self.assertNotIn("-virtfs", unit)
            self.assertIn("-m 40960 -smp 12", unit)
            self.assertIn("MemoryMax=44G", unit)
            self.assertIn("CPUQuota=1000%", unit)
            plan = json.loads((one / "plan.json").read_text())
            self.assertFalse(plan["production_activation"])
            self.assertEqual(plan["data_disk_gib"], 320)
            self.assertFalse(plan["capacity"]["is_enforced_admission_limit"])
            self.assertEqual(plan["capacity"]["guest_memory_mib"], 2048)
            self.assertEqual(plan["capacity"]["guest_cpu_millicores"], 2000)
            self.assertEqual(plan["capacity"]["planned_active_and_waking_guests"], 12)
            self.assertEqual(plan["capacity"]["host_guest_memory_quota_mib"], 30720)
            self.assertEqual(env["CUBEMASTER_HTTP_BIND"], "0.0.0.0")
            self.assertEqual(plan["worker_binary"], "UNSELECTED")
            before = (one / "cube-install.env").read_bytes()
            with self.assertRaises(ValueError):
                staging.render(one, key)
            self.assertEqual(before, (one / "cube-install.env").read_bytes())

    def test_rejects_injection_private_keys_and_benchmark_paths(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "out"
            for key, name in [("-----BEGIN OPENSSH PRIVATE KEY-----", "worker-01"),
                              ("ssh-ed25519 AAAA\nruncmd: bad", "worker-01"),
                              ("ssh-ed25519 AAAA", "worker-01;bad"),
                              ("ssh-ed25519 AAAA", "../worker-01")]:
                with self.assertRaises(ValueError):
                    staging.render(out, key, name)
                self.assertFalse(out.exists())
            with self.assertRaises(ValueError):
                staging.render(Path(tmp) / "benchmark", "ssh-ed25519 AAAA")


if __name__ == "__main__":
    unittest.main()
