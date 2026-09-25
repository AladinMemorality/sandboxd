#!/usr/bin/env python3
"""Owned warm frontend build. Preparation alone never authorizes execution."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import resource
import selectors
import socket
import shutil
import signal
import stat
import subprocess
import sys
import time

SOURCE = Path('/opt/templates/react-pro')
BASE = Path('/home/sandbox/workspace/app/.operator-frontend-build-20260925')
TASK = '01M3D5JY2K1WDBJW19N33ZHBP8'
PRIOR = {'01M3D27V0SHQ863WHPCAENGVTY', '01M3D30ENCD5BWQK4WN4DGV371', '01M3D3Z2T9CM6WSW9QB311KXB7', TASK}
PACKAGE_SHA = '2b1da52f6cedb7b064fa9d7c0286eec9e47b54b7f87c915676ae43fa42aaf6e5'
MAX_FILES, MAX_BYTES, MAX_LOG = 100000, 2 * 1024**3, 1024**2


def need(ok, message):
    if not ok:
        raise ValueError(message)


def digest(p):
    h = hashlib.sha256()
    with p.open('rb') as f:
        for block in iter(lambda: f.read(65536), b''):
            h.update(block)
    return h.hexdigest()


def json_file(p, limit=1024**2):
    need(p.resolve() == p, 'noncanonical evidence path')
    fd = os.open(p, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as f:
        st = os.fstat(f.fileno())
        need(stat.S_ISREG(st.st_mode) and st.st_size <= limit, 'invalid evidence file')
        raw = f.read(limit + 1)
        need(len(raw) <= limit, 'evidence too large')
        return json.loads(raw)


def private_write(p, value):
    tmp = p.with_name(p.name + '.tmp')
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f:
        json.dump(value, f, indent=2)
        f.write('\n')
        f.flush()
        os.fsync(f.fileno())
    os.replace(tmp, p)
    fd = os.open(p.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def terminal_guard(root=Path('/home/sandbox/.runtimed/tasks')):
    need(root.resolve() == root, 'noncanonical task root')
    ids = {p.name for p in root.iterdir() if p.is_dir()}
    need(ids == PRIOR, 'new or missing task; refuse build overlap')
    for task in ids:
        row = json_file(root / task / 'result.json')
        need(row.get('id') == task and row.get('status') in ('succeeded', 'failed', 'cancelled'), 'task is not terminal')
    return json_file(root / TASK / 'result.json')


def inventory(source, max_files=MAX_FILES, max_bytes=MAX_BYTES, deadline=None):
    need(source.resolve() == source and source.is_dir(), 'source not canonical directory')
    records, total = [], 0
    for current, dirs, files in os.walk(source, followlinks=False):
        for name in sorted(dirs + files):
            need(deadline is None or time.monotonic() < deadline, 'inventory deadline')
            p = Path(current) / name
            rel = p.relative_to(source)
            need(len(rel.parts) <= 64 and len(records) < max_files, 'source count/depth limit')
            st = p.lstat()
            if stat.S_ISLNK(st.st_mode):
                resolved = p.resolve(strict=True)
                need(resolved == source or source in resolved.parents, 'source link escapes template')
                record = [str(rel), 'link', os.readlink(p)]
            elif stat.S_ISDIR(st.st_mode):
                record = [str(rel), 'dir', stat.S_IMODE(st.st_mode)]
            else:
                need(stat.S_ISREG(st.st_mode), 'unsupported source inode')
                need(not st.st_mode & (stat.S_ISUID | stat.S_ISGID), 'special source permissions')
                total += st.st_size
                need(total <= max_bytes, 'source byte limit')
                record = [str(rel), 'file', st.st_size, stat.S_IMODE(st.st_mode), st.st_mtime_ns]
            records.append(record)
    return records, total


def copy_source(source, destination, deadline):
    records, total = inventory(source, deadline=deadline)
    manifest = hashlib.sha256(json.dumps(records, separators=(',', ':')).encode()).hexdigest()
    destination.mkdir(mode=0o700)
    for record in sorted(records, key=lambda r: (len(Path(r[0]).parts), r[0])):
        need(time.monotonic() < deadline, 'copy deadline')
        src, dst = source / record[0], destination / record[0]
        if record[1] == 'dir':
            dst.mkdir(mode=record[2])
        elif record[1] == 'link':
            # Preserve relative internal links; rewrite an absolute internal
            # template link to its corresponding copied target, never source.
            target = record[2]
            if os.path.isabs(target):
                target = os.path.relpath(destination / src.resolve().relative_to(source), dst.parent)
            dst.symlink_to(target)
        else:
            fd = os.open(src, os.O_RDONLY | os.O_NOFOLLOW)
            with os.fdopen(fd, 'rb') as inp:
                before = os.fstat(inp.fileno())
                need(stat.S_ISREG(before.st_mode) and before.st_size == record[2] and before.st_mtime_ns == record[4], 'source changed before copy')
                outfd = os.open(dst, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, record[3])
                with os.fdopen(outfd, 'wb') as out:
                    remaining = record[2]
                    while remaining:
                        need(time.monotonic() < deadline, 'copy deadline')
                        block = inp.read(min(65536, remaining))
                        need(bool(block), 'source truncated')
                        out.write(block)
                        remaining -= len(block)
                    need(not inp.read(1), 'source grew')
                after = os.fstat(inp.fileno())
                need(before.st_size == after.st_size and before.st_mtime_ns == after.st_mtime_ns, 'source changed during copy')
    # Every copied symlink must now resolve entirely inside this fresh copy.
    inventory(destination, deadline=deadline)
    return {'entries': len(records), 'logical_bytes': total, 'metadata_manifest_sha256': manifest}


def sterile_env(root):
    return {'PATH': '/usr/local/bin:/usr/bin:/bin', 'HOME': str(root / 'home'), 'LANG': 'C.UTF-8', 'CI': '1',
            'npm_config_offline': 'true', 'npm_config_audit': 'false', 'npm_config_fund': 'false',
            'npm_config_update_notifier': 'false', 'npm_config_cache': str(root / 'npm-cache')}


def stop_group(proc):
    # Kill the entire group even when its original parent already exited.
    # A child retaining stdout must not keep this bounded fixture alive.
    for sig in (signal.SIGTERM, signal.SIGKILL):
        try:
            os.killpg(proc.pid, sig)
        except ProcessLookupError:
            break
        if sig == signal.SIGTERM:
            time.sleep(0.15)
    try:
        proc.wait(timeout=3)
    except subprocess.TimeoutExpired:
        raise RuntimeError('owned child did not exit after SIGKILL')


def build(cwd, log_path, seconds=180, command=None):
    began, cpu_before = time.monotonic(), resource.getrusage(resource.RUSAGE_CHILDREN)
    fd = os.open(log_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    proc = None
    selected = selectors.DefaultSelector()
    timeout, size = False, 0
    try:
        with os.fdopen(fd, 'wb') as out:
            proc = subprocess.Popen(command or ['npm', 'run', 'build', '--offline'], cwd=cwd,
                                    env=sterile_env(cwd.parent), stdin=subprocess.DEVNULL,
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
            selected.register(proc.stdout, selectors.EVENT_READ)
            while selected.get_map() or proc.poll() is None:
                if time.monotonic() - began > seconds:
                    timeout = True
                    break
                if not selected.get_map():
                    time.sleep(0.025)
                for key, _ in selected.select(0.05) if selected.get_map() else []:
                    chunk = os.read(key.fileobj.fileno(), 65536)
                    if not chunk:
                        selected.unregister(key.fileobj)
                    else:
                        out.write(chunk[:max(0, MAX_LOG - size)])
                        size += len(chunk)
            stop_group(proc)
            out.flush()
            os.fsync(out.fileno())
    finally:
        selected.close()
        if proc is not None:
            stop_group(proc)
            proc.stdout.close()
    usage = resource.getrusage(resource.RUSAGE_CHILDREN)
    return {'elapsed_ms': round((time.monotonic() - began) * 1000), 'exit_code': proc.returncode, 'timed_out': timeout,
            'max_child_rss_kib': usage.ru_maxrss, 'user_cpu_s': usage.ru_utime - cpu_before.ru_utime,
            'system_cpu_s': usage.ru_stime - cpu_before.ru_stime, 'log_bytes_observed': size, 'log_truncated': size > MAX_LOG,
            'rss_scope': 'Linux RUSAGE_CHILDREN cumulative high-water RSS; not simultaneous process-tree or total guest RAM'}


def prepare_workload(work, run):
    # Synthetic implementation, never the existing notes app or its database.
    root = work / 'src' / 'profile'
    root.mkdir(mode=0o700)
    for index in range(48):
        (root / ('feature%d.ts' % index)).write_text(
            'export const feature%d = (n: number): number => (n * %d + %d) %% 997;\n' % (index, index + 1, index))
    imports = ''.join("import { feature%d } from './profile/feature%d';\n" % (i, i) for i in range(48))
    calls = ','.join('feature%d(n)' % i for i in range(48))
    (work / 'src/profile-marker.ts').write_text('export const marker = "%s-before";\n' % run)
    (work / 'src/App.tsx').write_text(imports + """
