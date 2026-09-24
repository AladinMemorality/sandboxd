#!/usr/bin/env python3
"""Inert cold-pair backup tooling. Never stops/starts a service or installs a unit."""
import argparse
import contextlib
import ctypes
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import stat
import subprocess
import sys
import tarfile
import tempfile
from datetime import datetime, timezone

WORKER_MARKER = b"baarcha-cube-backup-lock-v1\n"
CONTROLLER_MARKER = b"sandboxd-offline-maintenance-v1\n"
ROLES = {"controller-key", "controller-config", "worker-config", "worker-launch", "platform-db", "library", "rollback", "pause-receipt"}
MAX_BUNDLE = 1 << 40


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def real_file(path):
    path = Path(path).absolute()
    for parent in (path, *path.parents):
        require(not parent.is_symlink(), "symlink input or ancestor refused")
    info = path.stat()
    require(stat.S_ISREG(info.st_mode), "regular file required")
    return path


def private_directory(path):
    path = Path(path).absolute()
    require(path.resolve(strict=True) == path and path.is_dir(), "real directory required")
    info = path.stat()
    require(info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == 0o700, "directory must be owner-only0700")
    return path


def native_host():
    require(sys.platform == "linux" and os.geteuid() == 0, "capture/worker-exec requires native Linux host root")
    require(not Path("/.dockerenv").exists() and Path("/proc/1/comm").read_text().strip() == "systemd", "native systemd host required")


def run(args):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    require(result.returncode == 0, "external verification failed: " + Path(args[0]).name)
    return result.stdout


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


@contextlib.contextmanager
def lock_file(path, marker, exclusive, create=False):
    path = Path(path).absolute()
    require(path.parent.resolve(strict=True) == path.parent, "lock ancestor must be real")
    flags = os.O_RDWR | os.O_NOFOLLOW | (os.O_CREAT if create else 0)
    fd = os.open(path, flags, 0o600)
    try:
        info = os.fstat(fd)
        require(stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == 0o600, "unsafe startup lock")
        fcntl.flock(fd, (fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH) | fcntl.LOCK_NB)
        data = os.read(fd, 128)
        if create and data == b"":
            os.write(fd, marker)
            os.fsync(fd)
        else:
            require(data == marker, "lock protocol marker missing or wrong")
        yield fd
    finally:
        os.close(fd)


def worker_exec(args):
    native_host()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    require(command and command[0] == "/usr/bin/qemu-system-x86_64", "only reviewed QEMU executable permitted")
    with lock_file(args.lock_file, WORKER_MARKER, False, True) as fd:
        os.set_inheritable(fd, True)
        os.execv(command[0], command)


def no_open_users(paths):
    targets = {(p.stat().st_dev, p.stat().st_ino) for p in paths if p.exists()}
    for process in Path("/proc").iterdir():
        if not process.name.isdigit() or int(process.name) == os.getpid():
            continue
        try:
            for entry in (process / "fd").iterdir():
                try:
                    info = entry.stat()
                except (FileNotFoundError, ProcessLookupError):
                    continue
                require((info.st_dev, info.st_ino) not in targets, "source still open by another process")
        except (FileNotFoundError, ProcessLookupError):
            continue


def verify_unit(config):
    unit = config["worker_unit"]
    require(re.fullmatch(r"baarcha-cube-[a-zA-Z0-9-]+\.service", unit), "unexpected worker unit")
    state = run(["systemctl", "show", unit, "--property=ActiveState,MainPID,ExecStart"]).decode()
    values = dict(line.split("=", 1) for line in state.splitlines() if "=" in line)
    require(values.get("ActiveState") == "inactive" and values.get("MainPID") == "0", "worker must already be stopped")
    command = values.get("ExecStart", "")
    require(str(Path(__file__).resolve()) in command and "worker-exec" in command and "--lock-file " + config["worker_lock"] in command, "worker unit does not use this lifetime startup fence")


def qcow_info(path):
    info = json.loads(run(["qemu-img", "info", "--output=json", str(path)]))
    require(info.get("format") == "qcow2" and not info.get("backing-filename") and not info.get("data-file") and not info.get("format-specific", {}).get("data", {}).get("data-file"), "standalone qcow2 disk required")
    size = info.get("virtual-size", 0)
    require(isinstance(size, int) and 0 < size <= MAX_BUNDLE, "invalid disk size")
    run(["qemu-img", "check", "-f", "qcow2", str(path)])
    return size


