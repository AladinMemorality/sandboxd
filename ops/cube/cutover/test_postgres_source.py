import importlib.util
import os
from pathlib import Path
import socket
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('postgres_source', Path(__file__).with_name('postgres_source.py'))
pg = importlib.util.module_from_spec(spec); spec.loader.exec_module(pg)


class PostgresSourceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir='/tmp')
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name).resolve()
        self.uid = os.getuid()
        for name in ('.myhometroc/pgdata/global', '.myhometroc/socket', '.runtimed'):
            (self.home / name).mkdir(parents=True, exist_ok=True)
        self.write('.myhometroc/pgdata/PG_VERSION', b'18\n')
        self.write('.myhometroc/pgdata/global/pg_control', b'c' * 8192)
        self.write(pg.LOG, b'2026-09-26 [42] LOG: database system is ready to accept connections\n')
        self.write(pg.PID, b'42\n')
        self.manifest = {'entries': [{'path': '.myhometroc', 'disposition': 'preserve'}]}

    def write(self, name, data): (self.home / name).write_bytes(data)

    def capture(self): return pg.capture(self.home, True, self.uid)

    def shutdown(self, pid=42):
        (self.home / pg.PID).unlink()
        with (self.home / pg.LOG).open('ab') as out:
            out.write(f'2026-09-26 [{pid}] LOG: database system is shut down\n'.encode())

    def proof(self, before): return pg.stopped(self.home, before, self.uid)

    def test_exact_live_socket_can_only_defer_online_validation(self):
        sock = socket.socket(socket.AF_UNIX); self.addCleanup(sock.close)
        sock.bind(str(self.home / pg.SOCKET))
        proof = pg.live_socket_exception(self.home, self.manifest, self.uid)
        self.assertTrue(proof['online_only'] and proof['frozen_validation_required'])
        before = self.capture(); self.shutdown()
        with self.assertRaisesRegex(RuntimeError, 'live marker'): self.proof(before)
        (self.home / pg.SOCKET).unlink()
        self.assertTrue(self.proof(before)['clean_shutdown_log'])

    def test_additional_special_file_is_not_waived(self):
        sock = socket.socket(socket.AF_UNIX); self.addCleanup(sock.close)
        sock.bind(str(self.home / pg.SOCKET))
        os.mkfifo(self.home / '.myhometroc/pipe')
        with self.assertRaisesRegex(RuntimeError, 'special home file'):
            pg.live_socket_exception(self.home, self.manifest, self.uid)

    def test_clean_shutdown_proof_binds_source_control_and_log_bytes(self):
        before = self.capture(); self.shutdown()
        first = self.proof(before)
        self.assertEqual(first, self.proof(before))
        self.write('.myhometroc/pgdata/global/pg_control', b'd' * 8192)
        self.assertNotEqual(first, self.proof(before))

    def test_shutdown_must_acknowledge_current_postmaster(self):
        before = self.capture(); self.shutdown(pid=43)
        with self.assertRaisesRegex(RuntimeError, 'current PostgreSQL process'): self.proof(before)

    def test_old_shutdown_line_does_not_acknowledge_new_stop(self):
        self.write(pg.LOG, b'2026-09-26 [42] LOG: database system is shut down\n')
        before = self.capture(); (self.home / pg.PID).unlink()
        with self.assertRaisesRegex(RuntimeError, 'current PostgreSQL process'): self.proof(before)

    def test_forced_stop_without_shutdown_acknowledgment_is_refused(self):
        before = self.capture(); (self.home / pg.PID).unlink()
        with self.assertRaisesRegex(RuntimeError, 'not acknowledged'): self.proof(before)

    def test_log_rotation_is_refused(self):
        before = self.capture(); self.shutdown()
        (self.home / pg.LOG).rename(self.home / '.runtimed/old.log')
        self.write(pg.LOG, b'2026-09-26 [42] LOG: database system is shut down\n')
        with self.assertRaisesRegex(RuntimeError, 'generation changed'): self.proof(before)

    def test_shutdown_lock_must_be_removed(self):
        before = self.capture(); self.shutdown(); self.write(pg.SOCKET + '.lock', b'42')
        with self.assertRaisesRegex(RuntimeError, 'live marker'): self.proof(before)

    def test_symlinked_control_file_is_refused(self):
        before = self.capture(); self.shutdown()
        control = self.home / '.myhometroc/pgdata/global/pg_control'
        control.rename(control.with_name('other')); control.symlink_to('other')
        with self.assertRaisesRegex(RuntimeError, 'contains a link'): self.proof(before)

    def test_other_postgres_major_is_refused(self):
        self.write('.myhometroc/pgdata/PG_VERSION', b'17\n')
        with self.assertRaisesRegex(RuntimeError, 'PostgreSQL 18'): self.capture()


if __name__ == '__main__': unittest.main()
