import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import fs from 'node:fs'
import path from 'node:path'

// Runtime-error sink: the page (src/lib/report-errors.ts) POSTs every
// window.onerror / unhandledrejection here, and the dev server appends it
// to .runtime-errors.log. That file is how the NEXT coding-agent session
// sees what actually broke in the user's browser — the platform's answer
// to "the agent can't watch the preview". Log-only, dev-only, same-origin.
function errorSink(): Plugin {
  return {
    name: 'sandbox-error-sink',
    configureServer(server) {
      server.middlewares.use('/__report-error', (req, res) => {
        if (req.method !== 'POST') {
          res.statusCode = 405
          return res.end()
        }
        let body = ''
        req.on('data', (c) => {
          if (body.length < 32_000) body += c
        })
        req.on('end', () => {
          try {
            const e = JSON.parse(body || '{}')
            const line = JSON.stringify({
              ts: new Date().toISOString(),
              message: String(e.message ?? '').slice(0, 2000),
              stack: String(e.stack ?? '').slice(0, 4000),
              source: String(e.source ?? ''),
              url: String(e.url ?? ''),
            })
            fs.appendFileSync(path.resolve('.runtime-errors.log'), line + '\n')
          } catch {
            /* a malformed report is not worth crashing the dev server over */
          }
          res.statusCode = 204
          res.end()
        })
      })
    },
  }
}

// The preview is served through Traefik on a per-sandbox host
// (s-<id>-3000.preview.<domain>), so the dev server must listen on all
// interfaces and accept that forwarded Host header.
export default defineConfig({
  plugins: [react(), errorSink()],
  server: {
    host: true,
    port: 3000,
    strictPort: true,
    allowedHosts: true,
  },
})