def publish_directory(source, destination):
    # Use the OS no-replace operation; an existence check followed by rename
    # would overwrite another operator's newly created empty destination.
    libc = ctypes.CDLL(None, use_errno=True)
    if sys.platform == "linux":
        operation = libc.renameat2
        operation.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
        result = operation(-100, os.fsencode(source), -100, os.fsencode(destination), 1)
    elif sys.platform == "darwin":
        operation = libc.renamex_np
        operation.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_uint]
        result = operation(os.fsencode(source), os.fsencode(destination), 4)
    else:
        raise RuntimeError("atomic no-replace directory publication unsupported")
    if result != 0:
        raise OSError(ctypes.get_errno(), "atomic no-replace publication failed")


def write_manifest(directory):
    files = {}
    for path in sorted(directory.iterdir()):
        require(path.name != "manifest.json", "manifest already exists")
        real_file(path)
        files[path.name] = {"bytes": path.stat().st_size, "sha256": digest(path)}
    manifest = {"version": 1, "kind": "cold-cube-pair", "files": files, "application_restore_verified": False}
    (directory / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    os.chmod(directory / "manifest.json", 0o600)
    for path in directory.iterdir():
        with path.open("rb") as f:
            os.fsync(f.fileno())
    fd = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def validate_capture(directory, check_disks=True):
    directory = private_directory(directory)
    manifest_path = real_file(directory / "manifest.json")
    require(manifest_path.stat().st_size <= 1 << 20, "oversized manifest")
    manifest = json.loads(manifest_path.read_text())
    expected = {"root.qcow2", "data.qcow2", "controller.sqlite", *ROLES}
    require(manifest.get("version") == 1 and manifest.get("kind") == "cold-cube-pair" and set(manifest.get("files", {})) == expected, "incomplete backup set")
    require({p.name for p in directory.iterdir()} == expected | {"manifest.json"}, "unexpected backup entry")
    for name, item in manifest["files"].items():
        path = real_file(directory / name)
        require(path.stat().st_size == item["bytes"] and digest(path) == item["sha256"], "backup integrity mismatch")
    with contextlib.closing(sqlite3.connect((directory / "controller.sqlite").as_uri() + "?mode=ro", uri=True)) as db:
        require(db.execute("PRAGMA integrity_check").fetchall() == [("ok",)], "controller SQLite integrity failed")
    if check_disks:
        qcow_info(directory / "root.qcow2")
        qcow_info(directory / "data.qcow2")
    return manifest


def validate_pause_receipt(database, receipt_path):
    require(receipt_path.stat().st_size <= 1 << 20, "oversized pause receipt")
    receipt = json.loads(receipt_path.read_text())
    require(receipt.get("version") == 1 and receipt.get("provider_jobs") == 0, "reviewed no-job pause receipt required")
    generated = datetime.fromisoformat(receipt["generated_at"])
    require(generated.tzinfo is not None and 0 <= (datetime.now(timezone.utc) - generated).total_seconds() <= 600, "pause receipt must be freshly observed before shutdown")
    with contextlib.closing(sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)) as db:
        require(db.execute("SELECT COUNT(*) FROM task WHERE status='running'").fetchone()[0] == 0, "active coding task prevents backup")
        require(db.execute("SELECT COUNT(*) FROM cube_admission WHERE state='pending' OR charged<>0").fetchone()[0] == 0, "admission still active or ambiguous")
        ids = {row[0] for row in db.execute("SELECT runtime_id FROM runtime_binding WHERE provider='cube'")}
    require(receipt.get("guest_states") == {i: "paused" for i in ids}, "pause inventory differs from exact persisted Cube bindings")


