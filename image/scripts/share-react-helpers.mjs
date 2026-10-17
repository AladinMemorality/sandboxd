// Trusted image-build directories only; never run against customer workspaces.
import { cpSync, readFileSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
const root = resolve(process.argv[2] ?? '/opt/templates')
const source = join(root, 'react-pro'), target = join(root, 'marketplace-starter')
for (const path of ['src/components/media', 'src/components/motion', 'src/modules', 'src/styles/media.css']) {
  cpSync(join(source, path), join(target, path), { recursive: true })
}
cpSync(join(source, 'STARTER.md'), join(target, 'REACT-HELPERS.md'))
const css = join(target, 'src/index.css')
const text = readFileSync(css, 'utf8')
if (!text.includes('./styles/media.css')) writeFileSync(css, '@import "./styles/media.css";\n' + text)
