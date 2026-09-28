# Coding task startup and verification

Each runtimed task generates a bounded local workspace inventory before launching
the coding CLI. It lists existing entry/instruction paths, installed package
manifest versions, script names, component exports and the shared test helper
when present. It does not execute commands, read environment values, include
source contents or assume an existing workspace is still the shipped template.
External file links and oversized files are excluded. Component exports are
omitted if the JSON briefing exceeds 12 KB. The agent still reads applicable
instructions and relevant current source.

React Pro and marketplace already include Tailwind 4 and a Radix-based component
kit. Their compact reference explains the common APIs, semantic tokens and
responsive utilities. Agents should compose controls and vary app composition;
they need not rewrite every design token or inactive dark theme. This is an
authoring optimization, not a claim that Tailwind makes every website faster.

`image/agent-tools` installs pinned jsdom/esbuild outside tenant dependencies.
The helper bundles the actual React app in memory and supports focused simulated
DOM interaction checks. It does not verify browser layout, CSS or backend APIs.
Screenshots and applicable real integration checks remain necessary. Unsupported
frameworks/APIs must be reported rather than papered over with silent mocks.

## Measurement contract

- Controller `task_submit_timing` logs request total and stages in milliseconds:
  lifecycle lock wait, Cube preparation, lease, supervisor readiness, config
  synchronization, egress readiness, task persistence, model scope and dispatch.
  Nested spans overlap; do not sum all fields. Failures retain reached stages.
- Runtime `timing` events carry stage, origin and elapsed_ms. `process_started`
  follows successful `cmd.Start`; its duration covers spawn only.
  `cli_initialized`, `first_visible_message`, `first_tool` and
  `first_edit_invocation` are relative to stream-observation startup. These are
  neither model time-to-first-token nor proof of a rendered edit.
- `tool` events pair `running` with `completed`/`error` using `call_id`. Duration
  is observed stream interval, not isolated tool CPU time. Raw output is omitted.
  `is_error` is the CLI tool-result flag; exit_code is included only when explicitly
  supplied by the CLI. Completed means the tool returned, not necessarily that a
  background process finished or all product requirements passed.
- Replayed/duplicate results are ignored; old CLI envelopes without IDs still
  emit invocation events, without fabricated completion or exit codes.

Build new immutable Cube images and validate their starter, agent, preview and
shared test tools before changing template defaults. Keep existing bindings and
workspaces; old snapshots retain their own runtime until separately upgraded.
Publish and warm every configured worker's template artifact before promotion.

For before/after comparisons, hold prompt, preset, model and acceptance criteria
constant. Separate initial and follow-up tasks and warm/cold hosts. Report
creation, dispatch, first useful edit, usable preview, verification, completion,
failure rate and token cost. A single canary establishes functionality, not p95
latency or an average speedup.