def capture(args):
    native_host()
    config_path = real_file(args.config)
    require(config_path.stat().st_size <= 1 << 20, "oversized capture configuration")
    config = json.loads(config_path.read_text())
    require(set(config) == {"root_disk", "data_disk", "database", "worker_unit", "worker_lock", "artifacts"}, "unknown/incomplete capture configuration")
    require(set(config["artifacts"]) == ROLES, "all independent recovery artifacts required")
    sources = {"root.qcow2": real_file(config["root_disk"]), "data.qcow2": real_file(config["data_disk"]), "controller.sqlite": real_file(config["database"])}
    sources.update({name: real_file(path) for name, path in config["artifacts"].items()})
    output = Path(args.output).absolute()
    private_directory(output.parent)
    require(not output.exists(), "output must be new")
    with lock_file(config["worker_lock"], WORKER_MARKER, True), lock_file(str(sources["controller.sqlite"]) + ".maintenance.lock", CONTROLLER_MARKER, True):
        verify_unit(config)
        checked = list(sources.values()) + [Path(str(sources["controller.sqlite"]) + suffix) for suffix in ("-wal", "-shm")]
        no_open_users(checked)
        validate_pause_receipt(sources["controller.sqlite"], sources["pause-receipt"])
        virtual = sum(qcow_info(sources[name]) for name in ("root.qcow2", "data.qcow2"))
        required = virtual + sum(p.stat().st_size for name, p in sources.items() if not name.endswith(".qcow2")) + (64 << 20)
        require(shutil.disk_usage(output.parent).free >= required, "insufficient conservative capture space")
        stage = Path(tempfile.mkdtemp(prefix=".incomplete-cold-pair-", dir=output.parent))
        # Failure retains private incomplete evidence; source disks are never modified.
        for name in ("root.qcow2", "data.qcow2"):
            run(["qemu-img", "convert", "-f", "qcow2", "-O", "qcow2", str(sources[name]), str(stage / name)])
            os.chmod(stage / name, 0o600)
            run(["qemu-img", "compare", "-f", "qcow2", "-F", "qcow2", str(sources[name]), str(stage / name)])
        with contextlib.closing(sqlite3.connect(sources["controller.sqlite"].as_uri() + "?mode=ro", uri=True)) as source, contextlib.closing(sqlite3.connect(stage / "controller.sqlite")) as destination:
            source.backup(destination)
        os.chmod(stage / "controller.sqlite", 0o600)
        for name in ROLES:
            shutil.copyfile(sources[name], stage / name)
            os.chmod(stage / name, 0o600)
            require(digest(sources[name]) == digest(stage / name), "artifact changed while copying")
        verify_unit(config)
        no_open_users(checked)
        write_manifest(stage)
        validate_capture(stage)
        publish_directory(stage, output)
        fd = os.open(output.parent, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    print(json.dumps({"captured": True, "application_restore_verified": False}))


def public_key_fingerprint(colons, expected):
    require(re.fullmatch(r"[0-9A-F]{40}|[0-9A-F]{64}", expected), "exact uppercase fingerprint required")
    rows = [line.split(":") for line in colons.splitlines()]
    require(not any(row[0] in ("sec", "ssb") for row in rows), "private key material forbidden on backup host")
    require(sum(row[0] == "pub" for row in rows) == 1, "exactly one public primary key required")
    fingerprints = [row[9] for row in rows if row[0] == "fpr" and len(row) > 9]
    require(fingerprints and fingerprints[0] == expected, "recipient fingerprint mismatch")


def seal(args):
    source = private_directory(args.capture)
    validate_capture(source)
    key = real_file(args.recipient_key)
    output = Path(args.output).absolute()
    private_directory(output.parent)
    require(not output.exists(), "encrypted destination exists")
    with tempfile.TemporaryDirectory(prefix=".public-key-", dir=output.parent) as keyring:
        base = ["gpg", "--no-options", "--homedir", keyring, "--batch"]
        public_key_fingerprint(run(base + ["--with-colons", "--show-keys", str(key)]).decode(), args.fingerprint)
        run(base + ["--import", str(key)])
        require(not any(line.startswith((b"sec:", b"ssb:")) for line in run(base + ["--with-colons", "--list-secret-keys"]).splitlines()), "backup keyring must contain no private keys")
        fd, temp = tempfile.mkstemp(prefix=".incomplete-encrypted-", dir=output.parent)
        try:
            with os.fdopen(fd, "wb") as encrypted, tempfile.TemporaryFile() as errors:
                process = subprocess.Popen(base + ["--trust-model", "always", "--recipient", args.fingerprint, "--encrypt"], stdin=subprocess.PIPE, stdout=encrypted, stderr=errors)
                try:
                    with tarfile.open(fileobj=process.stdin, mode="w|", format=tarfile.GNU_FORMAT) as archive:
                        for path in sorted(source.iterdir()):
                            archive.add(path, arcname=path.name, recursive=False)
                    process.stdin.close()
                    require(process.wait() == 0, "GPG encryption failed")
                finally:
                    if process.poll() is None:
                        process.kill()
                        process.wait()
                encrypted.flush()
                os.fsync(encrypted.fileno())
            os.link(temp, output)
            directory_fd = os.open(output.parent, os.O_RDONLY)
            try:
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
        finally:
            os.unlink(temp)
    print(json.dumps({"encrypted": True, "sha256": digest(output), "bytes": output.stat().st_size, "restore_verified": False}))


def verify_tar_headers(path, expected):
    # Our writer emits only GNU ordinary-file headers (large sizes use base256),
    # never PAX/longname/sparse extensions. Reject extensions before tarfile can
    # allocate an attacker-sized metadata record. Payloads are skipped by seek.
    seen = set()
    total = 0
    with path.open("rb") as source:
        while True:
            header = source.read(512)
            require(len(header) == 512, "truncated tar header")
            if header == bytes(512):
                padding = source.read(10241)
                require(512 <= len(padding) <= 10240 and not any(padding), "invalid tar termination")
                break
            item = tarfile.TarInfo.frombuf(header, "utf-8", "strict")
            require(item.type in (tarfile.REGTYPE, tarfile.AREGTYPE) and item.name in expected and item.name not in seen, "unexpected tar type/path/duplicate")
            require(0 <= item.size <= MAX_BUNDLE, "tar member exceeds bound")
            seen.add(item.name)
            total += item.size
            require(total <= MAX_BUNDLE, "tar payload exceeds bound")
            source.seek(((item.size + 511) // 512) * 512, 1)
    require(seen == expected, "incomplete tar recovery set")


def restore(args):
    # Decrypt with GPG on an independent machine first. This tool never loads a
    # private key and never boots QEMU or overwrites any existing source/runtime.
    archive_path = real_file(args.archive)
    output = Path(args.output).absolute()
    private_directory(output.parent)
    require(not output.exists(), "restore destination must be new")
    expected = {"manifest.json", "root.qcow2", "data.qcow2", "controller.sqlite", *ROLES}
    verify_tar_headers(archive_path, expected)
    with tarfile.open(archive_path, "r:") as archive:
        members = archive.getmembers()
        require(len(members) == len(expected) and {m.name for m in members} == expected, "archive entries differ from exact recovery set")
        require(all(m.isfile() and not m.issym() and not m.islnk() and 0 <= m.size <= MAX_BUNDLE for m in members), "unsafe archive member")
        total = sum(m.size for m in members)
        require(total <= MAX_BUNDLE and shutil.disk_usage(output.parent).free >= total + (64 << 20), "restore space/bounds exceeded")
        stage = Path(tempfile.mkdtemp(prefix=".incomplete-restore-", dir=output.parent))
        for member in members:
            with archive.extractfile(member) as source, (stage / member.name).open("xb") as target:
                os.chmod(target.name, 0o600)
                shutil.copyfileobj(source, target, 1024 * 1024)
                target.flush()
                os.fsync(target.fileno())
    validate_capture(stage)
    publish_directory(stage, output)
    directory_fd = os.open(output.parent, os.O_RDONLY)
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)
    print(json.dumps({"integrity_verified": True, "application_restore_verified": False, "booted": False}))


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    p = sub.add_parser("worker-exec")
    p.add_argument("--lock-file", required=True)
    p.add_argument("command", nargs=argparse.REMAINDER)
    p = sub.add_parser("capture")
    p.add_argument("--config", required=True)
    p.add_argument("--output", required=True)
    p = sub.add_parser("seal")
    p.add_argument("--capture", required=True)
    p.add_argument("--recipient-key", required=True)
    p.add_argument("--fingerprint", required=True)
    p.add_argument("--output", required=True)
    p = sub.add_parser("restore")
    p.add_argument("--archive", required=True)
    p.add_argument("--output", required=True)
    args = parser.parse_args()
    {"worker-exec": worker_exec, "capture": capture, "seal": seal, "restore": restore}[args.action](args)


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, ValueError, KeyError, TypeError, sqlite3.Error, tarfile.TarError) as error:
        print("cold-pair failed: " + str(error), file=sys.stderr)
        sys.exit(1)
