// Disposable React Pro fixture only. Never run against an owner's workspace.
import fs from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { spawn } from 'node:child_process';
import { createServer as createHTTPServer } from 'node:http';
import { createRequire } from 'node:module';
import path from 'node:path';

const require = createRequire(import.meta.url);
const root = process.cwd();
const hash = (data) => createHash('sha256').update(data).digest('hex');
const originalSHA = 'bfa94186daff535fefdf286088c1588fad6b02c01ef11cd3b42a6cb3e4c90767';
const patchedSHA = '88f35a22eb6ae9d58e54b94c1544f6c1cc90e3e2eac42542c5ea834179ef24f7';
const before = '      optimizationResult?.cancel()\n    ]);\n  }\n  if (!cachedMetadata) {';
const after = '      optimizationResult?.cancel()\n    ]);\n    depOptimizationProcessing.resolve();\n    resolveEnqueuedProcessingPromises();\n  }\n  if (!cachedMetadata) {';
const marker = 'DISPOSABLE_COLD_RELOAD_ONLY';
const installedPatch = process.env.RELOAD_USE_INSTALLED_PATCH === '1';

if (process.argv[2] === 'child') {
  // Start in a fresh process: module caching must not hide the patch or race.
  const { createServer } = await import('vite');
  const server = await createServer({
    root,
    cacheDir: `.vite-reload-${randomUUID()}`,
    // Delay only the synthetic optimizer, never production application code.
    optimizeDeps: { esbuildOptions: { plugins: [{
      name: 'disposable-slow-optimizer',
      setup(build) { build.onEnd(() => new Promise(resolve => setTimeout(resolve, 2000))); },
    }] } },
    server: { host: '127.0.0.1', port: 3198, strictPort: true },
  });
  await server.listen();
  const get = async (pathname, needle = '') => {
    const response = await fetch(`http://127.0.0.1:3198${pathname}`, { signal: AbortSignal.timeout(5000) });
    return response.status === 200 && (await response.text()).includes(needle);
  };
  for (const pathname of ['/', '/src/main.tsx', '/@vite/client']) {
    if (!await get(pathname)) throw new Error('initial readiness failed');
  }
  await fs.appendFile('src/App.tsx', '\nexport const RELOAD_FIXTURE_MARKER = import.meta.env.VITE_RELOAD_FIXTURE;\n');
  const editedAt = Date.now();
  while (!await get('/src/App.tsx', 'RELOAD_FIXTURE_MARKER')) {
    if (Date.now() - editedAt > 5000) throw new Error('source edit unavailable');
  }
  const started = Date.now();
  await fs.writeFile('.env.local', 'VITE_RELOAD_FIXTURE=synthetic-reloaded-value\n');
  let passed = false;
  while (Date.now() - started < 8000) {
    try {
      if (await get('/src/App.tsx', 'synthetic-reloaded-value')) { passed = true; break; }
    } catch { /* An interrupted request during restart is expected. */ }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  const result = { passed, env_reload_ms: Date.now() - started, restarting: !!server._restartPromise,
    pending_requests: server._pendingRequests.size, shutdown_passed: false };
  if (passed) {
    let timer;
    try {
      await Promise.race([server.close(), new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('shutdown timeout')), 5000); })]);
      result.shutdown_passed = true;
    } catch { result.passed = false; }
    finally { clearTimeout(timer); }
  }
  console.log(`RELOAD_RESULT ${JSON.stringify(result)}`);
  process.exit(result.passed ? 0 : 1);
}

if ((await fs.readFile('README.reload-fixture.txt', 'utf8')).trim() !== marker) throw new Error('disposable marker required');
const report = { production_accepted: false, version: require('vite/package.json').version, results: [], patch_atomic: false };
try {
  if (report.version !== '5.4.21') throw new Error('exact Vite version required');
  const bundle = path.join(path.dirname(require.resolve('vite/package.json')), 'dist/node/chunks/dep-BK3b2jBa.js');
  const original = await fs.readFile(bundle);
  if (hash(original) !== (installedPatch ? patchedSHA : originalSHA)) throw new Error('bundle hash mismatch');
  const source = await fs.readFile('src/App.tsx');
  const run = async (variant) => {
    await fs.writeFile('src/App.tsx', source);
    await fs.rm('.env.local', { force: true });
    const child = spawn(process.execPath, [path.resolve('reload-regression.mjs'), 'child'], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] });
    let output = '';
    for (const stream of [child.stdout, child.stderr]) stream.on('data', chunk => { if (output.length < 64_000) output += chunk; });
    const timeout = setTimeout(() => child.kill('SIGKILL'), 30_000);
    const code = await new Promise(resolve => child.on('close', resolve));
    clearTimeout(timeout);
    const line = output.split('\n').find(line => line.startsWith('RELOAD_RESULT '));
    if (!line) throw new Error(`fixture child ${variant} did not report (exit ${code})`);
    const result = { variant, exit_code: code, ...JSON.parse(line.slice('RELOAD_RESULT '.length)) };
    report.results.push(result);
    return result;
  };
  if (!installedPatch) {
    const baseline = await run('unpatched-cold');
    if (baseline.passed || !baseline.restarting || baseline.pending_requests === 0) throw new Error('cold optimizer failure was not reproduced');
    const replacement = original.toString().replace(before, after);
    if (hash(replacement) !== patchedSHA) throw new Error('patch output hash mismatch');
    // Assert atomic replacement cannot mutate pnpm hardlinks in other directories.
    const witness = path.join(root, 'original-bundle-hardlink-witness');
    await fs.link(bundle, witness);
    const stat = await fs.stat(bundle);
    const temporary = `${bundle}.reload-fixture-${randomUUID()}`;
    await fs.writeFile(temporary, replacement, { flag: 'wx', mode: stat.mode & 0o777 });
    await fs.rename(temporary, bundle);
    report.patch_atomic = hash(await fs.readFile(witness)) === originalSHA && hash(await fs.readFile(bundle)) === patchedSHA;
    await fs.unlink(witness);
    if (!report.patch_atomic) throw new Error('atomic patch witness failed');
  } else {
    report.patch_application = 'preinstalled-pnpm-patch';
    report.patch_atomic = null; // This path tests the installer, not atomic replacement.
  }
  for (let i = 0; i < 3; i++) {
    const result = await run(`patched-cold-${i + 1}`);
    if (!result.passed || !result.shutdown_passed) throw new Error('patched cold reload/shutdown failed');
  }
  report.complete = true;
} catch (error) {
  report.error = error.message;
  report.complete = false;
}
await fs.writeFile('reload-results.json', JSON.stringify(report, null, 2));
if (process.env.RELOAD_EXIT_AFTER_REPORT === '1') process.exit(report.complete ? 0 : 1);
// Keep the synthetic supervisor process alive for result collection.
createHTTPServer((_request, response) => { response.writeHead(report.complete ? 200 : 500); response.end('disposable reload fixture'); }).listen(3000, '0.0.0.0');
