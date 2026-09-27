// Run under the cohort's traffic fence against the normal authenticated API.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import crypto from 'node:crypto';
import http from 'node:http';
const [configPath, output] = process.argv.slice(2);
assert(configPath?.startsWith('/') && output?.startsWith('/'));
const config = JSON.parse(await fs.readFile(configPath));
assert([1, 2].includes(config.version) && config.projects.length > 0 && config.projects.length <= (config.version === 2 ? 12 : 4));
const rt = new URL(process.env.SANDBOXD_URL);
assert(rt.origin === 'http://127.0.0.1:9090' && process.env.SANDBOXD_TOKEN);
const hash = value => crypto.createHash('sha256').update(value).digest('hex');
async function request(url, {method = 'GET', headers = {}} = {}) {
  if (headers.host) {
    assert(url.origin === rt.origin, 'Preview probe must remain on the local controller');
    return new Promise((resolve, reject) => {
      const req = http.request(url, {method, headers}, response => {
        const chunks = []; let size = 0;
        response.on('data', chunk => {
          size += chunk.length;
          if (size > 20 * 1024 * 1024) req.destroy(new Error('Preview response exceeds limit'));
          else chunks.push(chunk);
        });
        response.on('error', reject);
        response.on('end', () => resolve({status: response.statusCode, body: Buffer.concat(chunks),
          headers: {get: name => response.headers[name.toLowerCase()] || null}}));
      });
      req.setTimeout(60000, () => req.destroy(new Error('Preview request timed out')));
      req.on('error', reject); req.end();
    });
  }
  const response = await fetch(url, {method, headers, redirect: 'manual', signal: AbortSignal.timeout(60000)});
  const body = Buffer.from(await response.arrayBuffer());
  assert(body.length < 20 * 1024 * 1024);
  let json; try { json = JSON.parse(body); } catch {}
  return {status: response.status, body, json, headers: response.headers};
}
const runtime = (route, options = {}) => request(new URL(route, rt), {...options,
  headers: {authorization: 'Bearer ' + process.env.SANDBOXD_TOKEN}});