import { useEffect, useMemo, useState } from 'react';
import { z } from 'zod';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card } from '@/components/ui/card';
import { marker } from './profile-marker';
const Item = z.object({ id: z.number(), label: z.string(), category: z.number() });
export default function App() {
  const [query, setQuery] = useState(''); const [items, setItems] = useState<z.infer<typeof Item>[]>([]);
  const [error, setError] = useState(''); const [category, setCategory] = useState(-1);
  useEffect(() => { const controller = new AbortController();
    fetch('/api/items', {signal: controller.signal}).then(r => r.json()).then(v => setItems(z.array(Item).parse(v.items)))
      .catch(e => { if (!controller.signal.aborted) setError(String(e)); }); return () => controller.abort(); }, []);
  const rows = useMemo(() => items.filter(x => x.label.includes(query) && (category < 0 || x.category === category)), [items, query, category]);
  return <main className="p-8"><h1>Owned inventory profile</h1><p>{marker}</p>
    <Input aria-label="Search inventory" value={query} onChange={e => setQuery(e.target.value)} />
    <Button onClick={() => setCategory(category < 0 ? 2 : -1)}>Toggle category</Button>
    <p role="status">{error || `${rows.length} results`}</p><section className="grid gap-4 md:grid-cols-3">
      {rows.slice(0, 48).map(item => {const n = item.id; const score = [""" + calls + """].reduce((a, b) => a + b, 0);
        return <Card key={item.id} className="p-4"><h2>{item.label}</h2><p>Category {item.category}; score {score}</p></Card>})}
    </section></main>;
}
""")
    (work / 'vite.config.ts').write_text("""import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react'; import tailwindcss from '@tailwindcss/vite'; import path from 'node:path';
