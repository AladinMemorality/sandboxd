/* Forwards every uncaught browser error to the dev server's error sink
   (see vite.config.ts), where it lands in .runtime-errors.log for the next
   coding-agent session to read. Fire-and-forget: reporting must never
   affect the app. Imported once from main.tsx — leave that import alone. */

function send(payload: { message: string; stack?: string; source?: string }) {
  try {
    void fetch('/__report-error', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ ...payload, url: location.href }),
      keepalive: true,
    }).catch(() => {})
  } catch {
    /* never throw from the reporter */
  }
}

window.addEventListener('error', (event) => {
  send({
    message: event.message || String(event.error ?? 'unknown error'),
    stack: event.error instanceof Error ? event.error.stack : undefined,
    source: event.filename ? `${event.filename}:${event.lineno ?? 0}` : undefined,
  })
})

window.addEventListener('unhandledrejection', (event) => {
  const r = event.reason
  send({
    message: r instanceof Error ? r.message : String(r),
    stack: r instanceof Error ? r.stack : undefined,
    source: 'unhandledrejection',
  })
})

export {}
