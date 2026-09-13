# Conversation-first agent guidance

The parent chat establishes implementation intent. A sandbox task then inspects
relevant current code, respects the requested scope and verifies the user workflow.
Reviews and exploratory tasks do not automatically become application edits.

The shared runtime prompt and all seven template AGENTS.md guides agree on:

- Proportional inspection before editing; starter maps are not live file contents.
- Clear requests proceed without a permission ritual; minor defaults and delegated
  choices do not require a questionnaire.
- A consequential unresolved decision is asked through the bridge. Continue only
  independent work, then record the blocker in BRIEF.md and end the task with an
  accurate report if the answer has not arrived. Do not poll or claim the CLI is
  paused. A later task or live follow-up can carry the answer.
- BRIEF.md keeps product scope, decisions, assumptions and acceptance criteria;
  BRAIN.md keeps technical gotchas. Existing/custom instruction files are preserved.
- User references and project design conventions take precedence over template
  aesthetic defaults. Verification includes relevant interactions and visual review.

CLAUDE.md still imports AGENTS.md; no second policy copy belongs in that file.
`go test ./internal/agentprompt ./cmd/runtimed` from control-plane checks the
embedded prompt and template consistency as well as the existing CLI lifecycle.

The prompt is embedded in the runtime and control plane, and template guides are
copied into the base image. Rebuild/deploy these artifacts to activate the change;
editing source alone does not modify a running container or an existing workspace.
Upgrade idle containers through the normal lifecycle, preserving their files.
Do not overwrite custom guides in existing or remixed projects with template copies.

The paired landing update preserves bridge questions in model history and chat
sync, including questions sent immediately before task completion. It uses the
existing task/steering protocol, not a new suspended-session endpoint.