export default defineConfig({plugins:[react(),tailwindcss()],resolve:{alias:{'@':path.resolve(__dirname,'./src')}},
server:{host:'127.0.0.1',port:3012,strictPort:true,proxy:{'/api':'http://127.0.0.1:3013'}}});
""")
    # Only clear artifacts in our verified fresh copy, never the trusted source.
    if (work / 'dist').exists():
        need((work / 'dist').resolve() == work / 'dist', 'copied dist is a link')
        shutil.rmtree(work / 'dist')
    if os.path.lexists(work / 'tsconfig.tsbuildinfo'):
        need((work / 'tsconfig.tsbuildinfo').is_file() and not (work / 'tsconfig.tsbuildinfo').is_symlink(), 'unexpected build cache inode')
        (work / 'tsconfig.tsbuildinfo').unlink()


def require_uid():
    need(sys.platform == 'linux' and os.getuid() == 1000 and os.geteuid() == 1000,
         'must be launched by unprivileged runtimed supervisor, never operator root exec')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--run', required=True)
    a = p.parse_args()
    require_uid()
    need(len(a.run) == 16 and all(c in '0123456789abcdef' for c in a.run), 'fixed operator run nonce required')
    terminal = terminal_guard()
    stage = BASE / a.run
    target = stage / 'output'
    need(stage.resolve() == stage and stage.is_dir() and stage.stat().st_uid == 1000, 'owned canonical stage required')
    need(not os.path.lexists(target), 'one-shot result already exists; no automatic rerun')
    probe = stage / 'input/probe.mjs'
    need(probe.resolve() == probe and probe.is_file() and probe.stat().st_size <= 32768, 'fixed probe missing')
    need(digest(SOURCE / 'package.json') == PACKAGE_SHA, 'reviewed template package differs')
    package = json_file(SOURCE / 'package.json')
    need(package['scripts'] == {'dev': 'vite', 'build': 'tsc -b && vite build', 'preview': 'vite preview'}, 'unexpected package lifecycle commands')
    need((SOURCE / 'node_modules/.bin/vite').exists() and (SOURCE / 'node_modules/.bin/tsc').exists(), 'warm dependencies absent')
    # Refuse to borrow or stop any pre-existing listener.
    for port in (3012, 3013):
        with socket.socket() as check:
            check.bind(('127.0.0.1', port))
    target.mkdir(mode=0o700)
    (target / 'home').mkdir(mode=0o700)
    report = {'version': 2, 'run': a.run, 'task_terminal_status': terminal['status'], 'package_sha256': PACKAGE_SHA,
              'phases': [], 'successful': False, 'production_speed_claim': False,
              'dependency_mode': 'prepared cache; no install command; npm offline configured',
              'cold_scope': 'new frontend process after copy; no VM or page-cache-cold claim',
              'notes_and_pg_supervisor_restart': True, 'react_render_verified': False}
    processes = []
    def phase(name):
        report['phases'].append({'name': name, 'wall_time_ns': time.time_ns(), 'monotonic_ns': time.monotonic_ns()})
        private_write(target / 'report.json', report)
    def start(command, name, cwd):
        # All commands are fixed; log output is discarded for long-lived dev
        # services so a runaway stdout cannot fill the guest disk.
        child = subprocess.Popen(command, cwd=cwd, env=sterile_env(target), stdin=subprocess.DEVNULL,
                                 stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
        processes.append(child)
        return child
    def probe_phase(mode, seconds=45):
        result = build(target / 'work', target / (mode + '.log'), seconds,
                       ['node', str(probe), mode, str(target / (mode + '.json')), a.run])
        need(result['exit_code'] == 0 and not result['timed_out'], 'probe failed: ' + mode)
        report[mode] = json_file(target / (mode + '.json'))
    def interrupted(signum, frame):
        raise InterruptedError('bounded fixture interrupted')
    for signum in (signal.SIGTERM, signal.SIGINT, signal.SIGALRM):
        signal.signal(signum, interrupted)
    signal.alarm(480)
    try:
        phase('copy_started')
        report['copy'] = copy_source(SOURCE, target / 'work', time.monotonic() + 120)
        prepare_workload(target / 'work', a.run)
        phase('copy_finished')
        phase('frontend_process_start')
        start(['node', str(probe), 'serve', '', a.run], 'api', target / 'work')
        start(['npm', 'run', 'dev', '--', '--host', '127.0.0.1', '--port', '3012', '--strictPort'], 'vite', target / 'work')
        probe_phase('ready')
        phase('frontend_modules_and_api_ready')
        probe_phase('warm', 15)
        phase('warm_api_finished')
        phase('build_started')
        load = start(['node', str(probe), 'load', str(target / 'load.json'), a.run], 'load', target / 'work')
        report['build'] = build(target / 'work', target / 'build.log')
        phase('build_finished')
        need(report['build']['exit_code'] == 0 and not report['build']['timed_out'], 'build failed')
        load.wait(timeout=30)
        need(load.returncode == 0, 'concurrent API probe failed')
        report['load'] = json_file(target / 'load.json')
        phase('hmr_started')
        probe_phase('hmr', 15)
        phase('hmr_finished')
        output = target / 'work/dist/index.html'
        need(output.is_file() and output.stat().st_size > 0, 'build output missing')
        report['dist_index_sha256'] = digest(output)
        need(terminal_guard()['status'] == terminal['status'], 'task history changed')
        report['successful'] = True
        phase('finished')
    except Exception as error:
        report['error_type'] = type(error).__name__
        phase('failed')
        raise
    finally:
        signal.alarm(0)
        for child in reversed(processes):
            stop_group(child)
        phase('owned_children_stopped')
    # Supervisor may restart this command after it exits. The existing output
    # directory makes subsequent attempts refuse BEFORE any copy/build/probe.


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('Profile refused or failed; retain owned evidence; no automatic cleanup.', file=sys.stderr)
        sys.exit(1)
