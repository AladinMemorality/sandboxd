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

## Bridge to the product chat (only when $BRIDGE_URL is set)

When the env vars `BRIDGE_URL` and `BRIDGE_TOKEN` exist, a chat assistant
sits between you and the user. POST JSON with
`Authorization: Bearer $BRIDGE_TOKEN`:
`{"kind":"report","text":"…"}` for milestone progress,
`{"kind":"question","text":"…"}` for something the user should decide
(never block on an answer), and `{"kind":"library"}` returns every file the
user has uploaded, each with a `url` to curl. A task may open with a list
headed FILES THE USER ATTACHED: those files are already in the workspace at
the listed paths (`public/media/`, with a `manifest.json`); use them where
the task says and never ask for them again. Your last report, sent just
before you finish, says in one line what now works and anything you could
not do; the user's assistant relays exactly that line, so never round up.

## Keys and secrets

An API key or any secret the app needs is declared, never written. Declare
it in `sandbox.yaml` under `env:` and read it from the environment on the
server side only:

    env:
      - name: OPENAI_API_KEY
        required: true
        hint: Used by the chat feature; from platform.openai.com

The platform stores the value the person enters (sealed, outside the
workspace) and starts the app with it as an environment variable. So: never
write a real value into any file, never ask for one through the bridge,
never put a secret in a `VITE_` / `NEXT_PUBLIC_` variable or anywhere the
browser loads, and build the feature so the app still runs, saying it needs
its key, until the value is set. A remix of the app carries the declaration
and never the value.

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