const report = {version: 1, started_at: new Date().toISOString(), projects: [], ai_tasks_submitted: 0};
try {
  for (const row of config.projects) {
    const sb = row.sandbox_id, app = row.app_id;
    assert(/^[0-9A-HJKMNP-TV-Z]{26}$/.test(sb) && /^[0-9A-HJKMNP-TV-Z]{26}$/.test(app));
    const result = {sandbox_id: sb, app_id: app}; report.projects.push(result);
    const original = '/var/lib/sandboxd/workspaces/' + sb;
    const appResponse = await runtime('/v1/apps/' + app);
    assert(appResponse.status === 200 && appResponse.json.current_sandbox_id === sb, 'App identity changed');
    const sandbox = await runtime('/v1/sandboxes/' + sb);
    assert(sandbox.status === 200 && sandbox.json.runtime_provider === 'cube', 'Cube binding unavailable');
    const origin = new URL(sandbox.json.preview.url);
    assert(origin.origin === `https://s-${sb.toLowerCase()}-3000.preview.65.108.225.153.sslip.io`, 'Stable preview URL changed');
    const access = await runtime('/v1/sandboxes/' + sb + '/preview-access', {method: 'POST'});
    assert(access.status === 200 && new URL(access.json.url).origin === origin.origin, 'Preview authorization unavailable');
    const preview = path => {
      const parsed = new URL(path, origin); assert(parsed.origin === origin.origin, 'Foreign preview asset refused');
      const target = new URL(rt); target.pathname = parsed.pathname; target.search = parsed.search;
      return request(target, {headers: {
        accept: 'text/html,application/xhtml+xml,*/*;q=0.8',
        host: origin.host, cookie: 'sandbox_preview=' + access.json.token, 'x-forwarded-proto': 'https'}});
    };
    const began = performance.now(); let page;
    for (let attempt = 0; attempt < 60; attempt++) {
      page = await preview('/');
      if (page.status === 200) break;
      assert([502, 503, 504].includes(page.status), 'Unexpected preview status: ' + page.status);
      await new Promise(resolve => setTimeout(resolve, 500));
    }
    assert(page.status === 200 && /text\/html/.test(page.headers.get('content-type') || '') && page.body.length > 100, 'Application HTML unavailable');
    result.preview_status = page.status; result.preview_ready_ms = performance.now() - began;
    const scripts = [...page.body.toString().matchAll(/<script\b[^>]*\bsrc=["']([^"']+)["'][^>]*>/gi)]
      .map(match => new URL(match[1], origin)).filter(url => url.origin === origin.origin).slice(0, 8);
    for (const script of scripts) {
      const asset = await preview(script.pathname + script.search);
      assert(asset.status === 200 && /(?:javascript|ecmascript)/.test(asset.headers.get('content-type') || '')
        && asset.body.length > 0, 'Application entry module unavailable');
    }
    result.entry_modules_verified = scripts.length;
    if (['01M2P1KJ7G17D9QMFE7QNXG1YX', '01M3EX97H7T5C9588F4HDHXVX7'].includes(app)) {
      assert(row.preset === 'node-express', 'Minecraft dashboard preset changed');
      const health = await preview('/health');
      assert(health.status === 200 && JSON.parse(health.body).status === 'ok', 'Minecraft dashboard health unavailable');
      const status = await preview('/api/status');
      assert(status.status === 200 && typeof JSON.parse(status.body).status === 'string', 'Minecraft dashboard status unavailable');
      // The user explicitly removed public game tunnels from rollout scope.
      // Native migration still verifies all archived files, including worlds.
      result.minecraft_dashboard = {health: true, status: true, public_tunnel_verified: false};
    }
    if (app === '01M3CKN983PFRGMD711PCEPDFD') {
      assert(sb === '01M3CKN99ZF90BEEA4DS66YAQV', 'Motion project identity changed');
      const status = await preview('/api/status');
      assert(status.status === 200 && JSON.parse(status.body).mode === 'shared-workspace', 'Motion worker status unavailable through production preview');
      const response = await preview('/api/projects');
      assert(response.status === 200, 'Motion worker projects unavailable through production preview');
      const value = JSON.parse(response.body);
      assert(Array.isArray(value.projects), 'Invalid Motion worker projects response');
      result.motion_worker = {shared_workspace: true, projects: value.projects.length};
    }
    if (app === '01M37PPK7VDKCQMNRYKW8CCD4W') {
      assert(sb === '01M37PPK85JN1K0WEMP4ZYER6C', 'PostgreSQL project identity changed');
      // This application's health handler executes SELECT 1 against its private
      // PostgreSQL socket. The public handler also reads items/settings/wa_state.
      // Native migration already compared the complete frozen database files
      // before resuming workers; these checks exercise the transferred database.
      const health = await preview('/api/health');
      assert(health.status === 200 && health.body.length <= 8192, 'PostgreSQL health unavailable');
      const database = JSON.parse(health.body);
      assert(database.ok === true && database.database === 'PostgreSQL' && typeof database.demo === 'boolean',
        'Application SQL health check failed');
      const response = await preview('/api/public');
      assert(response.status === 200, 'PostgreSQL application reads unavailable');
      const catalog = JSON.parse(response.body);
      assert(Array.isArray(catalog.items) && catalog.items.length <= 24 && Number.isInteger(catalog.commission)
        && catalog.demo === database.demo && (catalog.whatsappUrl === null || typeof catalog.whatsappUrl === 'string'),
        'Transferred application database response is invalid');
      result.postgres = {sql_health: true, application_tables_read: true, public_items: catalog.items.length};
    }
    result.files_verified = [];
    // Next.js sources render app/page.js; Express dashboards use server.js.
    // The native migration still verifies the entire workspace;
    // these files additionally exercise the normal authenticated file API.
    const sourceFiles = ['package.json', 'sandbox.yaml', row.preset === 'nextjs' ? 'app/page.js'
      : row.preset === 'node-express' ? 'server.js' : 'index.html'];
    for (const name of sourceFiles) {
      const before = await fs.readFile(original + '/workspace/app/' + name);
      const current = await runtime('/v1/sandboxes/' + sb + '/files/content?path=' + encodeURIComponent(name));
      assert(current.status === 200 && hash(current.body) === hash(before), 'Transferred source differs: ' + name);
      result.files_verified.push(name);
    }
    const tasks = await runtime('/v1/sandboxes/' + sb + '/tasks');
    assert(tasks.status === 200 && Array.isArray(tasks.json.tasks), 'Task history unavailable');
    for (const task of tasks.json.tasks) {
      assert(/^[0-9A-HJKMNP-TV-Z]{26}$/.test(task.id));
      const old = JSON.parse(await fs.readFile(original + '/.runtimed/tasks/' + task.id + '/result.json'));
      const current = await runtime('/v1/sandboxes/' + sb + '/tasks/' + task.id);
      assert(current.status === 200 && current.json.status === old.status, 'Historical result changed');
      const events = await runtime('/v1/sandboxes/' + sb + '/tasks/' + task.id + '/events');
      assert(events.status === 200 && events.body.length > 0, 'Historical events unavailable');
    }
    result.historical_tasks_verified = tasks.json.tasks.length;
    // Normal controller pause checks both canonical and supervisor task state
    // under its sandbox lock. It refuses an active customer coding task.
    const stop = await runtime('/v1/sandboxes/' + sb + '/stop', {method: 'POST'});
    assert(stop.status === 200, 'Verified project could not return to its original paused state');
    const paused = await runtime('/v1/sandboxes/' + sb);
    assert(paused.status === 200 && paused.json.status === 'stopped' && paused.json.runtime_provider === 'cube', 'Paused Cube state unavailable');
    result.paused_after_verification = true; result.success = true;
  }
  report.success = true; report.completed_at = new Date().toISOString();
} catch (error) {
  report.success = false; report.error = error.message; process.exitCode = 1;
}
await fs.writeFile(output, JSON.stringify(report, null, 2) + '\n', {flag: 'wx', mode: 0o600});
console.log(JSON.stringify(report));
