import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, mkdir, writeFile, symlink, rm, readdir } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { mountApp } from "./ui-test.mjs";

async function fixture(t, source) {
  const root = await mkdtemp(join(tmpdir(), "agent-ui-check-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, "src"));
  await symlink(fileURLToPath(new URL("node_modules", import.meta.url)), join(root, "node_modules"), "dir");
  await writeFile(join(root, "src/App.tsx"), source);
  return root;
}

test("checks actual React state changes with aliases, without writing build files", async t => {
  const root = await fixture(t, `import {useState} from 'react'; import {label} from '@/label'; import './index.css';
    export default function App(){const [joined,setJoined]=useState(false);return <button onClick={()=>setJoined(true)}>{joined?'You are on the list':label}</button>}`);
  await writeFile(join(root, "src/label.ts"), "export const label='Join waitlist'");
  await writeFile(join(root, "src/index.css"), '@import "tailwindcss";');
  const before = await readdir(root);
  const app = await mountApp({root});
  try {
    await app.waitFor(() => assert.equal(app.document.querySelector("button")?.textContent, "Join waitlist"));
    app.document.querySelector("button").click();
    await app.waitFor(() => assert.equal(app.document.querySelector("button").textContent, "You are on the list"));
    await assert.rejects(app.waitFor(() => assert.match(app.document.body.textContent, /impossible/), 50));
  } finally { app.close(); }
  assert.deepEqual(await readdir(root), before);
});

test("does not silently pass missing browser APIs or build errors", async t => {
  const root = await fixture(t, `export default function App(){ throw new Error('unsupported browser API'); }`);
  await assert.rejects(mountApp({root}), /unsupported browser API/);
  await writeFile(join(root, "src/App.tsx"), "export default function broken {");
  await assert.rejects(mountApp({root}));
});
