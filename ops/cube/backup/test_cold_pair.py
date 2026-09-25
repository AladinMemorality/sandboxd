import contextlib
from datetime import datetime, timezone
import importlib.util
import io
import json
from pathlib import Path
import shutil
import sqlite3
import tarfile
import tempfile
import types
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("cold_pair", Path(__file__).with_name("cold_pair.py"))
cold = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cold)


class ColdPairTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.root.chmod(0o700)
        self.sources = self.root / "sources"
        self.sources.mkdir(mode=0o700)
        for name in ("root", "data", *cold.ROLES):
            (self.sources / name).write_bytes(("synthetic-" + name).encode())
        db = self.sources / "controller.sqlite"
        with contextlib.closing(sqlite3.connect(db)) as connection:
            connection.executescript("CREATE TABLE task(status TEXT); CREATE TABLE cube_admission(state TEXT,charged INTEGER); CREATE TABLE runtime_binding(runtime_id TEXT,provider TEXT); INSERT INTO runtime_binding VALUES('fixture-runtime','cube'); CREATE TABLE note(value TEXT); INSERT INTO note VALUES('newest-fixture-write');")
        (self.sources / "pause-receipt").write_text(json.dumps({"version": 1, "generated_at": datetime.now(timezone.utc).isoformat(), "provider_jobs": 0, "guest_states": {"fixture-runtime": "paused"}}))
        self.worker_lock = self.root / "worker.lock"
        self.worker_lock.write_bytes(cold.WORKER_MARKER)
        self.worker_lock.chmod(0o600)
        lock = Path(str(db) + ".maintenance.lock")
        lock.write_bytes(cold.CONTROLLER_MARKER)
        lock.chmod(0o600)
        self.config = {"root_disk": str(self.sources / "root"), "data_disk": str(self.sources / "data"), "database": str(db), "worker_unit": "baarcha-cube-fixture.service", "worker_lock": str(self.worker_lock), "artifacts": {name: str(self.sources / name) for name in cold.ROLES}}
        self.config_path = self.root / "config.json"
        self.config_path.write_text(json.dumps(self.config))

    def fake_run(self, args):
        if args[0] == "systemctl":
            return ("ActiveState=inactive\nMainPID=0\nExecStart=" + str(Path(cold.__file__).resolve()) + " worker-exec --lock-file " + str(self.worker_lock) + " -- /usr/bin/qemu-system-x86_64\n").encode()
        self.assertEqual(args[0], "qemu-img")
        if args[1] == "info":
            return b'{"format":"qcow2","virtual-size":4096}'
        if args[1] == "convert":
            shutil.copyfile(args[-2], args[-1])
        if args[1] == "compare":
            self.assertEqual(Path(args[-2]).read_bytes(), Path(args[-1]).read_bytes())
        return b""

    def capture(self):
        output = self.root / "captured"
        with mock.patch.object(cold, "native_host"), mock.patch.object(cold, "no_open_users"), mock.patch.object(cold, "run", side_effect=self.fake_run), contextlib.redirect_stdout(io.StringIO()):
            cold.capture(types.SimpleNamespace(config=self.config_path, output=output))
        return output

    def test_synthetic_capture_restore_preserves_latest_sqlite_and_files(self):
        captured = self.capture()
        archive = self.root / "decrypted.tar"
        with tarfile.open(archive, "w", format=tarfile.GNU_FORMAT) as tar:
            for path in captured.iterdir():
                tar.add(path, arcname=path.name)
        output = self.root / "restored"
        with mock.patch.object(cold, "run", side_effect=self.fake_run), contextlib.redirect_stdout(io.StringIO()):
            cold.restore(types.SimpleNamespace(archive=archive, output=output))
        with contextlib.closing(sqlite3.connect(output / "controller.sqlite")) as db:
            self.assertEqual(db.execute("SELECT value FROM note").fetchone()[0], "newest-fixture-write")
        self.assertEqual((output / "data.qcow2").read_bytes(), (self.sources / "data").read_bytes())
        self.assertFalse(json.loads((output / "manifest.json").read_text())["application_restore_verified"])

    def test_only_exact_reviewed_supervisor_entrypoint_is_accepted(self):
        command = "/usr/bin/python3 /usr/local/libexec/baarcha-cube-worker-lifecycle.py supervise --lock-file " + str(self.worker_lock)
        for value, allowed in ((command, True), (command.replace("worker-lifecycle.py", "other.py"), False), (command.replace(" supervise ", " hook "), False)):
            state = ("ActiveState=inactive\nMainPID=0\nExecStart=" + value + "\n").encode()
            with mock.patch.object(cold, "run", return_value=state):
                if allowed:
                    cold.verify_unit(self.config)
                else:
                    with self.assertRaises(Exception):
                        cold.verify_unit(self.config)

    def test_busy_startup_lock_refuses_before_output(self):
        with cold.lock_file(self.worker_lock, cold.WORKER_MARKER, False), mock.patch.object(cold, "native_host"):
            with self.assertRaises(BlockingIOError):
                cold.capture(types.SimpleNamespace(config=self.config_path, output=self.root / "refused"))
        self.assertFalse((self.root / "refused").exists())

    def test_live_or_unfenced_unit_refused(self):
        for state in (b"ActiveState=active\nMainPID=12\n", b"ActiveState=inactive\nMainPID=0\nExecStart=/usr/bin/qemu-system-x86_64\n"):
            with mock.patch.object(cold, "native_host"), mock.patch.object(cold, "run", return_value=state), self.assertRaises(RuntimeError):
                cold.capture(types.SimpleNamespace(config=self.config_path, output=self.root / "refused"))
        self.assertFalse((self.root / "refused").exists())

    def test_pending_allocation_and_incomplete_pause_inventory_refused(self):
        receipt = self.sources / "pause-receipt"
        receipt.write_text(json.dumps({"version": 1, "generated_at": datetime.now(timezone.utc).isoformat(), "provider_jobs": 0, "guest_states": {}}))
        with self.assertRaises(RuntimeError):
            cold.validate_pause_receipt(self.sources / "controller.sqlite", receipt)
        with contextlib.closing(sqlite3.connect(self.sources / "controller.sqlite")) as db:
            db.execute("INSERT INTO cube_admission VALUES('pending',1)")
            db.commit()
        with self.assertRaisesRegex(RuntimeError, "admission"):
            cold.validate_pause_receipt(self.sources / "controller.sqlite", receipt)

    def test_tampered_payload_and_symlink_refused(self):
        captured = self.capture()
        (captured / "controller-key").write_bytes(b"changed")
        with self.assertRaisesRegex(RuntimeError, "integrity"):
            cold.validate_capture(captured, False)
        (captured / "controller-key").unlink()
        (captured / "controller-key").symlink_to(self.sources / "controller-key")
        with self.assertRaisesRegex(RuntimeError, "symlink"):
            cold.validate_capture(captured, False)

    def test_traversal_or_links_never_extract(self):
        archive = self.root / "malicious.tar"
        with tarfile.open(archive, "w", format=tarfile.GNU_FORMAT) as tar:
            member = tarfile.TarInfo("../escaped")
            member.size = 1
            tar.addfile(member, io.BytesIO(b"x"))
        with self.assertRaises(RuntimeError):
            cold.restore(types.SimpleNamespace(archive=archive, output=self.root / "refused"))
        self.assertFalse((self.root / "escaped").exists())
        self.assertFalse((self.root / "refused").exists())

    def test_extension_headers_refused_before_large_metadata_read(self):
        archive = self.root / "oversized-extension.tar"
        entry = tarfile.TarInfo("extended")
        entry.type = tarfile.XHDTYPE
        entry.size = 1 << 40
        archive.write_bytes(entry.tobuf(format=tarfile.GNU_FORMAT))
        with self.assertRaisesRegex(RuntimeError, "tar type"):
            cold.verify_tar_headers(archive, {"root.qcow2"})

    def test_atomic_publication_does_not_replace_existing_directory(self):
        source, target = self.root / "source-stage", self.root / "existing"
        source.mkdir()
        target.mkdir()
        with self.assertRaises(OSError):
            cold.publish_directory(source, target)
        self.assertTrue(source.is_dir())
        self.assertTrue(target.is_dir())

    def test_exact_public_recipient_only(self):
        fingerprint = "A" * 40
        public = "pub:::::::::\nfpr:::::::::" + fingerprint + ":\n"
        cold.public_key_fingerprint(public, fingerprint)
        for text, expected in ((public, "B" * 40), (public.replace("pub:", "sec:"), fingerprint), (public + public, fingerprint)):
            with self.assertRaises(RuntimeError):
                cold.public_key_fingerprint(text, expected)


