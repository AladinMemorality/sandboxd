# Messages during a coding task

Claude Code tasks use native `--input-format stream-json` input with
`--replay-user-messages`. A correction sent while a tool runs enters the next
model request within the same session. The CLI retains its own transcript;
the running tool is not cancelled and the task is not restarted.

`POST /v1/sandboxes/{id}/tasks/{taskId}/messages` accepts a UUID `message_id` and
`prompt` (at most 80,000 UTF-8 bytes). The project must belong to the authenticated
API tenant, and the task must belong to that sandbox. Reusing an id with the
same text does not send twice; different text with the same id fails. A task
accepts at most 100 follow-ups. Follow-ups are recorded in its event log.

A 202 means accepted by the running session. An `input` event with
`status: received` means the CLI acknowledged that message. Completion and
submission are serialized: pending unacknowledged input keeps stdin open;
a completed session refuses a new message with 409. Final build checks and
other agent providers currently do not accept live input. There is no fallback
queue or silent cancellation. The existing task deadline still applies.

Run `go test -race ./cmd/runtimed ./internal/runtime ./internal/api` for delivery,
completion, retry-id and tenant tests. `python3 scripts/check-claude-live-input.py`
exercises the installed CLI against a local mock model: a follow-up sent during
a three-second Bash command must appear in the next model request. This uses
an isolated temporary home/workspace and no real provider credentials.
Verified with Claude Code 2.1.251 and 2.1.265.

Deploy the control-plane API and base image together. Existing containers keep
their old runtimed binary until upgraded/recreated. Let active tasks finish,
then install the new binary and restart idle sandboxes, or recreate them from
the updated base image while preserving their workspaces. A task launched with
one-shot input cannot acquire live stdin retroactively.
