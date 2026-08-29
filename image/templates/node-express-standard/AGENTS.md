# This app

Node.js + Express REST API. It starts as a single `server.js`; build what the
task asks for from there (split into modules freely).

## How it runs (platform-managed, do not fight it)

- A supervisor runs `node server.js` on port 3000 (declared in
  `sandbox.yaml`). There is no live reload: the platform restarts the process
  after each task, so your changes take effect when you finish. Never start a
  second server and never change the port.
- The server must listen on `0.0.0.0:3000` and keep answering `GET /health`
  with 200; the platform's readiness probe depends on it.
- Dependencies: the supervisor runs `pnpm install` on boot when needed. Run
  `pnpm install` yourself after editing `package.json`.
- Verify before finishing: `node --check server.js` for syntax. To test a
  route live mid-task, kill the running node process; the supervisor restarts
  it within seconds with your changes, then `curl -s http://127.0.0.1:3000/health`.

## Working style

Generation speed is the bottleneck in this environment: prefer small,
targeted edits over rewriting whole files, and keep code in separate modules
so a change touches little. Run the cheapest check that actually verifies
the change.

## Session memory

Every task runs in a fresh agent session. Files are the only memory:

- `BRAIN.md` carries project state, decisions, and gotchas from earlier
  sessions. Read it before starting; append durable learnings before you
  finish.
- This file carries stable facts about the stack and workflow. If you change
  them, update this file so the next session starts right instead of
  rediscovering it.