if __name__ == "__main__":
    unittest.main()


class SealManifestTests(unittest.TestCase):
    def test_sealed_ciphertext_manifest_is_durable_and_no_replace(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory).resolve();source=root/'capture';source.mkdir()
            (source/'manifest.json').write_text(json.dumps({'fixture':True,'captured_at':1234567890}))
            archive=root/'ciphertext.gpg';archive.write_bytes(b'encrypted-fixture');archive.chmod(0o600)
            result=cold.seal_manifest(archive,source,'A'*40)
            path=Path(str(archive)+'.manifest.json');value=json.loads(path.read_text())
            self.assertEqual(value['captured_at'],1234567890)
            self.assertGreater(value['sealed_at'],value['captured_at'])
            self.assertEqual(value['ciphertext_sha256'],cold.digest(archive))
            self.assertEqual(value['capture_manifest_sha256'],cold.digest(source/'manifest.json'))
            self.assertEqual(value['file_identity']['inode'],archive.stat().st_ino)
            self.assertEqual(path.stat().st_mode&0o777,0o600)
            old=path.read_bytes()
            with self.assertRaises(FileExistsError):cold.seal_manifest(archive,source,'B'*40)
            self.assertEqual(path.read_bytes(),old)
            self.assertFalse(result['restore_verified'])

    def test_missing_original_capture_timestamp_cannot_be_sealed_as_fresh(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory).resolve();source=root/'capture';source.mkdir()
            (source/'manifest.json').write_bytes(b'{"fixture":true}')
            archive=root/'ciphertext.gpg';archive.write_bytes(b'encrypted-fixture')
            with self.assertRaises(RuntimeError):cold.seal_manifest(archive,source,'A'*40)
            self.assertFalse(Path(str(archive)+'.manifest.json').exists())
