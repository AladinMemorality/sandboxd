# Docker project agent incident — 2026-09-25

The reported project remained the shipped waiting screen after two implementation tasks. Both complete briefs reached Claude Code unchanged. Every recorded tool call had a matching result, delivered within 421 ms. The initial task changed no files but was classified successful; the retry changed only CSS and timed out. See [aggregate evidence](result.json).

This reproduces independently of Cube. The concrete completion bug is that an agent exiting successfully is treated as task success even when an explicit build request leaves the starter untouched. A generic “no changed files means failure” rule would incorrectly reject legitimate inspection or already-satisfied tasks; any fix needs the task’s declared implementation intent or an explicit initial-starter acceptance check.

Repeated instruction/source reads suggest a model-level progress failure. The retained evidence rules out brief truncation and missing local tool-result delivery, but does not establish how the external model gateway processed its history. Increasing the timeout alone has not solved this incident.

The production primary model endpoint failed a bounded ten-second read-only probe. The affected account's daily usage contains 94 successful `agent.messages` calls, all attributed to the configured `z-ai/glm-5.3-flash` backup. These are daily aggregates, not per-task request traces. The deployed fallback code allows a 20-second primary connection/header wait after a 15-second process-local cooldown expires. Successful fallback attempts have no timing logs, so the cumulative penalty cannot be established from retained evidence. Observed tool-result-to-next-assistant gaps include model generation and must not be described as measured primary timeouts.

The separately measured owned Cube coding task also timed out with partial edits. Its application container peaked at 283.09 MiB with no observed OOM, swap or memory-pressure stalls; see [the failed coding profile](../coding-profile/failed-coding-2026-09-25.md). This is evidence against resource saturation for that run, not a successful coding, build or higher-concurrency acceptance result.

Raw transcripts remain private because shell results include environment credentials. No user content, raw transcript, credentials, or customer project edits are included here. The investigation used exact-container, UID-1000, bounded read-only file access. No tasks, builds, service changes, or project writes were performed.
