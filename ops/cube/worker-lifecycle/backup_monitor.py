"""Observation-only backup freshness. Receipts are trusted operator attestations,
not remote-storage or application-restore probes. Never decrypts or messages."""
import hashlib
import json
from pathlib import Path
import re
import stat
import time


def check(policy,private_json,digest,now=None):
    now=time.time() if now is None else now
    def need(value):
        if not value:raise RuntimeError('backup evidence missing, stale or inconsistent')
    need(policy.get('version')==1)
    age=policy.get('max_backup_age_seconds');restore_age=policy.get('max_restore_age_seconds')
    need(isinstance(age,int) and 0<age<=7*86400 and isinstance(restore_age,int) and 0<restore_age<=90*86400)
    fingerprint=policy.get('recipient_fingerprint','');need(re.fullmatch(r'[0-9A-F]{40}|[0-9A-F]{64}',fingerprint))
    manifest_path=policy['encrypted_manifest'];manifest=private_json(manifest_path)
    need(manifest.get('version')==1 and manifest.get('kind')=='encrypted-cold-cube-pair' and manifest.get('recipient_fingerprint')==fingerprint)
    need(re.fullmatch(r'[a-f0-9]{64}',manifest.get('ciphertext_sha256','')) and re.fullmatch(r'[a-f0-9]{64}',manifest.get('capture_manifest_sha256','')))
    captured=manifest.get('captured_at');sealed=manifest.get('sealed_at')
    need(type(captured) in (int,float) and type(sealed) in (int,float) and 0<captured<=sealed<=now and now-captured<=age)
    archive=Path(manifest['ciphertext_path']);need(archive.is_absolute() and archive.resolve(strict=True)==archive)
    info=archive.stat();need(stat.S_ISREG(info.st_mode) and info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o600)
    # Seal hashes the complete archive once. Monitor binds that receipt to the
    # same inode/size/mtime/ctime; full integrity verification occurs off-host.
    need(manifest.get('file_identity')=={'device':info.st_dev,'inode':info.st_ino,'bytes':info.st_size,'mtime_ns':info.st_mtime_ns,'ctime_ns':info.st_ctime_ns})
    copy=private_json(policy['offhost_receipt']);restore=private_json(policy['restore_receipt']);manifest_hash=digest(manifest_path)
    for receipt in [copy,restore]:
        need(receipt.get('version')==1 and receipt.get('manifest_sha256')==manifest_hash and receipt.get('ciphertext_sha256')==manifest['ciphertext_sha256'])
        need(re.fullmatch(r'[a-f0-9]{64}',receipt.get('evidence_sha256','')))
        need(isinstance(receipt.get('verified_at'),(int,float)) and sealed<=receipt['verified_at']<=now)
    need(copy.get('kind')=='offhost-copy' and copy.get('verified_bytes')==info.st_size and copy.get('offhost') is True and copy.get('destination_id'))
    need(restore.get('kind')=='application-restore' and now-restore['verified_at']<=restore_age)
    need(all(restore.get(key) is True for key in ['decryption_verified','disk_integrity_verified','bindings_verified','application_data_verified','task_history_verified','database_data_verified']))
    # Evidence files must exist and hash to the externally recorded observations;
    # no flag can stand in for absent evidence. Contents remain private.
    need(digest(policy['offhost_evidence'])==copy['evidence_sha256'] and digest(policy['restore_evidence'])==restore['evidence_sha256'])
    return {'backup_age_seconds':now-captured,'restore_age_seconds':now-restore['verified_at'],'offhost_receipt_verified':True,'restore_receipt_verified':True,'evidence_trust':'operator-recorded; no remote probe','healthy':True}
