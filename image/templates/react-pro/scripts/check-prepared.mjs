import { readFileSync, existsSync } from 'node:fs'
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'
const require = createRequire(import.meta.url)
const pkg = JSON.parse(readFileSync(new URL('../package.json', import.meta.url)))
const root = new URL('../', import.meta.url)
for (const [name, version] of Object.entries({ ...pkg.dependencies, ...pkg.devDependencies })) {
  const installed = JSON.parse(readFileSync(new URL(`node_modules/${name}/package.json`, root)))
  assert.equal(installed.version, version, `${name}: prepared version must match exact pin`)
}
for (const name of ['motion/react', 'hls.js', 'gsap', 'lenis', 'three', 'react-router-dom']) require.resolve(name)
for (const name of ['vite', 'tsc']) assert(existsSync(new URL(`node_modules/.bin/${name}`, root)), `Missing ${name}`)
for (const composition of ['marketing', 'portfolio', 'app']) assert(existsSync(new URL(`src/pages/${composition}/index.tsx`, root)))
assert(existsSync(new URL('patches/vite@5.4.21.patch', root)), 'Keep the reviewed Vite cold-reload patch')
assert.equal(JSON.parse(readFileSync(new URL('.baarcha/template.json', root))).version, '2.0.0')
console.log('Prepared React Pro 2.0.0: exact dependencies, commands and all page compositions available.')
