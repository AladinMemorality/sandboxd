# Ready React interaction checks

`jsdom` and `esbuild` are installed here, outside the app. Do not install them
again or add them to package.json. Prefer a project's existing tests when present.
This helper mounts a React component in a **simulated DOM**. It can test state,
click handlers and form logic; it cannot verify CSS, responsive layout, browser
APIs or real server integrations. Use Hannibal’s chat-side screenshot review
to inspect layout. Missing browser APIs are limitations, not passing tests.

From the workspace, write a focused temporary check (adjust the assertions to
the actual requested interaction):

```js
// /tmp/check-app.mjs
import assert from 'node:assert/strict';
import { mountApp } from '/opt/agent-tools/ui-test.mjs';
const app = await mountApp(); // default: current workspace's src/App.tsx
try {
  await app.waitFor(() => assert.ok(app.document.querySelector('button')));
  const button = [...app.document.querySelectorAll('button')]
    .find(b => b.textContent === 'Join waitlist');
  assert.ok(button, 'Join waitlist button exists');
  button.click();
  await app.waitFor(() => assert.match(app.document.body.textContent, /You are on the list/));
  console.log('PASS: waitlist state changes after click (simulated DOM)');
} finally { app.close(); }
```

Run `timeout 30 node /tmp/check-app.mjs` **from the app directory**. Nonzero exit
means failure. A different entry can be passed to `mountApp({entry:'src/App.jsx'})`.
It bundles in memory with the app's installed React, supports `@/` imports and
omits CSS. It does not start a server or write build artifacts. It does not mock
fetch or silently stub missing APIs; write explicit task-specific fixtures if
appropriate and disclose their limits. If this helper does not support the app's
framework or APIs, use its own checks and report the gap; do not repeatedly
install browser packages as a workaround. Delete the temporary check afterward.
