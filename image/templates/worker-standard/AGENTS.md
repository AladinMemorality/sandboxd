# This app

A background worker with no web preview. It starts as a single `worker.sh`;
build what the task asks for from there (other languages are fine, update
`sandbox.yaml`'s worker command if you change the entry point).

## How it runs (platform-managed, do not fight it)

- A supervisor runs `bash worker.sh` (declared in `sandbox.yaml`) and restarts
  it after each task, so your changes take effect when you finish. Keep the
  script long-running or exiting cleanly; a crash loop shows up as constant
  restarts.
- There is no web endpoint and no build check for this preset.
- Verify before finishing: `bash -n worker.sh` for syntax, and run one
  iteration of the work by hand if the script allows it.

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
