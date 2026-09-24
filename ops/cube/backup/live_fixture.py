#!/usr/bin/env python3
"""Explicit synthetic-only GPG/qcow2 integration; never operates the real worker."""
import contextlib
from datetime import datetime, timezone
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
from unittest import mock

ROOT = Path('/opt/baarcha-bench/cube-backup-fixture-20260925')
spec = importlib.util.spec_from_file_location('cold_pair', ROOT / 'cold_pair.py')
cold = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cold)


def command(args, success=True):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if success and result.returncode != 0:
        raise RuntimeError('synthetic external command failed: ' + Path(args[0]).name)
    return result


def main():
    os.umask(0o077)
    assert Path(__file__).resolve().parent == ROOT
    assert ROOT.is_dir() and not ROOT.is_symlink() and ROOT.stat().st_mode & 0o777 == 0o700
    source = ROOT / 'synthetic-source'
    artifacts = ROOT / 'artifacts'
    keys = ROOT / 'fixture-private-keys'
    for directory in (source, artifacts, keys):
        directory.mkdir(mode=0o700)
    report = {'scope': 'synthetic-only; no real worker/unit/database/configuration used', 'complete': False, 'key_custody_scope': 'disposable fixture key on this test host; production off-host custody NOT proven'}
    try:
        root, data = source / 'root.qcow2', source / 'data.qcow2'
        for path in (root, data):
            command(['qemu-img', 'create', '-f', 'qcow2', str(path), '32M'])
            command(['qemu-io', '-f', 'qcow2', '-c', 'write -P 0x53 0 65536', str(path)])
        # Distinct later disk write must survive conversion and restore.
        command(['qemu-io', '-f', 'qcow2', '-c', 'write -P 0x71 16777216 65536', str(data)])
        db = source / 'controller.sqlite'
        with contextlib.closing(sqlite3.connect(db)) as connection:
            connection.executescript("CREATE TABLE task(status TEXT); CREATE TABLE cube_admission(state TEXT,charged INTEGER); CREATE TABLE runtime_binding(runtime_id TEXT,provider TEXT); INSERT INTO runtime_binding VALUES('synthetic-runtime','cube'); CREATE TABLE note(value TEXT); INSERT INTO note VALUES('committed-after-migration-fixture');")
        roles = {}
        for role in cold.ROLES:
            path = source / role
            path.write_bytes(('synthetic-' + role).encode())
            roles[role] = str(path)
        (source / 'pause-receipt').write_text(json.dumps({'version': 1, 'generated_at': datetime.now(timezone.utc).isoformat(), 'provider_jobs': 0, 'guest_states': {'synthetic-runtime': 'paused'}}))
        worker_lock = source / 'worker.lock'
        worker_lock.write_bytes(cold.WORKER_MARKER)
        Path(str(db) + '.maintenance.lock').write_bytes(cold.CONTROLLER_MARKER)
        cfg = {'root_disk': str(root), 'data_disk': str(data), 'database': str(db), 'worker_unit': 'baarcha-cube-synthetic.service', 'worker_lock': str(worker_lock), 'artifacts': roles}
        config = source / 'config.json'
        config.write_text(json.dumps(cfg))
        captured = artifacts / 'captured'
        # Only systemd worker identity is replaced: there is no synthetic VM unit.
        # Real file locks, PID FD scan, receipt/SQLite checks, conversion/comparison,
        # fsync, hashing, and no-replace publication execute unchanged.
        def synthetic_unit_only(candidate):
            assert candidate['worker_unit'] == 'baarcha-cube-synthetic.service'
            assert all(Path(candidate[key]).parent == source for key in ('root_disk', 'data_disk', 'database', 'worker_lock'))
        with mock.patch.object(cold, 'verify_unit', side_effect=synthetic_unit_only), contextlib.redirect_stdout(io.StringIO()):
            cold.capture(type('Args', (), {'config': config, 'output': captured})())
        report['capture_boundary'] = 'Only verify_unit replaced for a nonexistent synthetic unit; all other capture checks and commands real'
        base = ['gpg', '--no-options', '--homedir', str(keys), '--batch']
        command(base + ['--pinentry-mode', 'loopback', '--passphrase', '', '--quick-generate-key', 'Cube Backup Synthetic Fixture', 'rsa2048', 'encr', '1d'])
        listing = command(base + ['--with-colons', '--list-keys']).stdout.decode()
        fingerprint = next(line.split(':')[9] for line in listing.splitlines() if line.startswith('fpr:'))
        public = artifacts / 'fixture-public.asc'
        public.write_bytes(command(base + ['--armor', '--export', fingerprint]).stdout)
        private = artifacts / 'fixture-private.asc'
        private.write_bytes(command(base + ['--pinentry-mode', 'loopback', '--passphrase', '', '--armor', '--export-secret-keys', fingerprint]).stdout)
        ciphertext = artifacts / 'sealed.gpg'
        with contextlib.redirect_stdout(io.StringIO()):
            cold.seal(type('Args', (), {'capture': captured, 'recipient_key': public, 'fingerprint': fingerprint, 'output': ciphertext})())
        for name, key, expected in [('wrong-recipient', public, 'B' * 40), ('private-key-refused', private, fingerprint)]:
            try:
                cold.seal(type('Args', (), {'capture': captured, 'recipient_key': key, 'fingerprint': expected, 'output': artifacts / name})())
            except RuntimeError:
                pass
            else:
                raise RuntimeError('invalid recipient key accepted')
            assert not (artifacts / name).exists()
        decrypted = artifacts / 'verified.tar'
        good = command(base + ['--status-fd', '1', '--output', str(decrypted), '--decrypt', str(ciphertext)])
        assert b'DECRYPTION_OKAY' in good.stdout
        restored = artifacts / 'restored'
        with contextlib.redirect_stdout(io.StringIO()):
            cold.restore(type('Args', (), {'archive': decrypted, 'output': restored})())
        for name, original in [('root.qcow2', root), ('data.qcow2', data)]:
            command(['qemu-img', 'compare', '-f', 'qcow2', '-F', 'qcow2', str(original), str(restored / name)])
        command(['qemu-io', '-f', 'qcow2', '-c', 'read -P 0x71 16777216 65536', str(restored / 'data.qcow2')])
        with contextlib.closing(sqlite3.connect(restored / 'controller.sqlite')) as connection:
            assert connection.execute('SELECT value FROM note').fetchone()[0] == 'committed-after-migration-fixture'
        encrypted_bytes = bytearray(ciphertext.read_bytes())
        encrypted_bytes[-1] ^= 1
        damaged = artifacts / 'damaged.gpg'
        damaged.write_bytes(encrypted_bytes)
        bad_output = artifacts / 'UNTRUSTED-DO-NOT-RESTORE.tar'
        bad = command(base + ['--status-fd', '1', '--output', str(bad_output), '--decrypt', str(damaged)], success=False)
        report['tamper_exit_nonzero'] = bad.returncode != 0
        report['tamper_bad_mdc'] = b'BADMDC' in bad.stdout
        report['tamper_decryption_failed'] = b'DECRYPTION_FAILED' in bad.stdout
        report['tamper_status_codes'] = [line.split()[1].decode() for line in bad.stdout.splitlines() if line.startswith(b'[GNUPG:] ') and len(line.split()) > 1]
        report['tamper_aead_tag_failure'] = b'gcry_cipher_checktag' in bad.stderr and b'Checksum error' in bad.stderr
        report['tamper_may_emit_decryption_okay'] = b'DECRYPTION_OKAY' in bad.stdout
        assert bad.returncode != 0 and (b'BADMDC' in bad.stdout or b'DECRYPTION_FAILED' in bad.stdout or report['tamper_aead_tag_failure'])
        # Never pass plaintext from a failed decrypt to the restore tool.
        if bad_output.exists():
            bad_output.unlink()
        (restored / 'controller-key').write_bytes(b'tampered-fixture')
        try:
            cold.validate_capture(restored)
        except RuntimeError:
            pass
        else:
            raise RuntimeError('modified plaintext payload accepted')
        report.update({'complete': True, 'qcow2_disk_count': 2, 'virtual_bytes_each': 32 << 20, 'real_qcow_check_convert_compare': True, 'latest_disk_pattern_and_sqlite_row_restored': True, 'real_gpg_encrypt_decrypt': True, 'exact_recipient_and_private_key_refusal': True, 'ciphertext_tampering_rejected': True, 'plaintext_hash_tampering_rejected': True, 'ciphertext_sha256': cold.digest(ciphertext), 'ciphertext_bytes': ciphertext.stat().st_size, 'worker_boot_or_application_restore_tested': False, 'qemu_version': command(['qemu-img', '--version']).stdout.decode().splitlines()[0], 'gpg_version': command(['gpg', '--version']).stdout.decode().splitlines()[0]})
    finally:
        command(['gpgconf', '--homedir', str(keys), '--kill', 'all'], success=False)
        for directory in (source, artifacts, keys):
            assert directory.parent == ROOT and not directory.is_symlink()
            shutil.rmtree(directory)
        report['owned_fixture_data_and_keys_removed'] = True
        (ROOT / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report))


if __name__ == '__main__':
    try:
        main()
    except Exception:
        sys.excepthook = sys.__excepthook__
        raise
