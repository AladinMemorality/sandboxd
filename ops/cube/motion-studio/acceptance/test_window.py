import importlib.util
from pathlib import Path
import sqlite3
import unittest

spec = importlib.util.spec_from_file_location('window', Path(__file__).with_name('window.py'))
w = importlib.util.module_from_spec(spec); spec.loader.exec_module(w)


class Fake:
    def __init__(self, clean=True): self.calls, self.clean = [], clean
    def __getattr__(self, name):
        def call(*args):
            self.calls.append(name)
            if name == 'acceptance':
                if not self.clean: raise RuntimeError('unsettled allocation')
                return {'accepted': False, 'fixtures_cleaned': True}
        return call


class WindowTests(unittest.TestCase):
    def test_failed_probe_restores_only_after_verified_cleanup(self):
        h = Fake(); sequence = w.Sequence(h, lambda *args: None)
        result = sequence.start(sequence.stop())
        self.assertFalse(result['accepted'])
        self.assertTrue(result['online_restored'])
        self.assertEqual(h.calls, ['preflight', 'drain', 'acceptance', 'restoration_scope', 'restore_controller', 'ready_fence', 'reopen', 'close_nested'])
        self.assertEqual(result['worker_power_operations'], 0)

    def test_unsettled_create_never_reopens(self):
        h = Fake(False); sequence = w.Sequence(h, lambda *args: None)
        with self.assertRaises(RuntimeError): sequence.start(sequence.stop())
        self.assertEqual(h.calls, ['preflight', 'drain', 'acceptance'])

    def test_customer_state_includes_binary_config_and_all_unrelated_tables(self):
        with sqlite3.connect(':memory:') as db:
            db.executescript('CREATE TABLE app_config(value BLOB); INSERT INTO app_config VALUES(X\'00FF\'); CREATE TABLE task(id TEXT); CREATE TABLE cube_admission(value TEXT);')
            baseline = w.customer_state(db)
            db.execute("INSERT INTO cube_admission VALUES('owned fixture')")
            self.assertEqual(w.customer_state(db), baseline)
            db.execute("INSERT INTO task VALUES('unexpected')")
            self.assertNotEqual(w.customer_state(db), baseline)
            db.execute('DELETE FROM task')
            db.execute("UPDATE app_config SET value=X'00FE'")
            self.assertNotEqual(w.customer_state(db), baseline)


if __name__ == '__main__': unittest.main()
