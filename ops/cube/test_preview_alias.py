import importlib.util
from pathlib import Path
import sqlite3
import unittest

spec = importlib.util.spec_from_file_location("preview_alias", Path(__file__).with_name("render-preview-alias.py"))
alias = importlib.util.module_from_spec(spec)
spec.loader.exec_module(alias)
SID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"


class PreviewAliasTest(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(":memory:")
        self.addCleanup(self.db.close)
        self.db.executescript("CREATE TABLE sandbox(id TEXT,visibility TEXT,web_port INTEGER); CREATE TABLE sandbox_port(sandbox_id TEXT,port INTEGER);")
        self.db.execute("INSERT INTO sandbox VALUES(?, 'public', 3000)", (SID,))
        self.db.execute("INSERT INTO sandbox_port VALUES(?,3000)", (SID,))

    def render(self, host="shop.preview.example.test", domain="example.test", sid=SID):
        return alias.render(self.db, sid, host, domain)

    def test_routes_public_alias_through_provider_aware_wake(self):
        config = self.render()["http"]
        router = next(iter(config["routers"].values()))
        self.assertEqual(router["service"], "sandbox-wake@file")
        self.assertEqual(router["rule"], "Host(`shop.preview.example.test`)")
        headers = next(iter(config["middlewares"].values()))["headers"]["customRequestHeaders"]
        self.assertEqual(headers, {"Host": f"s-{SID.lower()}-3000.preview.example.test"})
        self.assertNotIn("@docker", str(config))

    def test_refuses_private_or_missing_sandbox(self):
        self.db.execute("UPDATE sandbox SET visibility='private'")
        with self.assertRaises(ValueError): self.render()
        self.db.execute("DELETE FROM sandbox")
        with self.assertRaises(ValueError): self.render()

    def test_refuses_control_or_unexposed_ports(self):
        for port in (3031, 49983, 0, 65536, 3001):
            self.db.execute("UPDATE sandbox SET web_port=?", (port,))
            with self.subTest(port=port), self.assertRaises(ValueError): self.render()

    def test_refuses_rule_injection_and_reserved_hostnames(self):
        for host in ("x`) || Host(`other.test", "*.example.test", "https://x.test", "api.example.test", "s-other.preview.test", "x.test:443", "x..test", "x.test\n"):
            with self.subTest(host=host), self.assertRaises(ValueError): self.render(host=host)
        with self.assertRaises(ValueError): self.render(domain="example.test/path")
        with self.assertRaises(ValueError): self.render(sid="../other")


if __name__ == "__main__":
    unittest.main()
