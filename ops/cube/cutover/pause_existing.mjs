// Called only inside the cohort's verified traffic/task drain, before shutdown.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
const [path] = process.argv.slice(2);
const bindings = JSON.parse(await fs.readFile(path));
assert(Array.isArray(bindings) && bindings.length <= 1024);
const origin = new URL(process.env.SANDBOXD_URL);
assert(origin.origin === 'http://127.0.0.1:9090' && process.env.SANDBOXD_TOKEN);
async function api(sid, suffix = '', method = 'GET') {
  const response = await fetch(new URL('/v1/sandboxes/' + sid + suffix, origin), {
    method, headers: {authorization: 'Bearer ' + process.env.SANDBOXD_TOKEN},
    redirect: 'error', signal: AbortSignal.timeout(60000),
  });
  assert(response.status === 200, 'Task-aware pause or status refused');
  return response.json();
}
const paused = [];
const newlyPausedCube = [];
for (const binding of bindings) {
  const sid = binding.sandbox_id;
  assert(/^[0-9A-HJKMNP-TV-Z]{26}$/.test(sid) && !paused.includes(sid));
  assert(['cube', 'docker'].includes(binding.provider));
  const before = await api(sid);
  assert(before.runtime_provider === binding.provider, 'Existing binding changed');
  if (before.status !== 'stopped') {
    await api(sid, '/stop', 'POST');
    if (binding.provider === 'cube') newlyPausedCube.push(sid);
  }
  const after = await api(sid);
  assert(after.runtime_provider === binding.provider && after.status === 'stopped', 'Guest did not pause');
  paused.push(sid);
}
console.log(JSON.stringify({success: true, paused, newly_paused_cube: newlyPausedCube, ai_tasks_submitted: 0}));
