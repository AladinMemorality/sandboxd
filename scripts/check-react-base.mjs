// Run against a disposable copy; never edit a real sandbox or the source starter.
// PLAYWRIGHT_PACKAGE_JSON points to a checkout with playwright-core installed.
import { mkdtemp, cp, symlink, writeFile, mkdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { resolve, join } from 'node:path'
import { createRequire } from 'node:module'
import { spawn, execFileSync } from 'node:child_process'
import assert from 'node:assert/strict'
const require = createRequire(process.env.PLAYWRIGHT_PACKAGE_JSON || import.meta.url)
const { chromium } = require('playwright-core')
const source = resolve(process.argv[2] || 'image/templates/react-pro')
const output = resolve(process.env.REACT_BASE_RESULTS || '/tmp/react-base-results')
const root = await mkdtemp(join(tmpdir(), 'react-base-'))
await cp(source, root, { recursive: true, filter: path => !/(?:^|\/)(node_modules|dist|\.git)(?:\/|$)|\.tsbuildinfo$/.test(path) })
await symlink(join(source, 'node_modules'), join(root, 'node_modules'), 'dir')
await mkdir(output, { recursive: true })
await mkdir(join(root, 'public/check-media'), { recursive: true })
execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'lavfi', '-i', 'color=c=0x768c79:s=320x180:r=24:d=3', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', join(root, 'public/check-media/clip.mp4')])
execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-i', join(root, 'public/check-media/clip.mp4'), '-codec', 'copy', '-hls_time', '1', '-hls_list_size', '0', join(root, 'public/check-media/clip.m3u8')])
await writeFile(join(root, 'public/check-media/poster.svg'), '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><rect width="320" height="180" fill="#768c79"/></svg>')
await writeFile(join(root, '__checks.html'), '<html><body><div id="root"></div><script type="module" src="/src/check-fixture.tsx"></script></body></html>')
await writeFile(join(root, 'src/check-fixture.tsx'), `
import React, { useCallback, useRef, useState } from 'react'; import { createRoot } from 'react-dom/client';
import './index.css'; import { BackgroundVideo } from './components/media/BackgroundVideo';
import { ResponsiveImage } from './components/media/ResponsiveImage'; import { Scene } from './modules/three/Scene';
import { useScrollScene } from './modules/scroll/useScrollScene'; import { useSmoothScroll } from './modules/scroll/useSmoothScroll';
function Effects() {
  const ref = useRef(null); useSmoothScroll(true);
  useScrollScene(ref, useCallback(gsap => { gsap.to('.box', { x: 80, scrollTrigger: { trigger: ref.current, start: 'top bottom', end: 'bottom top', scrub: true } }) }, []));
  const create = useCallback(THREE => {
    const scene = new THREE.Scene(), camera = new THREE.PerspectiveCamera(45, 1, .1, 100); camera.position.z = 4;
    const geometry = new THREE.BoxGeometry(), material = new THREE.MeshBasicMaterial({ color: '#768c79' }), mesh = new THREE.Mesh(geometry, material); scene.add(mesh);
    return { scene, camera, frame: t => { mesh.rotation.y = t * .2 }, dispose: () => { geometry.dispose(); material.dispose(); document.documentElement.dataset.disposed = 'yes' } };
  }, []);
  return <div ref={ref}><div className="box">Scroll scene</div><Scene create={create} fallback={<p>Static scene</p>} label="Sample scene" /></div>;
}
function Checks() { const [mounted, setMounted] = useState(true); return <>
<button onClick={() => setMounted(v => !v)}>Toggle effects</button>{mounted && <Effects />}
<ResponsiveImage src="/check-media/missing.png" alt="Image fallback" />
<div id="mp4"><BackgroundVideo src="/check-media/clip.mp4" poster="/check-media/poster.svg" label="MP4 poster" /></div>
<div id="hls"><BackgroundVideo src="/check-media/clip.m3u8" type="hls" poster="/check-media/poster.svg" label="HLS poster" /></div>
<div id="broken"><BackgroundVideo src="/check-media/missing.mp4" poster="/check-media/poster.svg" label="Failure poster" /></div>
</> } createRoot(document.getElementById('root')).render(<React.StrictMode><Checks /></React.StrictMode>);
`)
const port = 4187
const server = spawn('node', [join(root, 'node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', String(port)], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] })
let logs = ''; server.stdout.on('data', value => { logs += value }); server.stderr.on('data', value => { logs += value })
let browser
const results = []
try {
  for (let n = 0; n < 100; n++) {
    if (server.exitCode !== null) throw new Error(logs)
    try { if ((await fetch(`http://127.0.0.1:${port}`)).ok) break } catch {}
    await new Promise(done => setTimeout(done, 100))
  }
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true })
  for (const composition of ['marketing', 'portfolio', 'app']) {
    await writeFile(join(root, 'src/content/starter.json'), JSON.stringify({ composition }))
    // Start a new context after Vite invalidates its JSON module.
    await new Promise(done => setTimeout(done, 250))
    for (const width of [390, 1440]) {
      const context = await browser.newContext({ viewport: { width, height: 1000 }, reducedMotion: 'reduce' })
      const page = await context.newPage(); const errors = []
      page.on('pageerror', error => errors.push(error.message))
      await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: 'networkidle' })
      await page.locator('h1').waitFor()
      assert(await page.locator('h1').innerText())
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${composition}: overflow at ${width}`)
      await page.screenshot({ path: join(output, `${composition}-${width}.png`), fullPage: true })
      if (width === 390) {
        await page.getByRole('button', { name: 'Open menu' }).click()
        await page.getByRole('dialog').waitFor()
        await page.keyboard.press('Escape')
        await page.getByRole('dialog').waitFor({ state: 'hidden' })
      } else assert.equal(await page.getByRole('button', { name: 'Open menu' }).isVisible(), false)
      if (composition === 'marketing') {
        await page.getByRole('link', { name: 'Find your space' }).click()
        await page.getByRole('button', { name: 'Yearly', exact: true }).click()
        assert.match(await page.locator('main').innerText(), /\$108 billed yearly/)
        await page.getByRole('link', { name: 'Ask about Personal' }).click()
        await page.getByLabel('Your name').fill('Sample Person'); await page.getByLabel('Email address').fill('sample@example.com')
        await page.getByRole('button', { name: 'Prepare email' }).click()
        assert.match(await page.getByRole('link', { name: /Open your email draft/ }).getAttribute('href'), /^mailto:.*Personal/)
        await page.reload(); await page.getByRole('heading', { name: /What’s on/ }).waitFor()
      } else if (composition === 'portfolio') {
        await page.getByRole('button', { name: 'Editorial', exact: true }).click()
        assert.equal(await page.locator('.work-card').count(), 1)
        await page.locator('.work-card').click(); await page.getByRole('heading', { level: 1, name: 'Field notes' }).waitFor()
        await page.reload(); await page.getByRole('heading', { level: 1, name: 'Field notes' }).waitFor()
      } else {
        await page.getByRole('link', { name: 'View all' }).click()
        await page.getByLabel('New project name').fill('Browser acceptance')
        await page.getByRole('button', { name: 'Add project' }).click()
        await page.getByRole('searchbox').fill('Browser acceptance')
        assert.equal(await page.locator('.project-list > a').count(), 1)
        await page.locator('.project-list > a').click()
        await page.getByRole('button', { name: 'Mark complete' }).click()
        await page.getByRole('button', { name: 'Reopen project' }).waitFor()
        await page.goto(`http://127.0.0.1:${port}/settings`)
        await page.getByLabel('Workspace name').fill('My studio')
        await page.getByRole('button', { name: 'Save changes' }).click()
        await page.getByRole('status').filter({ hasText: 'Workspace name updated.' }).waitFor()
      }
      await page.goto(`http://127.0.0.1:${port}/missing`); await page.getByRole('heading', { name: 'This page isn’t here.' }).waitFor()
      assert.deepEqual(errors, [])
      results.push({ composition, width, passed: true }); await context.close()
    }
  }
  for (const reducedMotion of ['no-preference', 'reduce']) {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, reducedMotion })
    const page = await context.newPage(); const errors = []; page.on('pageerror', error => errors.push(error.message))
    await page.goto(`http://127.0.0.1:${port}/__checks.html`, { waitUntil: 'networkidle' })
    await page.getByRole('img', { name: 'Image fallback', exact: true }).waitFor()
    if (reducedMotion === 'reduce') {
      assert.equal(await page.locator('canvas').count(), 0)
      assert.equal(await page.locator('video[src]').count(), 0)
      assert.equal(await page.getByRole('button', { name: 'Pause video' }).count(), 0)
    } else {
      await page.locator('canvas').waitFor()
      for (const id of ['mp4', 'hls']) {
        await page.locator('#' + id).scrollIntoViewIfNeeded()
        await page.waitForFunction(id => { const video = document.querySelector('#' + id + ' video'); return video.readyState >= 2 && !video.paused }, id)
        await page.locator('#' + id).getByRole('button', { name: 'Pause video' }).click()
        await page.waitForFunction(id => document.querySelector('#' + id + ' video').paused, id)
        await page.locator('#' + id).getByRole('button', { name: 'Play video' }).click()
      }
      await page.locator('#broken').scrollIntoViewIfNeeded()
      await page.waitForFunction(() => getComputedStyle(document.querySelector('#broken video')).opacity === '0')
      assert.equal(await page.locator('#broken img').isVisible(), true)
      await page.getByRole('button', { name: 'Toggle effects' }).click()
      assert.equal(await page.locator('canvas').count(), 0)
      assert.equal(await page.locator('html').getAttribute('data-disposed'), 'yes')
    }
    assert.deepEqual(errors, []); results.push({ mediaAndEffects: reducedMotion, passed: true }); await context.close()
  }
  console.log(JSON.stringify(results, null, 2)); await writeFile(join(output, 'results.json'), JSON.stringify(results, null, 2))
} finally { await browser?.close(); server.kill('SIGTERM'); await rm(root, { recursive: true, force: true }) }
