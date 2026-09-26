// Run only after the foreground probe has exited and while its maintenance
// fences remain held. No worker DELETE endpoint exists; remove only its newly
// created, marker-identified disposable film, after verifying every baseline.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {writeReceipt} from './probe.mjs';
const UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/;
const read = async file => {
  assert.ok((await fs.lstat(file)).isFile(), 'Expected a regular project or receipt file');
  return JSON.parse(await fs.readFile(file, 'utf8'));
};
const hash = value => createHash('sha256').update(JSON.stringify(value)).digest('hex');
export async function cleanup(root, directory) {
  assert.equal(await fs.realpath(root), root, 'Worker root must not be redirected');
  let intent;
  try { intent = await read(path.join(directory, 'film-create-intent.json')); }
  catch (error) {
    if (error.code !== 'ENOENT') throw error;
    await writeReceipt(directory, 'film-cleanup.json', {complete: true, films_removed: 0, create_not_attempted: true});
    return;
  }
  assert.match(intent.marker, /^cube-motion-owned-[a-f0-9]{32}$/);
  // Closing the guest channel does not prove a worker upload/ffprobe tail has
  // finished. An interrupted request without a complete HTTP response needs
  // explicit reconciliation; do not remove a directory the worker may recreate.
  let complete;
  try { complete = await read(path.join(directory, 'http-complete.json')); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (complete) {
    assert.equal(complete.success, true); assert.equal(complete.marker, intent.marker);
  } else {
    const failure = await read(path.join(directory, 'http-failure.json'));
    assert.ok(Number.isInteger(failure.last_request?.status), 'Worker request outcome is uncertain; explicit reconciliation required');
  }
  const baseline = await read(path.join(directory, 'worker-baseline.json'));
  assert.deepEqual(Object.keys(baseline).sort(), intent.existing.sort());
  const verify = async () => {
    for (const [id, digest] of Object.entries(baseline)) {
      assert.match(id, UUID);
      assert.equal(await fs.realpath(path.join(root, id)), path.join(root, id));
      assert.equal(hash(await read(path.join(root, id, 'project.json'))), digest, 'Existing worker project changed');
    }
  };
  await verify();
  const extra = (await fs.readdir(root)).filter(id => UUID.test(id) && !(id in baseline));
  assert.ok(extra.length <= 1, 'Unexpected worker project creation; preserve everything');
  let acknowledged;
  try { acknowledged = await read(path.join(directory, 'film-created.json')); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (acknowledged) {
    assert.equal(acknowledged.marker, intent.marker);
    assert.deepEqual(extra, [acknowledged.id]);
  }
  for (const id of extra) {
    const target = path.join(root, id);
    assert.equal(await fs.realpath(target), target);
    const project = await read(path.join(target, 'project.json'));
    assert.equal(project.id, id); assert.equal(project.brief.brand, intent.marker);
    assert.deepEqual(project.jobs, []); assert.deepEqual(project.renders, []);
    assert.ok(!project.voice && !project.narration && !project.generations?.length);
    await writeReceipt(directory, 'film-delete-intent.json', {id, marker: intent.marker});
    await fs.rm(target, {recursive: true});
  }
  await verify();
  assert.deepEqual((await fs.readdir(root)).filter(id => UUID.test(id)).sort(), Object.keys(baseline).sort());
  await writeReceipt(directory, 'film-cleanup.json', {complete: true, films_removed: extra.length, baseline_projects_unchanged: Object.keys(baseline).length});
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  assert.equal(process.argv.length, 3);
  cleanup('/opt/baarcha/motion-studio/data', process.argv[2]).catch(() => {
    console.error('Owned Motion film cleanup refused; preserve fixtures and inspect private receipts.');
    process.exitCode = 1;
  });
}
