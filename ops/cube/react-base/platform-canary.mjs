// Run on the platform host with its environment loaded, after default cutover.
// Creates/deletes one synthetic operator app. Never submits a model task.
import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { prepareStarter } from '/opt/baarcha/app/landing/src/lib/projects/starter.ts';

const root = '/opt/baarcha-bench/react-base-20261010/platform-canary';
await mkdir(root, { mode: 0o700 });
const base = process.env.SANDBOXD_URL.replace(/\/$/, '');
const owner = 'operator-react-base-release-20261010';
const headers = { authorization: `Bearer ${process.env.SANDBOXD_TOKEN}`, 'content-type': 'application/json' };
async function api(path, options = {}) {
  const response = await fetch(base + path, { ...options, headers: { ...headers, ...options.headers }, signal: AbortSignal.timeout(150_000) });
  if (!response.ok) throw new Error(`Controller ${path.split('?')[0]} returned ${response.status}`);
  return response;
}
const save = (name, value) => writeFile(`${root}/${name}.json`, JSON.stringify(value, null, 2), { mode: 0o600 });
const intent = { name: 'React base release verification', external_user_id: owner, external_project_id: owner, runtime_preset: 'react-pro' };
await save('create-intent', intent);
const app = await (await api('/v1/apps', { method: 'POST', body: JSON.stringify(intent) })).json();
await save('created-app', { id: app.id });
let passed = false;
let sandbox;
try {
  sandbox = await (await api(`/v1/apps/${app.id}/sandbox`, { method: 'POST', body: JSON.stringify({ runtime_preset: 'react-pro' }) })).json();
  await save('created-sandbox', { id: sandbox.id });
  const read = async path => (await api(`/v1/sandboxes/${sandbox.id}/files/content?path=${encodeURIComponent(path)}`)).text();
  assert.equal(JSON.parse(await read('.baarcha/template.json')).version, '2.0.0');
  const prompt = 'Create an app using the prepared dashboard. Release verification only.';
  const instructions = await prepareStarter({ preset: 'react-pro', imported: false, composition: 'app', prompt }, {
    read,
    write: (path, content) => api(`/v1/sandboxes/${sandbox.id}/files?path=${encodeURIComponent(path)}`, { method: 'PUT', headers: { 'content-type': 'application/octet-stream' }, body: content }),
  });
  assert.match(instructions, /selected composition: app/);
  assert.equal(JSON.parse(await read('src/content/starter.json')).composition, 'app');
  assert.equal(JSON.parse(await read('.baarcha/request.json')).prompt, prompt);
  let status;
  for (let attempt = 0; attempt < 60; attempt++) {
    status = await (await api(`/v1/sandboxes/${sandbox.id}`)).json();
    if (status.preview?.status === 'ready') break;
    await new Promise(resolve => setTimeout(resolve, 1000));
  }
  assert.equal(status.preview?.status, 'ready');
  const tasks = await (await api(`/v1/sandboxes/${sandbox.id}/tasks`)).json();
  assert.equal(tasks.tasks?.length ?? (Array.isArray(tasks) ? tasks.length : -1), 0);
  passed = true;
} finally {
  const owned = await (await api(`/v1/apps/${app.id}`)).json();
  assert.equal(owned.external_user_id, owner, 'Cleanup ownership mismatch');
  await api(`/v1/apps/${app.id}`, { method: 'DELETE' });
  const result = { passed, template_revision: '2.0.0', composition: 'app', normal_controller_creation: true, actual_platform_preparation: true, no_model_calls: true, deleted: true };
  await save('result', result);
  console.log(JSON.stringify(result));
}
